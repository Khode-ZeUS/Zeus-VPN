package com.zeusvpn.app

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.Canvas
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.util.Base64
import android.view.WindowInsets
import android.webkit.JavascriptInterface
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.webkit.WebViewAssetLoader
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayOutputStream
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
        // Edge-to-edge (targetSdk 35): the page keeps clear of the system
        // bars itself, using the real inset sizes injected as CSS variables —
        // env(safe-area-inset-*) alone returns 0 on many Android WebViews.
        // The keyboard still resizes the WebView.
        web.setOnApplyWindowInsetsListener { v, insets ->
            if (Build.VERSION.SDK_INT >= 30) {
                val bars = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout())
                val ime = insets.getInsets(WindowInsets.Type.ime())
                v.setPadding(ime.left, ime.top, ime.right, ime.bottom)
                setSafeArea(bars.top, bars.bottom, bars.left, bars.right)
            } else {
                @Suppress("DEPRECATION")
                setSafeArea(insets.systemWindowInsetTop, insets.systemWindowInsetBottom, insets.systemWindowInsetLeft, insets.systemWindowInsetRight)
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

            override fun onPageFinished(view: WebView, url: String) {
                // Re-apply the insets now that the page can receive them.
                view.requestApplyInsets()
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

    /** Publishes the system bar sizes to the page as CSS variables. */
    private fun setSafeArea(top: Int, bottom: Int, left: Int, right: Int) {
        runOnUiThread {
            js(
                "var d=document.documentElement.style;" +
                    "d.setProperty('--sa-top','${top}px');" +
                    "d.setProperty('--sa-bottom','${bottom}px');" +
                    "d.setProperty('--sa-left','${left}px');" +
                    "d.setProperty('--sa-right','${right}px');"
            )
        }
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
            if (method == "GetApps") {
                resolve(id, true, installedApps().toString())
                return
            }
            resolve(id, true, Core.call(method, args.toString()))
        } catch (e: Exception) {
            resolve(id, false, e.message ?: e.toString())
        }
    }

    /** Installed launchable apps for the per-app tunnel picker. */
    private fun installedApps(): JSONArray {
        val intent = Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER)
        val out = JSONArray()
        val seen = HashSet<String>()
        // Icons travel as small base64 PNGs: 48dp (capped at 128px) stays
        // crisp in the list while keeping the whole payload light.
        val iconSize = (resources.displayMetrics.density * 48).toInt().coerceIn(48, 128)
        val resolved = packageManager.queryIntentActivities(intent, 0)
        for (ri in resolved) {
            val pkg = ri.activityInfo?.packageName ?: continue
            if (pkg == packageName || !seen.add(pkg)) continue
            var label = pkg
            try {
                label = ri.loadLabel(packageManager).toString()
            } catch (e: Exception) {
                // fall back to the package id
            }
            val item = JSONObject().put("id", pkg).put("name", label)
            try {
                val drawable = ri.loadIcon(packageManager)
                val bmp = Bitmap.createBitmap(iconSize, iconSize, Bitmap.Config.ARGB_8888)
                val canvas = Canvas(bmp)
                drawable.setBounds(0, 0, iconSize, iconSize)
                drawable.draw(canvas)
                val bos = ByteArrayOutputStream()
                bmp.compress(Bitmap.CompressFormat.PNG, 100, bos)
                bmp.recycle()
                item.put("icon", Base64.encodeToString(bos.toByteArray(), Base64.NO_WRAP))
            } catch (e: Exception) {
                // an app without a usable icon still shows up, just plain
            }
            out.put(item)
        }
        // Deterministic order: label first, then package id.
        val items = ArrayList<JSONObject>()
        for (i in 0 until out.length()) items.add(out.optJSONObject(i))
        items.sortWith { a, b ->
            val byName = a.optString("name").lowercase().compareTo(b.optString("name").lowercase())
            if (byName != 0) byName else a.optString("id").compareTo(b.optString("id"))
        }
        val sorted = JSONArray()
        for (o in items) sorted.put(o)
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
