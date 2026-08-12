package com.linlitong.imapp

import android.content.Context
import android.content.Intent
import android.os.Bundle
import com.getui.getuiflut.FlutterIntentService
import com.igexin.sdk.message.GTTransmitMessage
import org.json.JSONObject
import java.nio.charset.StandardCharsets

/**
 * 个推透传来电的原生兜底入口。
 *
 * 这里只展示系统来电界面，不接触登录凭据、不接受通话，也不建立媒体连接；用户动作会由
 * flutter_callkit_incoming 持久化并在主 FlutterEngine 恢复后交给 CallController 鉴权处理。
 */
class LinliCallIntentService : FlutterIntentService() {
    override fun onReceiveMessageData(context: Context, message: GTTransmitMessage) {
        val raw = String(message.payload ?: byteArrayOf(), StandardCharsets.UTF_8)
        runCatching { JSONObject(raw) }
            .getOrNull()
            ?.takeIf { payload ->
                val type = payload.optString("type", payload.optString("eventType"))
                type == "call.invited" || type == "call.invite"
            }
            ?.let { payload -> showNativeIncomingCall(context, payload) }

        // 进程存活时仍把同一载荷交给 Flutter，CallController 自身会按 callId 幂等。
        super.onReceiveMessageData(context, message)
    }

    private fun showNativeIncomingCall(context: Context, payload: JSONObject) {
        val serverCallId = payload.optString("callId")
        if (serverCallId.isBlank()) return
        val conversationId = payload.optString("conversationId")
        val mediaType = payload.optString("mediaType", "audio")
        val systemCallId = LinliCallId.deterministicUuid(serverCallId).toString()
        val extra = hashMapOf<String, Any?>(
            "serverCallId" to serverCallId,
            "conversationId" to conversationId,
            "mediaType" to mediaType,
        )
        // flutter_callkit_incoming 3.1.3 publishes this Bundle/broadcast
        // protocol in its Android manifest. Avoid importing plugin-internal
        // Kotlin classes, which AGP 9 does not expose to the app module.
        val data = Bundle().apply {
            putString("EXTRA_CALLKIT_ID", systemCallId)
            putString("EXTRA_CALLKIT_NAME_CALLER", "邻里联系人")
            putString("EXTRA_CALLKIT_APP_NAME", "邻里通讯")
            putString("EXTRA_CALLKIT_HANDLE", "邻里通讯")
            putInt("EXTRA_CALLKIT_TYPE", if (mediaType == "video") 1 else 0)
            putLong("EXTRA_CALLKIT_DURATION", 30_000L)
            putString("EXTRA_CALLKIT_TEXT_ACCEPT", "接听")
            putString("EXTRA_CALLKIT_TEXT_DECLINE", "拒绝")
            putSerializable("EXTRA_CALLKIT_EXTRA", extra)
            putBoolean("EXTRA_CALLKIT_IS_CUSTOM_NOTIFICATION", true)
            putBoolean("EXTRA_CALLKIT_IS_SHOW_LOGO", false)
            putBoolean("EXTRA_CALLKIT_IS_SHOW_CALL_ID", false)
            putString("EXTRA_CALLKIT_RINGTONE_PATH", "system_ringtone_default")
            putString("EXTRA_CALLKIT_BACKGROUND_COLOR", "#07101F")
            putString("EXTRA_CALLKIT_ACTION_COLOR", "#34C759")
            putString("EXTRA_CALLKIT_TEXT_COLOR", "#FFFFFF")
            putString("EXTRA_CALLKIT_INCOMING_CALL_NOTIFICATION_CHANNEL_NAME", "音视频来电")
            putString("EXTRA_CALLKIT_MISSED_CALL_NOTIFICATION_CHANNEL_NAME", "未接来电")
            putBoolean("EXTRA_CALLKIT_IS_SHOW_FULL_LOCKED_SCREEN", true)
            putBoolean("EXTRA_CALLKIT_IS_IMPORTANT", true)
            putBoolean("EXTRA_CALLKIT_IS_FULL_SCREEN", true)
        }
        val action = "com.hiennv.flutter_callkit_incoming.ACTION_CALL_INCOMING"
        val intent = Intent().apply {
            setClassName(
                context.packageName,
                "com.hiennv.flutter_callkit_incoming.CallkitIncomingBroadcastReceiver",
            )
            this.action = "${context.packageName}.$action"
            putExtra("EXTRA_CALLKIT_INCOMING_DATA", data)
            `package` = context.packageName
        }
        context.sendBroadcast(intent)
    }

}
