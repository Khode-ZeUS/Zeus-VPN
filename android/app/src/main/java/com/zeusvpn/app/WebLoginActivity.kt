package com.zeusvpn.app

import android.annotation.SuppressLint
import android.app.Activity
import android.os.Bundle
import android.webkit.JavascriptInterface
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
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

        /**
         * TEMPORARY on-page diagnostic panel for the sign-in freeze: logs the
         * WebChannel traffic, every network call (with its status — a stalled
         * call is the freeze fingerprint), SPA navigations, deliveries and JS
         * errors. Tap the panel to expand it. Remove once the flow is fixed.
         */
        private const val DEBUG_OVERLAY_JS = """
(function(){
if (window.__zdbg) return; window.__zdbg = true;
window.__zdbgLines = [];
var render = function(){ try {
  var el = document.getElementById('__zdbg');
  if (el) el.textContent = window.__zdbgLines.slice(-9).join('\n');
} catch(e){} };
var log = function(s){ try {
  window.__zdbgLines.push(Math.floor(performance.now()/1000) + ' ' + s);
  render();
} catch(e){} };
var boot = function(){ try {
  if (document.getElementById('__zdbg')) return;
  var d = document.createElement('div');
  d.id = '__zdbg';
  d.style.cssText = 'position:fixed;left:4px;bottom:4px;z-index:2147483646;max-width:96vw;' +
    'padding:6px 8px;border-radius:8px;background:rgba(0,0,0,.82);color:#7fff9e;' +
    'font:10px/1.35 monospace;white-space:pre-wrap;overflow:hidden;max-height:22vh;';
  d.textContent = 'dbg';
  d.onclick = function(){ d.style.maxHeight = d.style.maxHeight === '70vh' ? '22vh' : '70vh'; render(); };
  (document.body || document.documentElement).appendChild(d);
  log('UA ' + (navigator.userAgent || '').slice(0, 70));
  log('url ' + String(location.href).replace(/^https?:\/\//, '').slice(0, 70));
} catch(e){} };
if (document.readyState === 'loading') { document.addEventListener('DOMContentLoaded', boot); } else { boot(); }
window.addEventListener('error', function(e){
  log('ERR ' + (e.message || '?').slice(0, 90) + ' @' + String(e.filename || '').split('/').pop() + ':' + e.lineno);
});
window.addEventListener('unhandledrejection', function(e){
  var r = e.reason || '';
  log('REJ ' + (r.message || String(r)).slice(0, 110));
});
['WebChannelMessageToChrome', 'WebChannelMessageToContent'].forEach(function(name){
  window.addEventListener(name, function(e){ try {
    var d = e.detail; var m = (typeof d === 'string' ? JSON.parse(d) : d) || {};
    var msg = m.message || {};
    log((name === 'WebChannelMessageToChrome' ? 'WC> ' : 'WC< ') + (msg.command || '?') +
      (name === 'WebChannelMessageToContent' ? ' ' + JSON.stringify(msg.data || '').slice(0, 50) : ''));
  } catch(e2){ log(name + ' ?'); } });
});
try {
  var of = window.fetch;
  if (of) window.fetch = function(){ try {
    var input = arguments[0];
    var url = (input && input.url) || input || '';
    var meth = (arguments[1] && arguments[1].method) || (input && input.method) || 'GET';
    var tag = 'F ' + meth + ' ' + String(url).replace(/^https?:\/\/[^\/]+/, '').slice(0, 72);
    log(tag + ' ...');
    return of.apply(this, arguments).then(function(r){ log(tag + ' -> ' + r.status); return r; },
      function(e2){ log(tag + ' !! ' + ((e2 && e2.message) || 'failed').slice(0, 60)); throw e2; });
  } catch(e3){ return of.apply(this, arguments); } };
} catch(e){}
try {
  var oo = XMLHttpRequest.prototype.open, os = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.open = function(m, u){ try {
    this.__ztag = 'X ' + m + ' ' + String(u).replace(/^https?:\/\/[^\/]+/, '').slice(0, 72);
  } catch(e){} return oo.apply(this, arguments); };
  XMLHttpRequest.prototype.send = function(){ try {
    var t = this.__ztag || 'X'; log(t + ' ...');
    this.addEventListener('loadend', function(){ log(t + ' -> ' + this.status); });
  } catch(e){} return os.apply(this, arguments); };
} catch(e){}
try {
  var ps = history.pushState;
  if (ps) history.pushState = function(){ try {
    log('NAV ' + String(arguments[2] || '').slice(0, 72));
  } catch(e){} return ps.apply(this, arguments); };
} catch(e){}
try {
  var wrapDeliver = function(){ try {
    if (typeof window.__zeusDeliver === 'function' && !window.__zeusDeliver.__wrapped) {
      var od = window.__zeusDeliver;
      var nd = function(q){ try { log('DLV ' + String(q).split('?')[0]); } catch(e){} return od(q); };
      nd.__wrapped = true;
      window.__zeusDeliver = nd;
    }
  } catch(e){} };
  wrapDeliver();
  setInterval(wrapDeliver, 1000);
} catch(e){}
})();
"""
    }

    /** Receives the shared hook's deliveries; mirrors the desktop receiver. */
    inner class Bridge {
        @JavascriptInterface
        fun deliver(q: String) {
            val kind = q.substringBefore('?')
            val params = q.substringAfter('?', "")
                .split('&')
                .mapNotNull {
                    val p = it.split('=', limit = 2)
                    if (p.size == 2) p[0] to java.net.URLDecoder.decode(p[1], "UTF-8") else null
                }
                .toMap()
            runOnUiThread {
                try {
                    when (kind.removePrefix("/")) {
                        "tokens" -> {
                            Core.call("CompleteWebLoginToken",
                                "[${JSONObject.quote(params["email"] ?: "")}, ${JSONObject.quote(params["access_token"] ?: "")}, ${JSONObject.quote(params["refresh_token"] ?: "")}, ${params["expires_in"] ?: "86400"}]")
                            setResult(RESULT_OK)
                        }
                        "session" -> {
                            Core.call("CompleteWebLogin",
                                "[${JSONObject.quote(params["email"] ?: "")}, ${JSONObject.quote(params["token"] ?: "")}]")
                            setResult(RESULT_OK)
                        }
                        "cancel" -> setResult(RESULT_CANCELED)
                    }
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
            // Keep the WebView's own real-browser user agent. Fastly
            // fingerprints the API password-login route and stalls exactly
            // the client that claims Firefox in its headers while being a
            // Chromium WebView underneath; the desktop build passes the same
            // route with its real Chromium UA. The injected hook spoofs
            // Firefox inside the page for the WebChannel handshake instead.
        }
        web.addJavascriptInterface(Bridge(), "zeusWebLogin")
        // The shared hook (WebChannel + in-page exchange) comes from the Go
        // engine so every platform runs the same logic; this side only
        // provides the delivery bridge. The nonce argument only matters to
        // the desktop loopback delivery, so it is empty here.
        val deliver = "function(q) { zeusWebLogin.deliver(q); }"
        val hook = runCatching {
            Core.call("WebLoginHook", "[" + org.json.JSONObject.quote(deliver) + ", \"\"]")
        }.getOrDefault("")
        // The hook has to run BEFORE the page's own scripts: the content
        // server only boots its desktop WebChannel flow for a Firefox user
        // agent, so the hook's in-page spoof must land first — a late
        // (onPageFinished) injection leaves the page spinning on its loading
        // screen. Document-start injection also catches the WebChannel
        // handshake from the very first message, like the desktop's
        // repeated early injection. The WebView's HTTP user agent stays the
        // real one: Fastly fingerprints the API password-login route and
        // stalls a claimed-Firefox Chromium client (the desktop build passes
        // that route with its real Chromium UA). WebViews without the
        // document-start support fall back to the late injection.
        val injectAtDocStart =
            WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)
        if (injectAtDocStart) {
            WebViewCompat.addDocumentStartJavaScript(web,
                DEBUG_OVERLAY_JS + "\n" + hook, setOf("https://accounts.firefox.com"))
        }
        web.webViewClient = object : WebViewClient() {
            override fun onPageFinished(view: WebView, url: String) {
                if (injectAtDocStart) return
                view.evaluateJavascript(DEBUG_OVERLAY_JS, null)
                view.evaluateJavascript(hook, null)
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
