package com.zeusvpn.app

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.net.VpnService

/** Reconnects after a reboot when "Connect on launch" is on. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_BOOT_COMPLETED) return
        Core.init(context)
        if (VpnService.prepare(context) != null || !Core.autoConnectEnabled()) return
        ZeusVpnService.startAndConnect(context)
    }
}
