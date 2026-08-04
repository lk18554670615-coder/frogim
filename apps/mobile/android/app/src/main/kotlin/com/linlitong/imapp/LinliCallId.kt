package com.linlitong.imapp

import java.nio.charset.StandardCharsets
import java.security.MessageDigest
import java.util.UUID

/**
 * 将服务端 callId 稳定映射为 Android Telecom 要求的 UUID。
 *
 * 相同 callId 在原生推送入口和恢复后的 FlutterEngine 中会得到同一标识，避免重复展示来电。
 */
internal object LinliCallId {
    fun deterministicUuid(value: String): UUID {
        val bytes = MessageDigest.getInstance("SHA-256")
            .digest(value.toByteArray(StandardCharsets.UTF_8))
            .copyOf(16)
        bytes[6] = ((bytes[6].toInt() and 0x0f) or 0x50).toByte()
        bytes[8] = ((bytes[8].toInt() and 0x3f) or 0x80).toByte()
        var most = 0L
        var least = 0L
        for (index in 0 until 8) {
            most = (most shl 8) or (bytes[index].toLong() and 0xff)
        }
        for (index in 8 until 16) {
            least = (least shl 8) or (bytes[index].toLong() and 0xff)
        }
        return UUID(most, least)
    }
}
