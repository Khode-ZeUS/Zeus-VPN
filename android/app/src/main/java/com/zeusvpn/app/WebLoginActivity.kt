package com.zeusvpn.app

import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.webkit.JavascriptInterface
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.webkit.WebViewCompat
import org.json.JSONObject
import java.net.URLDecoder
import java.util.concurrent.Executors
import java.util.concurrent.Future
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Signs in on the Firefox Accounts web page. The API password-login route is
 * edge-blocked, but the page signs in on its own origin and hands the session
 * token to its embedder through the FxA WebChannel; an injected hook forwards
 * it to a JavascriptInterface bridge, which completes the sign-in through the
 * Go engine.
 */
class WebLoginActivity : Activity() {

    private lateinit var web: WebView

    // Core.call may exchange tokens or fetch the account profile over the
    // network. Keep it off the WebView/UI thread and bound the wait so a
    // stalled endpoint cannot leave this Activity or its worker pending.
    private val completionExecutor = Executors.newSingleThreadExecutor { runnable ->
        Thread(runnable, "ZeusWebLogin").apply { isDaemon = true }
    }
    private val timeoutExecutor = Executors.newSingleThreadScheduledExecutor { runnable ->
        Thread(runnable, "ZeusWebLoginTimeout").apply { isDaemon = true }
    }
    private val completionStarted = AtomicBoolean(false)
    private val completionTerminal = AtomicBoolean(false)
    private val destroyed = AtomicBoolean(false)

    @Volatile
    private var completionTask: Future<*>? = null

    @Volatile
    private var timeoutTask: ScheduledFuture<*>? = null

    companion object {
        private const val PAGE_URL =
            "https://accounts.firefox.com/?context=fx_desktop_v3&service=sync&action=email"
        private const val LOGIN_TIMEOUT_MS = 45_000L
        const val EXTRA_ERROR = "zeus.login.error"
        private val SESSION_TOKEN_HEX = Regex("^[0-9a-f]{64}$")
    }

    /** Receives the shared hook's deliveries; mirrors the desktop receiver. */
    inner class Bridge {
        @JavascriptInterface
        fun deliver(q: String) {
            if (destroyed.get() || completionStarted.get()) return
            val delivery = try {
                parseDelivery(q)
            } catch (error: Exception) {
                finishCompletion(RESULT_CANCELED, "The sign-in response was invalid.")
                return
            }
            when (delivery.first) {
                "tokens", "session" -> completeLogin(delivery.first, delivery.second)
                "cancel" -> cancelLogin()
            }
        }
    }

    private fun parseDelivery(q: String): Pair<String, Map<String, String>> {
        val kind = q.substringBefore('?').removePrefix("/")
        val query = q.substringAfter('?', "")
        val params = query.split('&')
            .filter { it.isNotEmpty() }
            .mapNotNull { item ->
                val parts = item.split('=', limit = 2)
                if (parts.size != 2) return@mapNotNull null
                parts[0] to URLDecoder.decode(parts[1], "UTF-8")
            }
            .toMap()
        if (kind.isEmpty()) throw IllegalArgumentException("missing delivery kind")
        return kind to params
    }

    private fun completeLogin(kind: String, params: Map<String, String>) {
        if (!completionStarted.compareAndSet(false, true)) return
        val email = params["email"] ?: ""
        val task = try {
            completionExecutor.submit {
                if (destroyed.get() || completionTerminal.get()) return@submit
                try {
                    when (kind) {
                        "tokens" -> {
                            val access = params["access_token"].orEmpty()
                            if (access.isEmpty()) throw IllegalArgumentException("the sign-in page did not return an access token")
                            val refresh = params["refresh_token"].orEmpty()
                            val expires = params["expires_in"]?.toLongOrNull()?.takeIf { it > 0 } ?: 86400L
                            Core.call(
                                "CompleteWebLoginToken",
                                "[${JSONObject.quote(email)}, ${JSONObject.quote(access)}, ${JSONObject.quote(refresh)}, $expires]"
                            )
                        }
                        "session" -> {
                            val token = params["token"].orEmpty()
                            if (!SESSION_TOKEN_HEX.matches(token)) {
                                throw IllegalArgumentException("the sign-in page returned an invalid session token")
                            }
                            Core.call(
                                "CompleteWebLogin",
                                "[${JSONObject.quote(email)}, ${JSONObject.quote(token)}]"
                            )
                        }
                    }
                    finishCompletion(RESULT_OK, null)
                } catch (error: Throwable) {
                    if (error is InterruptedException) {
                        Thread.currentThread().interrupt()
                        return@submit
                    }
                    finishCompletion(RESULT_CANCELED, readableError(error))
                }
            }
        } catch (error: RejectedExecutionException) {
            finishCompletion(RESULT_CANCELED, "Sign-in could not start. Please try again.")
            return
        }
        completionTask = task

        val timeout = try {
            timeoutExecutor.schedule({
                if (!completionTerminal.get()) {
                    completionTask?.cancel(true)
                    finishCompletion(RESULT_CANCELED, "Sign-in timed out. Please try again.")
                }
            }, LOGIN_TIMEOUT_MS, TimeUnit.MILLISECONDS)
        } catch (error: RejectedExecutionException) {
            completionTask?.cancel(true)
            finishCompletion(RESULT_CANCELED, "Sign-in could not be completed. Please try again.")
            return
        }
        timeoutTask = timeout
        if (completionTerminal.get()) timeout.cancel(false)
    }

    private fun cancelLogin() {
        if (!completionStarted.compareAndSet(false, true)) return
        finishCompletion(RESULT_CANCELED, "Sign-in cancelled.")
    }

    private fun readableError(error: Throwable): String {
        val detail = error.message?.trim()?.takeIf { it.isNotEmpty() }
        return if (detail == null) "Sign-in failed. Please try again." else "Sign-in failed: ${detail.take(300)}"
    }

    private fun finishCompletion(resultCode: Int, error: String?) {
        completionStarted.set(true)
        if (!completionTerminal.compareAndSet(false, true)) return
        timeoutTask?.cancel(false)
        if (resultCode != RESULT_OK) completionTask?.cancel(true)
        if (destroyed.get()) return
        runOnUiThread {
            if (destroyed.get()) return@runOnUiThread
            val data = error?.let { Intent().putExtra(EXTRA_ERROR, it) }
            setResult(resultCode, data)
            finish()
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
            // browser UA passes. The page's boot check reads navigator.userAgent,
            // which the document-start hook spoofs to Firefox before scripts run.
        }
        web.addJavascriptInterface(Bridge(), "zeusWebLogin")
        // The shared hook (WebChannel + in-page exchange) comes from the Go
        // engine so every platform runs the same logic; this side only
        // provides the delivery bridge. The nonce argument only matters to
        // the desktop loopback delivery, so it is empty here.
        val deliver = "function(q) { zeusWebLogin.deliver(q); }"
        val hook = runCatching {
            Core.call("WebLoginHook", "[" + JSONObject.quote(deliver) + ", \"\"]")
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
        destroyed.set(true)
        completionStarted.set(true)
        completionTerminal.set(true)
        timeoutTask?.cancel(false)
        completionTask?.cancel(true)
        timeoutExecutor.shutdownNow()
        completionExecutor.shutdownNow()
        if (::web.isInitialized) {
            web.stopLoading()
            web.removeJavascriptInterface("zeusWebLogin")
            web.destroy()
        }
        super.onDestroy()
    }
}
