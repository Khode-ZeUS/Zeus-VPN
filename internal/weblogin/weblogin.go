// Package weblogin runs the Firefox Accounts sign-in inside the app's own
// webview. Fastly's edge answers the API password-login route
// (POST /v1/account/login) with a bare HTTP 406 and the block follows the
// client fingerprint, so the password can no longer be submitted from Go —
// while the sign-in web page (browser TLS) passes. The page hands the
// session token to its embedder through the FxA WebChannel; an injected hook
// forwards it to a loopback receiver. The hook also spoofs a Firefox user
// agent: the content server requires one for the desktop handover flow.
package weblogin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"zeusvpn/internal/store"
)

const PageURL = "https://accounts.firefox.com/?context=fx_desktop_v3&service=sync&action=email"

var sessionTokenHex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Result reports the outcome: the finished OAuth tokens (exchanged inside
// the page, where the browser's TLS passes networks that drop Go's), or the
// raw session token for the engine to exchange itself, or the failure.
type Result struct {
	Tokens       *store.Tokens
	SessionToken string
	Email        string
	Err          error
}

// Login is one web sign-in flow: a loopback receiver plus the pieces the
// platform webview needs (page URL, hook script, watchdog state).
type Login struct {
	ln       net.Listener
	receiver string
	result   chan Result
	mu       sync.Mutex
	pending  string // unverified token, delivered once verification shows
	pendMail string
	stopped  bool

	hookSeen atomic.Int64 // unix ms of the last hook ping
}

// Start opens the loopback receiver.
func Start() (*Login, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("could not open the sign-in receiver: %w", err)
	}
	l := &Login{ln: ln, receiver: "http://" + ln.Addr().String(), result: make(chan Result, 4)}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", l.token)
	mux.HandleFunc("/ping", l.ping)
	mux.HandleFunc("/cancel", l.cancel)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5}
	go srv.Serve(ln)
	return l, nil
}

// Stop closes the receiver; the flow is over either way.
func (l *Login) Stop() {
	l.mu.Lock()
	l.stopped = true
	l.mu.Unlock()
	l.ln.Close()
}

func (l *Login) deliver(r Result) {
	l.mu.Lock()
	stopped := l.stopped
	l.mu.Unlock()
	if stopped {
		return
	}
	select {
	case l.result <- r:
	default:
	}
}

