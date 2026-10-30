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
	"sync"
	"sync/atomic"
	"time"
)

const PageURL = "https://accounts.firefox.com/?context=fx_desktop_v3&service=sync&action=email"

var sessionTokenHex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Result reports the session token (and email, when the page shared it), or
// why the flow ended without one.
type Result struct {
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
      if (verified) {
        // Session confirmed: hand it over (navigating away from the page).
        window.location.href = RECV + '/token?token=' + encodeURIComponent(token) +
          '&email=' + encodeURIComponent(data.email || sessionStorage.getItem('zeusEmail') || '') + '&verified=1';
      } else {
        // Session exists but is not verified yet: hold it page-side without
        // navigating — reloading here would interrupt the sign-in flow.
        try { sessionStorage.setItem('zeusToken', token);
              sessionStorage.setItem('zeusEmail', data.email || ''); } catch (e) {}
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
