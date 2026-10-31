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
            // Keep the WebView's REAL user agent on the wire — exactly like
            // the working desktop build. The API edge block follows the
            // client fingerprint: a forged Firefox UA on a Chromium TLS
            // stack is stalled on the password-login route, while the real
            // browser UA passes (panel data: account/status 200 with both,
            // the login step only passes with the real UA). The page's boot
            // check reads navigator.userAgent, which the document-start
            // hook spoofs to Firefox before the page's scripts run.
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
            WebViewCompat.addDocumentStartJavaScript(web, hook, setOf("https://accounts.firefox.com"))
            docStart = true
        } catch (e: Throwable) {
            // fall through to the late injection below
        }
        web.webViewClient = object : WebViewClient() {
            override fun onPageFinished(view: WebView, url: String) {
                if (docStart) return
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