// token handles the session handover. The hook delivers it via a top-level
// navigation (GET with query parameters) because a cross-origin fetch from
// the FxA page to the loopback receiver is blocked by its CSP and by the
// browser's Private Network Access rules. After the token is recorded the
// receiver redirects the webview back to the sign-in page.
func (l *Login) token(w http.ResponseWriter, r *http.Request) {
	l.hookSeen.Store(time.Now().UnixMilli())
	q := r.URL.Query()
	token := q.Get("token")
	email := q.Get("email")
	verified := q.Get("verified") != "false"
	ping := q.Get("ping")

	if r.Method == http.MethodPost {
		var in struct {
			Token    string `json:"token"`
			Email    string `json:"email"`
			Verified *bool  `json:"verified"`
			Ping     string `json:"ping"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err == nil {
			token, email = in.Token, in.Email
			if in.Verified != nil {
				verified = *in.Verified
			}
			ping = in.Ping
		}
	}

	if at := q.Get("access_token"); at != "" {
		// The page exchanged the session for OAuth tokens itself (its TLS
		// passes networks that drop Go's); store them without Go touching
		// the network.
		expires, _ := strconv.Atoi(q.Get("expires_in"))
		if expires <= 0 {
			expires = 86400
		}
		tokens := store.Tokens{AccessToken: at, RefreshToken: q.Get("refresh_token"),
			Scope: "profile https://identity.mozilla.com/apps/vpn", ObtainedAt: time.Now(), ExpiresIn: int64(expires)}
		l.mu.Lock()
		l.pending, l.pendMail = "", ""
		l.mu.Unlock()
		l.deliver(Result{Tokens: &tokens, Email: q.Get("email")})
		w.Header().Set("Location", PageURL)
		w.WriteHeader(302)
		return
	}
	if token == "" && ping != "" {
		// A verification/success screen was reached: a held token is usable.
		l.Verified()
		w.WriteHeader(200)
		return
	}
	if !sessionTokenHex.MatchString(token) {
		w.WriteHeader(400)
		return
	}

	l.mu.Lock()
	if verified {
		l.pending, l.pendMail = "", ""
		l.mu.Unlock()
		l.deliver(Result{SessionToken: token, Email: email})
	} else {
		l.pending, l.pendMail = token, email
		l.mu.Unlock()
	}
	// Send the webview back to the sign-in page; the platform will take it
	// to the app UI once the token exchange finishes.
	w.Header().Set("Location", PageURL)
	w.WriteHeader(302)
}

// ping records that the hook is alive on the sign-in page (watchdog input).
func (l *Login) ping(w http.ResponseWriter, r *http.Request) {
	l.hookSeen.Store(time.Now().UnixMilli())
	w.WriteHeader(200)
}

// cancel handles the user pressing the injected cancel button.
func (l *Login) cancel(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	l.deliver(Result{Err: errors.New("canceled")})
}

// Verified is called by the platform when the page reaches a verification
// or success screen: a held unverified token becomes deliverable.
func (l *Login) Verified() {
	l.mu.Lock()
	token, email := l.pending, l.pendMail
	l.pending, l.pendMail = "", ""
	l.mu.Unlock()
	if sessionTokenHex.MatchString(token) {
		l.deliver(Result{SessionToken: token, Email: email})
	}
}

// Result delivers the outcome of the flow.
func (l *Login) Result() <-chan Result { return l.result }

// HookSeen reports the unix-ms time of the last hook ping (0 = never).
func (l *Login) HookSeen() int64 { return l.hookSeen.Load() }

// HookJS is injected into the sign-in page (idempotent; safe to run on every
// poll). It spoofs a Firefox user agent, answers the page's WebChannel
// questions, forwards the session-token handover to the loopback receiver,
// detects verification screens, and adds a floating cancel button.
func (l *Login) HookJS() string {
	hook := "(function(){\n"
	hook += "if (window.__zeusHooked) return; window.__zeusHooked = true;\n"
	hook += "var RECV = " + jsString(l.receiver) + ";\n"
	hook += firefoxUASpoof
	hook += webChannelReply
	hook += webChannelForward
	hook += successWatch
	hook += inPageExchange
	hook += cancelButton
	hook += "})();"
	return hook
}

const firefoxUASpoof = `try {
  var ffUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:132.0) Gecko/20100101 Firefox/132.0";
  Object.defineProperty(navigator, 'userAgent', {get: function(){ return ffUA; }, configurable: true});
  Object.defineProperty(navigator, 'vendor', {get: function(){ return ''; }, configurable: true});
} catch (e) {}
`

const webChannelReply = `var reply = function(messageId, command, data) {
  var envelope = {id: 'account_updates', message: {messageId: messageId, command: command, data: data}};
  [window, document].forEach(function(t) {
    try { t.dispatchEvent(new CustomEvent('WebChannelMessageToContent', {detail: envelope})); } catch (e) {}
    try { t.dispatchEvent(new CustomEvent('WebChannelMessageToContent', {detail: JSON.stringify(envelope)})); } catch (e) {}
  });
};
`

const webChannelForward = `var forward = function(detail) {
  try {
    var host = window.location.hostname;
    if (host !== 'accounts.firefox.com' && !host.endsWith('.firefox.com')) return;
    var msg = {}; try { msg = (typeof detail === 'string' ? JSON.parse(detail) : detail).message || {}; } catch (e) {}
    var cmd = msg.command || ''; var data = msg.data || {};
    if (cmd === 'fxaccounts:fxa_status') { reply(msg.messageId, cmd, {capabilities:{}, signedInUser:null}); return; }
    if (cmd === 'fxaccounts:can_link_account') { reply(msg.messageId, cmd, {ok:true}); return; }
    var token = String(data.sessionToken || data.token || '').trim();
    var verified = (data.verified === undefined) ? true : !!data.verified;
    if (cmd === 'fxaccounts:login' || cmd === 'fxaccounts:oauth_login' || /verified/i.test(cmd)) {
      if (msg.messageId) reply(msg.messageId, cmd, {ok:true});
      if (!token) return;
      var email = data.email || sessionStorage.getItem('zeusEmail') || '';
      if (verified) {
        // Session confirmed: exchange it for OAuth tokens right here — the
        // browser's TLS passes networks that drop Go's. Falls back to
        // handing the raw session to the engine if the exchange fails.
        zeusExchange(token, email);
      } else {
        // Session exists but is not verified yet: hold it page-side without
        // navigating — reloading here would interrupt the sign-in flow.
        try { sessionStorage.setItem('zeusToken', token);
              sessionStorage.setItem('zeusEmail', email); } catch (e) {}
      }
      return;
    }
    if (msg.messageId) reply(msg.messageId, cmd, {ok:true});
  } catch (e) {}
};
window.addEventListener('WebChannelMessageToChrome', function(e) { forward(e.detail); });
document.addEventListener('WebChannelMessageToChrome', function(e) { forward(e.detail); });
`

const successWatch = `var pingSuccess = function() {
  try {
    var u = window.location.href.toLowerCase();
    var st = sessionStorage.getItem('zeusToken') || '';
    var success = u.indexOf('/signin_confirmed') !== -1 || u.indexOf('connect_another_device') !== -1 ||
                  u.indexOf('/settings') !== -1 || u.indexOf('/complete') !== -1 || u.indexOf('success') !== -1;
    if (st && success) {
      window.location.href = RECV + '/token?token=' + encodeURIComponent(st) +
        '&email=' + encodeURIComponent(sessionStorage.getItem('zeusEmail') || '') + '&verified=1';
    }
  } catch (e) {}
};
if (window.fetch) {
  var origFetch = window.fetch;
  window.fetch = function() {
    var url = (arguments[0] && arguments[0].url) || arguments[0] || '';
    return origFetch.apply(this, arguments).then(function(res) {
      try { if (res && res.ok && String(url).indexOf('/session/verify') !== -1) pingSuccess(); } catch (e) {}
      return res;
    });
  };
}
var nav = function() { try { pingSuccess(); } catch (e) {} };
var ps = history.pushState; if (ps) history.pushState = function() { var r = ps.apply(this, arguments); nav(); return r; };
var rs = history.replaceState; if (rs) history.replaceState = function() { var r = rs.apply(this, arguments); nav(); return r; };
window.addEventListener('popstate', nav);
`

const inPageExchange = `var zeusExchange = function(sessionToken, email) {
  var enc = new TextEncoder();
  var b64 = function(buf) { var s = btoa(String.fromCharCode.apply(null, new Uint8Array(buf))); return s; };
  var hex = function(b) { return Array.prototype.map.call(new Uint8Array(b), function(x) { return ('0' + x.toString(16)).slice(-2); }).join(''); };
  return crypto.subtle.importKey('raw', new Uint8Array(sessionToken.match(/.{2}/g).map(function(h) { return parseInt(h, 16); })), 'HKDF', false, ['deriveBits'])
    .then(function(base) {
      return crypto.subtle.deriveBits({name: 'HKDF', hash: 'SHA-256', salt: new Uint8Array(32),
        info: enc.encode('identity.mozilla.com/picl/v1/sessionToken')}, base, 512);
    })
    .then(function(expanded) {
      expanded = new Uint8Array(expanded);
      var tokenID = hex(expanded.slice(0, 32));
      var ts = String(Math.floor(Date.now() / 1000));
      var nb = crypto.getRandomValues(new Uint8Array(6));
      var nonce = b64(nb).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
      var body = JSON.stringify({client_id: '5882386c6d801776', grant_type: 'fxa-credentials',
        scope: 'profile https://identity.mozilla.com/apps/vpn', access_type: 'offline'});
      return crypto.subtle.digest('SHA-256', enc.encode('hawk.1.payload\napplication/json\n' + body + '\n'))
        .then(function(ph) {
          var payloadHash = b64(ph);
          var normalized = ['hawk.1.header', ts, nonce, 'POST', '/v1/oauth/token',
            'api.accounts.firefox.com', '443', payloadHash, '', ''].join('\n');
          return crypto.subtle.importKey('raw', expanded.slice(32), {name: 'HMAC', hash: 'SHA-256'}, false, ['sign'])
            .then(function(hk) { return crypto.subtle.sign('HMAC', hk, enc.encode(normalized)); })
            .then(function(mac) {
              var header = 'Hawk id="' + tokenID + '", ts="' + ts + '", nonce="' + nonce +
                '", mac="' + mac + '", hash="' + payloadHash + '"';
              return fetch('https://api.accounts.firefox.com/v1/oauth/token', {method: 'POST',
                headers: {'Content-Type': 'application/json', 'Authorization': header}, body: body});
            });
        })
        .then(function(res) {
          if (!res.ok) throw new Error('HTTP ' + res.status);
          return res.json();
        })
        .then(function(out) {
          window.location.href = RECV + '/token?access_token=' + encodeURIComponent(out.access_token || '') +
            '&refresh_token=' + encodeURIComponent(out.refresh_token || '') +
            '&expires_in=' + (out.expires_in || 86400) + '&email=' + encodeURIComponent(email) + '&exchange=1';
        });
    })
    .catch(function(e) {
      window.location.href = RECV + '/token?token=' + encodeURIComponent(sessionToken) +
        '&email=' + encodeURIComponent(email) + '&verified=1';
    });
};
`

const cancelButton = `if (!document.getElementById('zeus-cancel')) {
  var b = document.createElement('button');
  b.id = 'zeus-cancel';
  b.textContent = "\u2190 Zeus VPN";
  b.style.cssText = 'position:fixed;top:10px;right:10px;z-index:2147483647;padding:8px 14px;border-radius:10px;' +
    'background:#262a4d;color:#fff;border:1px solid rgba(255,255,255,0.2);font:600 13px sans-serif;cursor:pointer;';
  b.onclick = function() { window.location.href = RECV + '/cancel'; };
  document.body.appendChild(b);
}
`

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
