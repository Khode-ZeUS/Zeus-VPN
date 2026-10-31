package com.zeusvpn.app

import android.annotation.SuppressLint
import android.app.Activity
import android.os.Bundle
import android.webkit.JavascriptInterface
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.webkit.WebViewClient
import org.json.JSONObject

/**
 * Signs in on the Firefox Accounts web page. The API password-login route is
 * edge-blocked (HTTP 406), but the page signs in on its own origin and hands
 * the session token to its embedder through the FxA WebChannel; an injected
 * hook forwards it to a JavascriptInterface bridge, which completes the
 * sign-in through the Go engine.
 */
class WebLoginActivity : Activity() {

    private lateinit var web: WebView

    companion object {
        private const val PAGE_URL =
            "https://accounts.firefox.com/?context=fx_desktop_v3&service=sync&action=email"
        private const val CHANNEL = "account_updates"
        private val SESSION_TOKEN_HEX = Regex("^[0-9a-f]{64}$")

        private val HOOK_JS = """
(function() {
  if (window.__zeusHooked) return; window.__zeusHooked = true;
  var exchangeFailed = false;
  var reply = function(messageId, command, data) {
    var envelope = {id: 'account_updates', message: {messageId: messageId, command: command, data: data}};
    [window, document].forEach(function(t) {
      try { t.dispatchEvent(new CustomEvent('WebChannelMessageToContent', {detail: envelope})); } catch (e) {}
      try { t.dispatchEvent(new CustomEvent('WebChannelMessageToContent', {detail: JSON.stringify(envelope)})); } catch (e) {}
    });
  };
  var zeusExchange = function(sessionToken, email) {
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
            zeusWebLogin.onTokens(out.access_token || '', out.refresh_token || '',
              String(out.expires_in || 86400), email);
          });
      })
      .catch(function(e) {
        // The in-page exchange failed: fall back to the engine's own exchange.
        exchangeFailed = true;
        zeusWebLogin.onLogin(sessionToken, email, true);
      });
  };
  var forward = function(detail) {
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
          zeusExchange(token, data.email || '');
        } else {
          zeusWebLogin.onLogin(token, data.email || '', verified);
        }
        return;
      }
      if (msg.messageId) reply(msg.messageId, cmd, {ok:true});
    } catch (e) {}
  };
  window.addEventListener('WebChannelMessageToChrome', function(e) { forward(e.detail); });
  document.addEventListener('WebChannelMessageToChrome', function(e) { forward(e.detail); });
  var pingSuccess = function() {
    try {
      var u = window.location.href.toLowerCase();
      if (u.indexOf('/verify') !== -1 || u.indexOf('/totp') !== -1 || u.indexOf('/signin_confirmed') !== -1 ||
          u.indexOf('connect_another_device') !== -1 || u.indexOf('/settings') !== -1 || u.indexOf('/complete') !== -1) {
        zeusWebLogin.onVerified();
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
})();
""".trimIndent()
    }

    /** Receives WebChannel messages; mirrors the FxA desktop handover. */
    inner class Bridge {
        private var pending: String? = null
        private var pendingEmail = ""

        private var exchangeFailed = false

        @JavascriptInterface
        fun onLogin(token: String, email: String, verified: Boolean) {
            if (!SESSION_TOKEN_HEX.matches(token.trim())) return
            pending = token.trim()
            if (email.isNotBlank()) pendingEmail = email
            if (verified && !exchangeFailed) runExchange(token.trim(), pendingEmail)
        }

        @JavascriptInterface
        fun onVerified() {
            // The page reached a verification/success screen: the session
            // held earlier is verified — exchange it inside the page (the
            // browser TLS passes networks that drop Go's).
            val token = pending ?: return
            if (!exchangeFailed) runExchange(token, pendingEmail) else finishLogin(token, pendingEmail)
        }

        @JavascriptInterface
        fun onTokens(accessToken: String, refreshToken: String, expiresIn: String, email: String) {
            // The page already exchanged the session for OAuth tokens with
            // its own TLS; the engine only stores them.
            runOnUiThread {
                try {
                    Core.call("CompleteWebLoginToken",
                        "[${JSONObject.quote(email)}, ${JSONObject.quote(accessToken)}, ${JSONObject.quote(refreshToken)}, $expiresIn]")
                    setResult(RESULT_OK)
                } catch (e: Exception) {
                    setResult(RESULT_CANCELED)
                }
                finish()
            }
        }

        private fun finishLogin(token: String, email: String) {
            // Fallback: the engine exchanges the session itself (fails on
            // networks whose edge blocks non-browser clients).
            runOnUiThread {
                try {
                    Core.call("CompleteWebLogin", """[${JSONObject.quote(email)}, ${JSONObject.quote(token)}]""")
                    setResult(RESULT_OK)
                } catch (e: Exception) {
                    setResult(RESULT_CANCELED)
                }
                finish()
            }
        }

        private fun runExchange(token: String, email: String) {
            runOnUiThread {
                web.evaluateJavascript("zeusExchange(${JSONObject.quote(token)}, ${JSONObject.quote(email)})", null)
            }
        }
    }

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        web = WebView(this)
        setContentView(web)
        web.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            // The content server only runs the desktop WebChannel handover
            // for a Firefox user agent.
            userAgentString = "Mozilla/5.0 (Android 14; Mobile; rv:132.0) Gecko/132.0 Firefox/132.0"
        }
        web.addJavascriptInterface(Bridge(), "zeusWebLogin")
        web.webViewClient = object : WebViewClient() {
            override fun onPageFinished(view: WebView, url: String) {
                view.evaluateJavascript(HOOK_JS, null)
            }
        }
        web.webChromeClient = WebChromeClient()
        web.loadUrl(PAGE_URL)
    }

    override fun onBackPressed() {
        if (web.canGoBack()) web.goBack() else super.onBackPressed()
    }

    override fun onDestroy() {
        web.destroy()
        super.onDestroy()
    }
}
