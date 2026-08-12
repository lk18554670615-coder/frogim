package com.linlitong.imapp

import android.app.Activity
import android.content.Intent
import android.os.Build
import android.os.Bundle
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.embedding.android.FlutterActivity
import io.flutter.plugin.common.MethodChannel
import org.json.JSONArray
import org.json.JSONObject

class MainActivity : FlutterActivity() {
    private val screenshotChannelName = "com.linlitong.imapp/screenshot"
    private val systemCallChannelName = "com.linlitong.imapp/system_calls"
    private var screenshotChannel: MethodChannel? = null
    private var systemCallChannel: MethodChannel? = null
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

    override fun onCreate(savedInstanceState: Bundle?) {
        captureSystemCallAction(intent)
        super.onCreate(savedInstanceState)
    }

    override fun onNewIntent(intent: Intent) {
        captureSystemCallAction(intent)
        super.onNewIntent(intent)
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        systemCallChannel = MethodChannel(
            flutterEngine.dartExecutor.binaryMessenger,
            systemCallChannelName,
        ).also { channel ->
            channel.setMethodCallHandler { call, result ->
                when (call.method) {
                    "drainLaunchActions" -> result.success(drainSystemCallActions())
                    else -> result.notImplemented()
                }
            }
        }
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
        systemCallChannel?.setMethodCallHandler(null)
        systemCallChannel = null
        super.onDestroy()
    }

    /**
     * The CallKit plugin starts the launcher Activity with the accepted call
     * in its Intent. If Android reclaimed Flutter, that event can be emitted
     * before Dart attaches its listener, so persist the minimal action here.
     */
    private fun captureSystemCallAction(intent: Intent?) {
        val type = when (intent?.action) {
            "com.hiennv.flutter_callkit_incoming.ACTION_CALL_ACCEPT" -> "accept"
            "com.hiennv.flutter_callkit_incoming.ACTION_CALL_DECLINE" -> "decline"
            "com.hiennv.flutter_callkit_incoming.ACTION_CALL_ENDED" -> "end"
            "com.hiennv.flutter_callkit_incoming.ACTION_CALL_TIMEOUT" -> "timeout"
            else -> return
        }
        val data = intent.getBundleExtra("EXTRA_CALLKIT_CALL_DATA") ?: return
        val systemCallId = data.getString("EXTRA_CALLKIT_ID")?.takeIf { it.isNotBlank() } ?: return
        @Suppress("DEPRECATION")
        val extra = data.getSerializable("EXTRA_CALLKIT_EXTRA") as? Map<*, *> ?: return
        val serverCallId = extra["serverCallId"]?.toString()?.takeIf { it.isNotBlank() } ?: return
        val action = JSONObject()
            .put("type", type)
            .put("serverCallId", serverCallId)
            .put("systemCallId", systemCallId)
        val preferences = getSharedPreferences(systemCallPreferencesName, MODE_PRIVATE)
        val pending = runCatching {
            JSONArray(preferences.getString(systemCallPendingKey, "[]"))
        }.getOrElse { JSONArray() }
        val key = "$type|$serverCallId|$systemCallId"
        for (index in 0 until pending.length()) {
            val current = pending.optJSONObject(index) ?: continue
            val currentKey = "${current.optString("type")}|${current.optString("serverCallId")}|${current.optString("systemCallId")}"
            if (currentKey == key) return
        }
        pending.put(action)
        val bounded = JSONArray()
        for (index in maxOf(0, pending.length() - 12) until pending.length()) {
            bounded.put(pending.get(index))
        }
        preferences.edit().putString(systemCallPendingKey, bounded.toString()).apply()
    }

    private fun drainSystemCallActions(): List<Map<String, Any?>> {
        val preferences = getSharedPreferences(systemCallPreferencesName, MODE_PRIVATE)
        val raw = preferences.getString(systemCallPendingKey, "[]") ?: "[]"
        preferences.edit().remove(systemCallPendingKey).commit()
        val pending = runCatching { JSONArray(raw) }.getOrElse { JSONArray() }
        return buildList {
            for (index in 0 until pending.length()) {
                val action = pending.optJSONObject(index) ?: continue
                val type = action.optString("type")
                val serverCallId = action.optString("serverCallId")
                val systemCallId = action.optString("systemCallId")
                if (type.isBlank() || serverCallId.isBlank() || systemCallId.isBlank()) continue
                add(
                    mapOf(
                        "type" to type,
                        "serverCallId" to serverCallId,
                        "systemCallId" to systemCallId,
                    ),
                )
            }
        }
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

    companion object {
        private const val systemCallPreferencesName = "linli_system_calls"
        private const val systemCallPendingKey = "pending_actions_v1"
    }
}
