package com.qingwaguagua.imapp

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Application
import android.os.Build
import com.igexin.sdk.PushManager

/** 在 Android 进程创建时注册来电透传服务，避免依赖 MainActivity 已经启动。 */
class LinliApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        createSystemCallNotificationChannels()
        PushManager.getInstance().registerPushIntentService(
            this,
            LinliCallIntentService::class.java,
        )
    }

    /**
     * flutter_callkit_incoming can enter its accept foreground service before
     * its notification manager is attached to a Flutter engine. Android 15
     * terminates the process if that service references a missing channel.
     */
    private fun createSystemCallNotificationChannels() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannels(
            listOf(
                NotificationChannel(
                    "callkit_incoming_channel_id_v2",
                    "音视频来电",
                    NotificationManager.IMPORTANCE_HIGH,
                ).apply {
                    description = "语音和视频来电提醒"
                    lockscreenVisibility = android.app.Notification.VISIBILITY_PUBLIC
                    enableVibration(true)
                },
                NotificationChannel(
                    "callkit_missed_channel_id",
                    "未接来电",
                    NotificationManager.IMPORTANCE_HIGH,
                ).apply {
                    description = "未接语音和视频来电"
                    enableVibration(true)
                },
                NotificationChannel(
                    "callkit_ongoing_channel_id",
                    "通话进行中",
                    NotificationManager.IMPORTANCE_LOW,
                ).apply {
                    description = "正在进行的语音和视频通话"
                    setSound(null, null)
                    enableVibration(false)
                },
            ),
        )
    }
}
