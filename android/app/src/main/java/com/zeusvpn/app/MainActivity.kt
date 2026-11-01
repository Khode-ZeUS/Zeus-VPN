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
import java.util.concurrent.CancellationException
import java.util.concurrent.CompletableFuture
import java.util.concurrent.ExecutionException
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException

/**
 * Shows the shared web UI (frontend/dist, packaged as assets) and connects
 * it to the Go engine: frontend/dist/bridge.js posts calls to [Bridge.post]
 * and receives results and events through window.__zeus.
 */
class MainActivity : Activity() {
    companion object {
        private const val REQ_VPN = 1
        private const val REQ_NOTIFICATIONS = 2
        private const val REQ_WEB_LOGIN = 3
        private const val VPN_PERMISSION_TIMEOUT_MS = 30_000L
        private const val ORIGIN = "https://appassets.androidplatform.net"
    }

    private lateinit var web: WebView
    private val executor = Executors.newCachedThreadPool()

    private val vpnPermissionLock = Any()

    @Volatile
    private var vpnPermission: CompletableFuture<Boolean>? = null

    @Volatile
    private var vpnPermissionRequestActive = false

    @Volatile
    private var activityDestroyed = false

    @Volatile
    private var webLoginActive = false

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
                // The shared UI hides controls whose native API contract is not
                // available on this Android release (route exclusion needs API 33).
                js("document.documentElement.dataset.androidApi='${Build.VERSION.SDK_INT}';window.applyPlatform&&window.applyPlatform();window.renderSettings&&window.renderSettings()")
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
            if (activityDestroyed) return
            try {
                executor.execute { handle(message) }
            } catch (error: java.util.concurrent.RejectedExecutionException) {
                // The Activity was destroyed while a WebView callback was in flight.
            }
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
            // Web sign-in runs in a native WebView (the FxA page hands the
            // session over through its WebChannel).
            if (method == "StartWebLogin") {
                runOnUiThread {
                    if (activityDestroyed) {
                        resolve(id, false, "the application is closing")
                    } else if (webLoginActive) {
                        resolve(id, false, "sign-in already in progress")
                    } else {
                        webLoginActive = true
                        try {
                            startActivityForResult(android.content.Intent(this, WebLoginActivity::class.java), REQ_WEB_LOGIN)
                            resolve(id, true, "null")
                        } catch (error: Exception) {
                            webLoginActive = false
                            resolve(id, false, error.message ?: "could not open sign-in")
                        }
                    }
                }
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

    /** Asks for VPN permission without waiting forever on the bridge worker. */
    private fun ensureVpnPermission(): Boolean {
        if (activityUnavailable()) return false
        val intent = try {
            VpnService.prepare(this)
        } catch (error: Exception) {
            return false
        } ?: return true

        val result = CompletableFuture<Boolean>()
        synchronized(vpnPermissionLock) {
            if (activityUnavailable() || vpnPermissionRequestActive) return false
            vpnPermission = result
            vpnPermissionRequestActive = true
        }

        runOnUiThread {
            val current = synchronized(vpnPermissionLock) {
                vpnPermission === result && vpnPermissionRequestActive && !result.isDone
            }
            if (!current || activityUnavailable()) {
                cancelVpnPermission(result)
                return@runOnUiThread
            }
            try {
                startActivityForResult(intent, REQ_VPN)
            } catch (error: Exception) {
                completeVpnPermission(result, false)
            }
        }

        return try {
            result.get(VPN_PERMISSION_TIMEOUT_MS, TimeUnit.MILLISECONDS)
        } catch (error: InterruptedException) {
            Thread.currentThread().interrupt()
            cancelVpnPermission(result)
            false
        } catch (error: TimeoutException) {
            // The system permission UI may still deliver a late result; keep
            // the request marker until that callback so it cannot complete a
            // newer request accidentally.
            cancelVpnPermission(result)
            false
        } catch (error: CancellationException) {
            false
        } catch (error: ExecutionException) {
            false
        } finally {
            synchronized(vpnPermissionLock) {
                if (vpnPermission === result && result.isDone && !vpnPermissionRequestActive) {
                    vpnPermission = null
                }
            }
        }
    }

    private fun completeVpnPermission(result: CompletableFuture<Boolean>, allowed: Boolean) {
        synchronized(vpnPermissionLock) {
            if (vpnPermission !== result || !vpnPermissionRequestActive) return
            vpnPermissionRequestActive = false
            vpnPermission = null
            result.complete(allowed)
        }
    }

    private fun cancelVpnPermission(result: CompletableFuture<Boolean>) {
        synchronized(vpnPermissionLock) {
            if (vpnPermission !== result || !vpnPermissionRequestActive) return
            // Keep the active marker until Android sends the matching result;
            // a late callback must not resolve a future for a different flow.
            result.cancel(false)
        }
    }

    private fun activityUnavailable(): Boolean =
        activityDestroyed || isFinishing || (android.os.Build.VERSION.SDK_INT >= 17 && isDestroyed)

    @Deprecated("Activity result API")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode == REQ_VPN) {
            val pending = synchronized(vpnPermissionLock) {
                val current = vpnPermission
                vpnPermission = null
                vpnPermissionRequestActive = false
                current
            }
            pending?.complete(resultCode == RESULT_OK)
        } else if (requestCode == REQ_WEB_LOGIN) {
            webLoginActive = false
            if (resultCode != RESULT_OK) {
                val message = data?.getStringExtra(WebLoginActivity.EXTRA_ERROR)
                    ?: "Sign-in cancelled."
                runOnUiThread {
                    if (!activityUnavailable()) {
                        js("window.__zeus && window.__zeus.emit('notice', ${JSONObject.quote(message)})")
                    }
                }
            }
        }
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
    }

    @Deprecated("Back gesture API")
    override fun onBackPressed() {
        // Close an open panel or dialog first; otherwise keep the app (and VPN) running.
        web.evaluateJavascript(
            "(function(){if(document.querySelector('.modal-panel:not([hidden]),.panel-scrim:not([hidden]),.modal:not([hidden])')){" +
                "document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape'}));return true}return false})()"
        ) { handled -> if (handled != "true") moveTaskToBack(true) }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
    }

    override fun onDestroy() {
        activityDestroyed = true
        webLoginActive = false
        val pendingPermission = synchronized(vpnPermissionLock) {
            val current = vpnPermission
            vpnPermission = null
            vpnPermissionRequestActive = false
            current
        }
        pendingPermission?.cancel(false)
        executor.shutdownNow()
        Core.listener = null
        if (::web.isInitialized) web.destroy()
        super.onDestroy()
    }
}
