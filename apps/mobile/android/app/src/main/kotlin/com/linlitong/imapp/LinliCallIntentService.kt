package com.linlitong.imapp

import android.content.Context
import com.getui.getuiflut.FlutterIntentService
import com.hiennv.flutter_callkit_incoming.CallkitIncomingBroadcastReceiver
import com.hiennv.flutter_callkit_incoming.Data
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
        val android = hashMapOf<String, Any?>(
            "isCustomNotification" to true,
            "isShowLogo" to false,
            "isShowCallID" to false,
            "ringtonePath" to "system_ringtone_default",
            "backgroundColor" to "#07101F",
            "actionColor" to "#34C759",
            "textColor" to "#FFFFFF",
            "incomingCallNotificationChannelName" to "音视频来电",
            "missedCallNotificationChannelName" to "未接来电",
            "isShowFullLockedScreen" to true,
            "isImportant" to true,
            "isFullScreen" to true,
            "textAccept" to "接听",
            "textDecline" to "拒绝",
        )
        val data = Data(
            hashMapOf(
                "id" to systemCallId,
                "nameCaller" to "邻里联系人",
                "appName" to "邻里通讯",
                "handle" to "邻里通讯",
                "type" to if (mediaType == "video") 1 else 0,
                "duration" to 30_000L,
                "extra" to extra,
                "android" to android,
            ),
        )
        context.sendBroadcast(CallkitIncomingBroadcastReceiver.getIntentIncoming(context, data.toBundle()))
    }

}
