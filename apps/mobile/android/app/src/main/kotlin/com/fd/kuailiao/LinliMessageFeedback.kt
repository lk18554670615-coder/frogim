package com.fd.kuailiao

import android.app.Activity
import android.app.NotificationManager
import android.media.AudioAttributes
import android.media.AudioManager
import android.media.Ringtone
import android.media.RingtoneManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.VibrationEffect
import android.os.Vibrator
import androidx.core.app.NotificationManagerCompat
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodChannel

/** Online IM feedback; never creates a second system notification. */
class LinliMessageFeedback(private val activity: Activity, messenger: BinaryMessenger) {
    private val handler = Handler(Looper.getMainLooper())
    private var ringtone: Ringtone? = null
    private val channel = MethodChannel(messenger, "com.fd.kuailiao/message_feedback")
    private val attributes = AudioAttributes.Builder()
        .setUsage(AudioAttributes.USAGE_NOTIFICATION)
        .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
        .build()

    init {
        channel.setMethodCallHandler { call, result ->
            if (call.method != "play") {
                result.notImplemented()
            } else {
                runCatching { play(call.argument<Boolean>("sound") == true, call.argument<Boolean>("vibration") == true) }
                    .fold({ result.success(null) }, { result.error("message_feedback_unavailable", it.javaClass.simpleName, null) })
            }
        }
    }

    @Suppress("DEPRECATION")
    private fun play(sound: Boolean, vibration: Boolean) {
        if (activity.isFinishing || activity.isDestroyed || !activity.hasWindowFocus()) return
        if (!NotificationManagerCompat.from(activity).areNotificationsEnabled()) return
        val manager = activity.getSystemService(NotificationManager::class.java)
        // Do not bypass system DND, quiet notification channels or silent mode.
        if (manager.currentInterruptionFilter != NotificationManager.INTERRUPTION_FILTER_ALL) return
        val audio = activity.getSystemService(AudioManager::class.java)
        if (audio.mode != AudioManager.MODE_NORMAL || audio.ringerMode == AudioManager.RINGER_MODE_SILENT) return
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N && audio.activeRecordingConfigurations.isNotEmpty()) return
        val settings = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) manager.getNotificationChannel("messages") else null
        if (settings?.importance == NotificationManager.IMPORTANCE_NONE) return
        stop()
        if (sound && audio.ringerMode == AudioManager.RINGER_MODE_NORMAL &&
            (settings == null || (settings.importance >= NotificationManager.IMPORTANCE_DEFAULT && settings.sound != null))) {
            val uri = settings?.sound ?: RingtoneManager.getDefaultUri(RingtoneManager.TYPE_NOTIFICATION)
            ringtone = RingtoneManager.getRingtone(activity, uri)?.apply {
                audioAttributes = attributes
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) isLooping = false
                play()
            }
            handler.postDelayed({ stop() }, 2000)
        }
        if (vibration && (settings == null || settings.shouldVibrate())) {
            val vibrator = activity.getSystemService(Vibrator::class.java)
            if (vibrator.hasVibrator()) {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                    vibrator.vibrate(VibrationEffect.createOneShot(160, VibrationEffect.DEFAULT_AMPLITUDE), attributes)
                } else {
                    vibrator.vibrate(longArrayOf(0, 160), -1, attributes)
                }
            }
        }
    }

    fun stop() {
        handler.removeCallbacksAndMessages(null)
        ringtone?.stop()
        ringtone = null
    }

    fun dispose() {
        stop()
        channel.setMethodCallHandler(null)
    }
}
