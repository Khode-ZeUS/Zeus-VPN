package com.zeusvpn.app

import android.app.Application

class ZeusApp : Application() {
    override fun onCreate() {
        super.onCreate()
        Core.init(this)
    }
}
