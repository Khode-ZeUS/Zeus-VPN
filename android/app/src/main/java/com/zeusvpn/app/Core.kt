package com.zeusvpn.app

import android.content.Context
import android.content.Intent
import android.net.Uri
import org.json.JSONObject
import zeuscore.Host
import zeuscore.Zeuscore
import java.io.File
import java.util.concurrent.atomic.AtomicBoolean

/**
 * The Go engine (package zeuscore, built with gomobile). It lives as long as
 * the app process, so the VPN keeps running when the UI is closed.
 */
object Core : Host {
    private val started = AtomicBoolean(false)
    private lateinit var context: Context

    /** Receives UI events while the screen is open. */
    @Volatile
    var listener: ((name: String, payload: String) -> Unit)? = null

    fun init(ctx: Context) {
        if (!started.compareAndSet(false, true)) return
        context = ctx.applicationContext
        val dir = File(context.filesDir, "zeus").apply { mkdirs() }
        Zeuscore.start(dir.absolutePath, "android", this, null)
    }

    /** Calls an API method (see internal/appapi) with JSON arguments. */
    fun call(method: String, args: String = "[]"): String = Zeuscore.call(method, args)

    /** The "Connect on launch" setting. */
    fun autoConnectEnabled(): Boolean = runCatching {
        JSONObject(call("GetSnapshot")).getJSONObject("settings").optBoolean("auto_connect")
    }.getOrDefault(false)

    override fun onEvent(name: String, payload: String) {
        if (name == "state") ZeusVpnService.onState(payload)
        listener?.invoke(name, payload)
    }

    override fun openURL(url: String) {
        val intent = Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        runCatching { context.startActivity(intent) }
    }

    override fun openTun(config: String): Int = ZeusVpnService.open(context, JSONObject(config))

    override fun closeTun() = ZeusVpnService.close()
}
