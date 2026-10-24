package com.zeusvpn.app

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.view.WindowInsets
import android.webkit.JavascriptInterface
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.webkit.WebViewAssetLoader
import org.json.JSONArray
import org.json.JSONObject
import java.util.concurrent.CompletableFuture
import java.util.concurrent.Executors

/**
 * Shows the shared web UI (frontend/dist, packaged as assets) and connects
 * it to the Go engine: frontend/dist/bridge.js posts calls to [Bridge.post]
 * and receives results and events through window.__zeus.
 */
class MainActivity : Activity() {
    companion object {
        private const val REQ_VPN = 1
        private const val REQ_NOTIFICATIONS = 2
        private const val ORIGIN = "https://appassets.androidplatform.net"
    }

    private lateinit var web: WebView
    private val executor = Executors.newCachedThreadPool()

    @Volatile
    private var vpnPermission: CompletableFuture<Boolean>? = null

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Core.init(this)
        WebView.setWebContentsDebuggingEnabled(applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0)

        web = WebView(this)
        web.setBackgroundColor(0xFF090B1A.toInt())
        setContentView(web)
        // Keep the page clear of the status bar, navigation bar and keyboard.
        web.setOnApplyWindowInsetsListener { v, insets ->
            if (Build.VERSION.SDK_INT >= 30) {
                val i = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.ime() or WindowInsets.Type.displayCutout())
                v.setPadding(i.left, i.top, i.right, i.bottom)
            } else {
                @Suppress("DEPRECATION")
                v.setPadding(insets.systemWindowInsetLeft, insets.systemWindowInsetTop, insets.systemWindowInsetRight, insets.systemWindowInsetBottom)
            }
            insets
        }

        val assets = WebViewAssetLoader.Builder()
            .addPathHandler("/assets/", WebViewAssetLoader.AssetsPathHandler(this))
            .build()
        web.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = false
            allowContentAccess = false
            textZoom = 100
        }
        web.webViewClient = object : WebViewClient() {
            override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? =
                assets.shouldInterceptRequest(request.url)

            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                if (request.url.toString().startsWith(ORIGIN)) return false
                runCatching { startActivity(Intent(Intent.ACTION_VIEW, request.url)) }
                return true
            }
        }
        web.addJavascriptInterface(Bridge(), "ZeusAndroid")
        Core.listener = { name, payload ->
            runOnUiThread { js("window.__zeus && window.__zeus.emit(${JSONObject.quote(name)}, ${JSONObject.quote(payload)})") }
        }
        web.loadUrl("$ORIGIN/assets/index.html")

        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), REQ_NOTIFICATIONS)
        }
        // "Connect on launch" needs the VPN permission granted earlier.
        if (savedInstanceState == null && VpnService.prepare(this) == null) {
            executor.execute { runCatching { zeuscore.Zeuscore.autoConnect(false) } }
        }
    }

    private fun js(code: String) {
        if (!isDestroyed) web.evaluateJavascript(code, null)
    }

    private fun resolve(id: Int, ok: Boolean, payload: String) = runOnUiThread {
        js("window.__zeus.resolve($id, $ok, ${JSONObject.quote(payload)})")
    }

    inner class Bridge {
        @JavascriptInterface
        fun post(message: String) {
            executor.execute { handle(message) }
        }
    }

    private fun handle(message: String) {
        val msg = JSONObject(message)
        val id = msg.getInt("id")
        val method = msg.getString("method")
        val args = msg.optJSONArray("args") ?: JSONArray()
        try {
            if (method == "Connect" && !ensureVpnPermission()) throw IllegalStateException("VPN permission was not granted")
            // The app list lives on the Android side, not in Go.
            if (method == "GetApps") return resolve(id, true, installedApps().toString())
            resolve(id, true, Core.call(method, args.toString()))
        } catch (e: Exception) {
            resolve(id, false, e.message ?: e.toString())
        }
    }

    /** Installed launchable apps for the per-app tunnel picker. */
    private fun installedApps(): JSONArray {
        val intent = Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER)
        val out = JSONArray()
        val seen = sortedSetOf<String>()
        for (ri in packageManager.queryIntentActivities(intent, 0)) {
            val pkg = ri.activityInfo?.packageName ?: continue
            if (pkg == packageName || !seen.add(pkg)) continue
            val label = runCatching { ri.loadLabel(packageManager).toString() }.getOrNull() ?: pkg
            out.put(JSONObject().put("id", pkg).put("name", label))
        }
        // Deterministic order: label first, then package id.
        val arr = (0 until out.length()).map { out.optJSONObject(it)!! }
            .sortedWith(compareBy({ it.optString("name").lowercase() }, { it.optString("id") }))
        val sorted = JSONArray()
        for (o in arr) sorted.put(o)
        return sorted
    }

    /** Asks for the VPN permission if needed; blocks a worker thread. */
    private fun ensureVpnPermission(): Boolean {
        val intent = VpnService.prepare(this) ?: return true
        val result = CompletableFuture<Boolean>()
        vpnPermission = result
        runOnUiThread { startActivityForResult(intent, REQ_VPN) }
        return result.get()
    }

    @Deprecated("Activity result API")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode == REQ_VPN) vpnPermission?.complete(resultCode == RESULT_OK)
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
    }

    @Deprecated("Back gesture API")
    override fun onBackPressed() {
        // Close an open sheet first; otherwise keep the app (and VPN) running.
        web.evaluateJavascript(
            "(function(){if(document.querySelector('.sheet:not([hidden]),.modal:not([hidden])')){" +
                "document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape'}));return true}return false})()"
        ) { handled -> if (handled != "true") moveTaskToBack(true) }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
    }

    override fun onDestroy() {
        Core.listener = null
        vpnPermission?.complete(false)
        web.destroy()
        super.onDestroy()
    }
}
