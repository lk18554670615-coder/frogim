package com.linlitong.imapp

import android.app.Application
import com.igexin.sdk.PushManager

/** 在 Android 进程创建时注册来电透传服务，避免依赖 MainActivity 已经启动。 */
class LinliApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        PushManager.getInstance().registerPushIntentService(
            this,
            LinliCallIntentService::class.java,
        )
    }
}
