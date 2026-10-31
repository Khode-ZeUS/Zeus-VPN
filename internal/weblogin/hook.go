package weblogin

import (
	"encoding/json"
	"regexp"
)

var sessionTokenHex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// HookJS returns the shared sign-in hook injected into the Firefox Accounts
// page on every platform (desktop webview, Android WebView, iOS WKWebView).
// It handles the WebChannel handshake, spoofs a Firefox user agent (the
// content server requires one for the desktop handover flow), exchanges the
// session for OAuth tokens inside the page (the browser's TLS passes
// networks that drop Go's), and reports results through a delivery callback
// the host provides.
//
// deliver is a JavaScript expression taking one string argument — the result
// query ("tokens?...", "session?...", "cancel") — defined by the host, e.g.
//
//	function(q) { window.webkit.messageHandlers.zeusWebLogin.postMessage(q); }
//
// deliverBase is the loopback receiver base URL used for the one-shot page
// trail ("" on platforms without a receiver). The host must also define
// window.__zeusDeliver with the same semantics before navigating to PageURL,
// and re-run this script on every page load (it is idempotent).
func HookJS(deliver, deliverBase, nonce string) string {
	hook := "(function(){\n"
	hook += "if (window.__zeusHooked) return; window.__zeusHooked = true;\n"
	hook += "var exchangeFailed = false;\n"
	hook += "var NONCE = " + jsString(nonce) + ";\n"
	hook += "var __deliver = " + jsString(deliver) + ";\n"
	hook += "var __deliverBase = " + jsString(deliverBase) + ";\n"
	hook += uaSpoof
	hook += webChannelReply
	hook += webChannelForward
	hook += inPageExchange
	hook += successWatch
	hook += navTrail
	hook += cancelButton
	hook += "})();"
	return hook
}

// InjectJS returns the script the host runs on every page load: the delivery
// shim followed by the shared hook (both idempotent).
func InjectJS(deliver, deliverBase, nonce string) string {
	return "window.__zeusDeliver = function(q) { __deliver(q); };\n" + HookJS(deliver, deliverBase, nonce)
}

// Result query kinds delivered to the host.
const (
	DeliverTokens  = "tokens"  // finished OAuth tokens
	DeliverSession = "session" // raw session token (engine exchanges it)
	DeliverCancel  = "cancel"
)

const uaSpoof = `try {
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
    var email = String(data.email || '');
    var verified = (data.verified === undefined) ? true : !!data.verified;
    if (cmd === 'fxaccounts:login' || cmd === 'fxaccounts:oauth_login' || /verified/i.test(cmd)) {
      if (msg.messageId) reply(msg.messageId, cmd, {ok:true});
      if (!token) return;
      if (verified) {
        zeusExchange(token, email);
      } else {
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

const inPageExchange = `var zeusExchange = function(sessionToken, email) {
  var enc = new TextEncoder();
  var b64 = function(buf) { var s = btoa(String.fromCharCode.apply(null, new Uint8Array(buf))); return s; };
  crypto.subtle.importKey('raw', new Uint8Array(sessionToken.match(/.{2}/g).map(function(h) { return parseInt(h, 16); })), 'HKDF', false, ['deriveBits'])
    .then(function(base) {
      return crypto.subtle.deriveBits({name: 'HKDF', hash: 'SHA-256', salt: new Uint8Array(32),
        info: enc.encode('identity.mozilla.com/picl/v1/sessionToken')}, base, 512);
    })
    .then(function(expanded) {
      expanded = new Uint8Array(expanded);
      var tokenID = Array.prototype.map.call(expanded.slice(0, 32), function(x) { return ('0' + x.toString(16)).slice(-2); }).join('');
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
              var macB64 = b64(mac); // subtle.sign yields an ArrayBuffer
              var header = 'Hawk id="' + tokenID + '", ts="' + ts + '", nonce="' + nonce +
                '", mac="' + macB64 + '", hash="' + payloadHash + '"';
              return fetch('https://api.accounts.firefox.com/v1/oauth/token', {method: 'POST',
                headers: {'Content-Type': 'application/json', 'Authorization': header}, body: body});
            });
        })
        .then(function(res) {
          if (!res.ok) throw new Error('HTTP ' + res.status);
          return res.json();
        })
        .then(function(out) {
          __deliver('tokens?access_token=' + encodeURIComponent(out.access_token || '') +
            '&refresh_token=' + encodeURIComponent(out.refresh_token || '') +
            '&expires_in=' + (out.expires_in || 86400) + '&email=' + encodeURIComponent(email));
        });
    })
    .catch(function(e) {
      exchangeFailed = true;
      try {
        var st = sessionStorage.getItem('zeusToken') || '';
        if (st) { __deliver('session?token=' + encodeURIComponent(st) +
          '&email=' + encodeURIComponent(sessionStorage.getItem('zeusEmail') || '') + '&verified=1'); return; }
      } catch (e2) {}
      __deliver('session?token=' + encodeURIComponent(sessionToken) + '&email=' + encodeURIComponent(email) + '&verified=1');
    });
};
`

const successWatch = `var pingSuccess = function() {
  try {
    var u = window.location.href.toLowerCase();
    var st = sessionStorage.getItem('zeusToken') || '';
    var success = u.indexOf('/signin_confirmed') !== -1 || u.indexOf('connect_another_device') !== -1 ||
                  u.indexOf('/settings') !== -1 || u.indexOf('/complete') !== -1 || u.indexOf('success') !== -1;
    if (st && success && !exchangeFailed) {
      zeusExchange(st, sessionStorage.getItem('zeusEmail') || '');
    } else if (st && success) {
      __deliver('session?token=' + encodeURIComponent(st) +
        '&email=' + encodeURIComponent(sessionStorage.getItem('zeusEmail') || '') + '&verified=1');
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

const navTrail = `try {
  if (location.hostname.indexOf('firefox.com') !== -1 && __deliverBase && !sessionStorage.getItem('zeusNav')) {
    sessionStorage.setItem('zeusNav', '1');
    var i = new Image();
    i.src = __deliverBase + 'nav?href=' + encodeURIComponent(location.href) + '&nonce=' + NONCE;
  }
} catch (e) {}
`

const cancelButton = `if (!document.getElementById('zeus-cancel')) {
  var b = document.createElement('button');
  b.id = 'zeus-cancel';
  b.textContent = "\u2190 Zeus VPN";
  b.style.cssText = 'position:fixed;top:10px;right:10px;z-index:2147483647;padding:8px 14px;border-radius:10px;' +
    'background:#262a4d;color:#fff;border:1px solid rgba(255,255,255,0.2);font:600 13px sans-serif;cursor:pointer;';
  b.onclick = function() { __deliver('cancel'); };
  document.body.appendChild(b);
}
`

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
