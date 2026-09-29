package top.hongjinghuanqiu.app

import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Bundle
import org.json.JSONArray
import org.json.JSONObject
import java.util.UUID

/** App-private, atomic, credential-free state; process-wide serialization also
 * prevents a late FlutterEngine callback replacing a newer engine's identity. */
internal object LinliTenantCalls {
    private const val preferences = "linli_tenant_calls_v1"
    private const val stateKey = "state"
    private const val callsKey = "native_call_ids"
    private var control: String? = null
    private var sequence = 0L

    fun managed(context: Context): Boolean = runCatching {
        @Suppress("DEPRECATION")
        val info = context.packageManager.getApplicationInfo(context.packageName, PackageManager.GET_META_DATA)
        info.metaData?.getBoolean("top.hongjinghuanqiu.app.TENANT_AUTH_ENABLED", false) == true
    }.getOrDefault(true) // Missing package information cannot enable a legacy bypass.

    @Synchronized fun begin(): String {
        sequence = 0
        return UUID.randomUUID().toString().also { control = it }
    }
    @Synchronized fun update(context: Context, input: Map<*, *>): Boolean {
        if (!managed(context) || input["control"] != control || control == null) return false
        val next = LinliTenantCallPolicy.version(input["sequence"]) ?: return false
        if (next <= sequence) return false
        val scope = input["scope"]
        val bindings = input["bindings"] as? List<*> ?: return false
        if (bindings.size > 2 || (scope == null && bindings.isNotEmpty())) return false
        val parsed = LinliTenantCallPolicy.scope(scope as? Map<*, *>)
        if (scope != null && parsed == null) return false
        for (entry in bindings) {
            val item = entry as? Map<*, *> ?: return false
            val provider = item["provider"] as? String ?: return false
            if (provider != "getui" && provider != "apns_voip") return false
            if (parsed == null || LinliTenantCallPolicy.incoming(
                mapOf("scope" to parsed.fields, "bindings" to listOf(item)),
                item + mapOf("expiresAt" to item["leaseExpiresAt"]), provider,
                System.currentTimeMillis(),
            ) == null) return false
        }
        val previous = LinliTenantCallPolicy.scope(read(context)?.get("scope") as? Map<*, *>)
        val changed = parsed == null || previous == null || !parsed.sameSession(previous)
        val prefs = context.getSharedPreferences(preferences, Context.MODE_PRIVATE)
        val oldCalls = if (changed) prefs.getStringSet(callsKey, emptySet())!!.toSet() else emptySet()
        val state = JSONObject(mapOf("scope" to parsed?.fields, "bindings" to bindings)).toString()
        val edit = prefs.edit().putString(stateKey, state)
        if (changed) edit.remove(callsKey)
        if (!edit.commit()) return false
        // IDs refer only to calls shown by this native fallback, never a new
        // account's call with the same business ID. Broadcasts are serialized
        // with publishIncoming so logout cannot be overtaken by a stale push.
        oldCalls.forEach { endNativeCall(context, it) }
        sequence = next
        return true
    }
    @Synchronized fun publishIncoming(
        context: Context, payload: JSONObject, callId: String,
        publish: (LinliTenantCallPolicy.Scope) -> Unit,
    ) {
        val scope = LinliTenantCallPolicy.incoming(read(context), map(payload), "getui", System.currentTimeMillis()) ?: return
        val nativeId = LinliCallId.deterministicUuid(scope.seed(callId)).toString()
        val prefs = context.getSharedPreferences(preferences, Context.MODE_PRIVATE)
        val ids = prefs.getStringSet(callsKey, emptySet())!!.toMutableSet()
        // A bounded local cleanup registry, not call history. End obsolete UI
        // before admitting more entries rather than silently dropping IDs.
        if (ids.size >= 32 && !ids.contains(nativeId)) {
            ids.forEach { endNativeCall(context, it) }
            ids.clear()
        }
        ids.add(nativeId)
        if (!prefs.edit().putStringSet(callsKey, ids).commit()) return
        publish(scope)
    }

    private fun endNativeCall(context: Context, id: String) {
        val data = Bundle().apply { putString("EXTRA_CALLKIT_ID", id) }
        context.sendBroadcast(Intent().apply {
            setClassName(context.packageName, "com.hiennv.flutter_callkit_incoming.CallkitIncomingBroadcastReceiver")
            action = "${context.packageName}.com.hiennv.flutter_callkit_incoming.ACTION_CALL_ENDED"
            putExtra("EXTRA_CALLKIT_INCOMING_DATA", data)
            `package` = context.packageName
        })
    }

    @Synchronized fun acceptsAction(context: Context, extra: Map<*, *>): Boolean =
        LinliTenantCallPolicy.action(read(context), extra, System.currentTimeMillis())

    private fun read(context: Context): Map<String, Any?>? = runCatching {
        val raw = context.getSharedPreferences(preferences, Context.MODE_PRIVATE).getString(stateKey, null)
            ?: return null
        map(JSONObject(raw))
    }.getOrNull()

    fun map(json: JSONObject): Map<String, Any?> = json.keys().asSequence().associateWith { key -> value(json.opt(key)) }
    private fun value(raw: Any?): Any? = when (raw) {
        is JSONObject -> map(raw)
        is JSONArray -> (0 until raw.length()).map { value(raw.opt(it)) }
        JSONObject.NULL -> null
        else -> raw
    }
}
