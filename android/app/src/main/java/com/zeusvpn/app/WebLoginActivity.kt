package com.zeusvpn.app

import android.annotation.SuppressLint
import android.app.Activity
import android.os.Bundle
import android.webkit.JavascriptInterface
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.webkit.WebViewCompat
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
         * TEMPORARY on-page diagnostic panel for the mobile sign-in freeze:
         * logs the boot user agent, WebChannel traffic, every network call
         * (a call without a "->" line is the stalled one), SPA navigations,
         * rAF liveness and JS errors. Collapsed to a small bar; tap to
         * expand. Remove once the flow is confirmed fixed.
         */
        private const val DEBUG_OVERLAY_JS = """
(function(){
if (window.__zdbg) return; window.__zdbg = true;
var T0 = Date.now();
var t = function(){ return ((Date.now() - T0) / 1000).toFixed(1); };
window.__zdbgLines = [];
var el = null;
var render = function(){ try { if (el) el.textContent = window.__zdbgLines.slice(-30).join('\n'); } catch(e){} };
var log = function(s){ try { window.__zdbgLines.push(t() + ' ' + s); render(); } catch(e){} };
var boot = function(){ try {
  if (document.getElementById('__zdbg')) return;
  el = document.createElement('div');
  el.id = '__zdbg';
  el.style.cssText = 'position:fixed;left:3px;bottom:3px;z-index:2147483646;max-width:88vw;' +
    'padding:2px 5px;border-radius:5px;background:rgba(0,0,0,.5);color:#8fffab;' +
    'font:8px/1.25 monospace;white-space:pre-wrap;overflow:hidden;max-height:2.6em;';
  el.textContent = 'dbg';
  el.onclick = function(){ el.style.maxHeight = el.style.maxHeight === '60vh' ? '2.6em' : '60vh'; render(); };
  (document.body || document.documentElement).appendChild(el);
} catch(e){} };
if (document.readyState === 'loading') { document.addEventListener('DOMContentLoaded', boot); } else { boot(); }
if (window.__zdbgLate) log('LATE-INJECT (no document-start)');
log('UA ' + (navigator.userAgent || '').slice(0, 64));
try { log('vis ' + (document.visibilityState || '?')); } catch(e){}
window.addEventListener('error', function(e){ log('ERR ' + (e.message || '?').slice(0, 90)); });
window.addEventListener('unhandledrejection', function(e){
  var r = e.reason || '';
  log('REJ ' + (r.message || String(r)).slice(0, 100));
});
['WebChannelMessageToChrome', 'WebChannelMessageToContent'].forEach(function(name){
  window.addEventListener(name, function(e){ try {
    var d = e.detail; var m = (typeof d === 'string' ? JSON.parse(d) : d) || {};
    log((name === 'WebChannelMessageToChrome' ? 'WC> ' : 'WC< ') + ((m.message && m.message.command) || '?'));
  } catch(e2){ log(name + ' ?'); } });
});
try {
  var of = window.fetch;
  if (of) window.fetch = function(){ try {
    var input = arguments[0];
    var u = (input && input.url) || input || '';
    var m2 = (arguments[1] && arguments[1].method) || 'GET';
    var tag = 'F ' + m2 + ' ' + String(u).replace(/^https?:\/\/[^\/]+/, '').slice(0, 60);
    log(tag);
    return of.apply(this, arguments).then(function(r){ log(tag + ' -> ' + r.status); return r; },
      function(e2){ log(tag + ' !! ' + ((e2 && e2.message) || 'fail').slice(0, 50)); throw e2; });
  } catch(e3){ return of.apply(this, arguments); } };
} catch(e){}
try {
  var oo = XMLHttpRequest.prototype.open, os = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.open = function(m, u){ try {
    this.__ztag = 'X ' + m + ' ' + String(u).replace(/^https?:\/\/[^\/]+/, '').slice(0, 60);
  } catch(e){} return oo.apply(this, arguments); };
  XMLHttpRequest.prototype.send = function(){ try {
    var tg = this.__ztag || 'X'; log(tg);
    this.addEventListener('loadend', function(){ log(tg + ' -> ' + this.status); });
  } catch(e){} return os.apply(this, arguments); };
} catch(e){}
try {
  var ps = history.pushState;
  if (ps) history.pushState = function(){ try {
    log('NAV ' + String(arguments[2] || '').slice(0, 60));
  } catch(e){} return ps.apply(this, arguments); };
} catch(e){}
try {
  var fired = false;
  var oraf = window.requestAnimationFrame;
  if (oraf) window.requestAnimationFrame = function(cb){
    var done = false;
    var wcb = function(x){ if (!done) { done = true; if (!fired) { fired = true; log('rAF ok'); } return cb(x); } };
    var id = oraf.call(window, wcb);
    setTimeout(function(){ if (!done) { done = true; if (!fired) { fired = true; log('rAF STALLED'); } cb(performance.now()); } }, 400);
    return id;
  };
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
            // The content server only boots its desktop WebChannel flow for a
            // Firefox user agent — with the WebView's real Chromium UA the
            // page never leaves its loading spinner (tested on device). Use
            // the desktop Firefox UA the injected hook spoofs anyway, so the
            // HTTP headers and the in-page JS agree.
            userAgentString = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:132.0) Gecko/20100101 Firefox/132.0"
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
        // The hook must land BEFORE the page's own scripts so the WebChannel
        // handshake is caught from the very first message (like the desktop's
        // repeated early injection). Document-start injection is attempted
        // unconditionally; WebViews without the support (or a failed call)
        // fall back to the late onPageFinished injection, which the page
        // tolerates: it settles the boot handshake after a short timeout and
        // asks can_link_account only when the user submits the form.
        var docStart = false
        try {
            WebViewCompat.addDocumentStartJavaScript(web,
                DEBUG_OVERLAY_JS + "\n" + hook, setOf("https://accounts.firefox.com"))
            docStart = true
        } catch (e: Throwable) {
            // fall through to the late injection below
        }
        web.webViewClient = object : WebViewClient() {
            override fun onPageFinished(view: WebView, url: String) {
                if (docStart) return
                view.evaluateJavascript("window.__zdbgLate = true;", null)
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
