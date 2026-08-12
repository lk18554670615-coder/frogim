package com.linlitong.imapp

import android.app.Activity
import android.os.Build
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.embedding.android.FlutterActivity
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterActivity() {
    private val screenshotChannelName = "com.linlitong.imapp/screenshot"
    private var screenshotChannel: MethodChannel? = null
    private var screenshotRequested = false
    private var activityStarted = false
    private var screenshotCallbackRegistered = false

    private val screenshotCallback =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            Activity.ScreenCaptureCallback {
                screenshotChannel?.invokeMethod(
                    "detected",
                    mapOf("occurredAt" to System.currentTimeMillis()),
                )
            }
        } else {
            null
        }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        screenshotChannel = MethodChannel(
            flutterEngine.dartExecutor.binaryMessenger,
            screenshotChannelName,
        ).also { channel ->
            channel.setMethodCallHandler { call, result ->
                when (call.method) {
                    "start" -> {
                        screenshotRequested = true
                        updateScreenshotRegistration()
                        result.success(
                            mapOf(
                                "supported" to (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE),
                                "minimumApi" to Build.VERSION_CODES.UPSIDE_DOWN_CAKE,
                            ),
                        )
                    }
                    "stop" -> {
                        screenshotRequested = false
                        updateScreenshotRegistration()
                        result.success(null)
                    }
                    else -> result.notImplemented()
                }
            }
        }
    }

    override fun onStart() {
        super.onStart()
        activityStarted = true
        updateScreenshotRegistration()
    }

    override fun onStop() {
        activityStarted = false
        updateScreenshotRegistration()
        super.onStop()
    }

    override fun onDestroy() {
        screenshotRequested = false
        updateScreenshotRegistration()
        screenshotChannel?.setMethodCallHandler(null)
        screenshotChannel = null
        super.onDestroy()
    }

    private fun updateScreenshotRegistration() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.UPSIDE_DOWN_CAKE) return
        val callback = screenshotCallback ?: return
        val shouldRegister = screenshotRequested && activityStarted
        if (shouldRegister && !screenshotCallbackRegistered) {
            try {
                registerScreenCaptureCallback(mainExecutor, callback)
                screenshotCallbackRegistered = true
            } catch (_: SecurityException) {
                // Do not crash on an OEM manifest merge that drops the
                // install-time permission declared by the production app.
                screenshotCallbackRegistered = false
            }
        } else if (!shouldRegister && screenshotCallbackRegistered) {
            unregisterScreenCaptureCallback(callback)
            screenshotCallbackRegistered = false
        }
    }
}
