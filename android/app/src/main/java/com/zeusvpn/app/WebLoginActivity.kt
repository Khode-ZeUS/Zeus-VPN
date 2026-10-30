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
  var reply = function(messageId, command, data) {
    var envelope = {id: 'account_updates', message: {messageId: messageId, command: command, data: data}};
    [window, document].forEach(function(t) {
      try { t.dispatchEvent(new CustomEvent('WebChannelMessageToContent', {detail: envelope})); } catch (e) {}
      try { t.dispatchEvent(new CustomEvent('WebChannelMessageToContent', {detail: JSON.stringify(envelope)})); } catch (e) {}
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
        zeusWebLogin.onLogin(token, data.email || '', verified);
        if (msg.messageId) reply(msg.messageId, cmd, {ok:true});
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

        @JavascriptInterface
        fun onLogin(token: String, email: String, verified: Boolean) {
            if (!SESSION_TOKEN_HEX.matches(token.trim())) return
            val complete = verified || pending != null
            pending = token.trim()
            if (email.isNotBlank()) pendingEmail = email
            if (complete) finishLogin()
        }

        @JavascriptInterface
        fun onVerified() {
            // The page reached a verification/success screen: an unverified
            // token held earlier is now usable.
            if (pending != null) finishLogin()
        }

        private fun finishLogin() {
            val token = pending ?: return
            runOnUiThread {
                try {
                    Core.call("CompleteWebLogin", """[${JSONObject.quote(pendingEmail)}, ${JSONObject.quote(token)}]""")
                    setResult(RESULT_OK)
                } catch (e: Exception) {
                    setResult(RESULT_CANCELED)
                }
                finish()
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
