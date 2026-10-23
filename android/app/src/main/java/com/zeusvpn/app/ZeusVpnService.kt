package com.zeusvpn.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.IpPrefix
import android.net.VpnService
import android.os.Build
import org.json.JSONObject
import java.net.InetAddress
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

/**
 * Owns the system VPN interface. The Go engine asks for it through
 * Core.openTun when connecting, and gives it back through Core.closeTun.
 * The app itself is excluded from the VPN, so the engine's own connections
 * to Mozilla go out directly.
 */
class ZeusVpnService : VpnService() {

    companion object {
        private const val CHANNEL = "vpn"
        private const val NOTIFICATION_ID = 1
        private const val ACTION_CONNECT = "com.zeusvpn.app.CONNECT"
        private const val ACTION_DISCONNECT = "com.zeusvpn.app.DISCONNECT"

        // Private networks kept off the VPN when "Bypass local network" is
        // on (172.16.0.0/12 stays in: the VPN's DNS address is there).
        private val LAN = listOf("10.0.0.0" to 8, "192.168.0.0" to 16, "169.254.0.0" to 16)

        @Volatile
        private var instance: ZeusVpnService? = null

        @Volatile
        private var started = CountDownLatch(1)

        @Volatile
        private var lastStatus = "Connecting…"

        /** Called on a Go thread: starts the service and establishes the interface. */
        fun open(ctx: Context, cfg: JSONObject): Int {
            if (prepare(ctx) != null) throw IllegalStateException("VPN permission was not granted")
            val svc = instance ?: run {
                started = CountDownLatch(1)
                startService(ctx, null)
                started.await(10, TimeUnit.SECONDS)
                instance ?: throw IllegalStateException("the VPN service did not start")
            }
            return svc.establish(cfg)
        }

        fun close() {
            instance?.shutdown()
        }

        /** Starts the service, which then connects (after a reboot). */
        fun startAndConnect(ctx: Context) = startService(ctx, ACTION_CONNECT)

        private fun startService(ctx: Context, action: String?) {
            val intent = Intent(ctx, ZeusVpnService::class.java).setAction(action)
            if (Build.VERSION.SDK_INT >= 26) ctx.startForegroundService(intent) else ctx.startService(intent)
        }

        /** Keeps the notification in step with the connection. */
        fun onState(payload: String) {
            val text = runCatching {
                val s = JSONObject(payload)
                when (s.optString("status")) {
                    "connected" -> {
                        val server = s.optJSONObject("server")
                        val where = server?.optString("countryName")
                            ?.takeIf { it.isNotEmpty() && server.optString("countryCode") != "REC" }
                        if (where != null) "Connected · $where" else "Connected"
                    }
                    "reconnecting" -> "Reconnecting…"
                    "disconnecting" -> "Disconnecting…"
                    "disconnected" -> null
                    else -> "Connecting…"
                }
            }.getOrNull() ?: return
            lastStatus = text
            instance?.notifyStatus()
        }
    }

    override fun onCreate() {
        super.onCreate()
        if (Build.VERSION.SDK_INT >= 26) {
            val channel = NotificationChannel(CHANNEL, getString(R.string.channel_name), NotificationManager.IMPORTANCE_LOW)
            channel.setShowBadge(false)
            getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_DISCONNECT) {
            thread { runCatching { Core.call("Disconnect") } }
            if (instance == null) stopSelf()
            return START_NOT_STICKY
        }
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIFICATION_ID, buildNotification(), ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIFICATION_ID, buildNotification())
        }
        instance = this
        started.countDown()
        // Started by the system (always-on VPN) or after a reboot: connect.
        if (intent == null || intent.action == SERVICE_INTERFACE || intent.action == ACTION_CONNECT) {
            Core.init(this)
            thread { runCatching { Core.call("Connect") } }
        }
        return START_NOT_STICKY
    }

    fun establish(cfg: JSONObject): Int {
        val b = Builder()
            .setSession(getString(R.string.app_name))
            .setMtu(cfg.getInt("mtu"))
            .addAddress(cfg.getString("address4"), cfg.getInt("prefix4"))
            .addAddress(cfg.getString("address6"), cfg.getInt("prefix6"))
            .addDnsServer(cfg.getString("dns"))
            .addRoute("0.0.0.0", 0)
            .addRoute("::", 0)
            .addDisallowedApplication(packageName)
            .setConfigureIntent(openAppIntent())
        if (Build.VERSION.SDK_INT >= 29) b.setMetered(false)
        if (cfg.optBoolean("bypassLan") && Build.VERSION.SDK_INT >= 33) {
            for ((addr, len) in LAN) b.excludeRoute(IpPrefix(InetAddress.getByName(addr), len))
        }
        val pfd = b.establish() ?: throw IllegalStateException("VPN permission was not granted")
        return pfd.detachFd()
    }

    fun shutdown() {
        instance = null
        lastStatus = "Connecting…"
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    override fun onRevoke() {
        // Another VPN app took over, or the user turned us off in Settings.
        thread { runCatching { Core.call("Disconnect") } }
    }

    override fun onDestroy() {
        if (instance === this) instance = null
        super.onDestroy()
    }

    private fun openAppIntent(): PendingIntent =
        PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)

    private fun notifyStatus() {
        getSystemService(NotificationManager::class.java)?.notify(NOTIFICATION_ID, buildNotification())
    }

    @Suppress("DEPRECATION")
    private fun buildNotification(): Notification {
        val disconnect = PendingIntent.getService(
            this, 1, Intent(this, ZeusVpnService::class.java).setAction(ACTION_DISCONNECT), PendingIntent.FLAG_IMMUTABLE
        )
        val builder = if (Build.VERSION.SDK_INT >= 26) Notification.Builder(this, CHANNEL) else Notification.Builder(this)
        return builder
            .setSmallIcon(R.drawable.ic_stat_zeus)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(lastStatus)
            .setContentIntent(openAppIntent())
            .setOngoing(true)
            .setShowWhen(false)
            .addAction(Notification.Action.Builder(null, getString(R.string.action_disconnect), disconnect).build())
            .build()
    }
}
