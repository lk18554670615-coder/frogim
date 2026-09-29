package top.hongjinghuanqiu.app

import java.text.ParsePosition
import java.text.SimpleDateFormat
import java.util.Locale

/** Pure policy: shared by terminated-process push and Activity action handling. */
internal object LinliTenantCallPolicy {
    private val id = Regex("^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$")
    private val generation = Regex("^[A-Za-z0-9_-]{43}$")
    private val iso = Regex("^(\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2})(?:\\.(\\d{1,9}))?(Z|[+-]\\d{2}:\\d{2})$")
    private val identityKeys = listOf("tenantId", "localUserId", "assignmentVersion", "authVersion", "realmVersion")
    data class Scope(val fields: Map<String, Any>, val expires: Long) {
        fun sameIdentity(other: Scope) = identityKeys.all { fields[it] == other.fields[it] }
        fun sameSession(other: Scope) = sameIdentity(other) && fields["callSessionId"] != null &&
            fields["callSessionId"] == other.fields["callSessionId"]
        fun seed(callId: String) = "tenant-call-v1|${fields["tenantId"]}|${fields["localUserId"]}|" +
            "${fields["assignmentVersion"]}|${fields["authVersion"]}|${fields["realmVersion"]}|${fields["callSessionId"]}|$callId"
    }
    fun version(value: Any?): Long? {
        if (value !is Number) return null
        val number = value.toDouble()
        val integer = value.toLong()
        return integer.takeIf { number.isFinite() && it in 1..9007199254740991L && it.toDouble() == number }
    }
    fun expiry(value: Any?): Long? {
        val match = iso.matchEntire(value as? String ?: return null) ?: return null
        val normalized = match.groupValues[1] + "." + match.groupValues[2].padEnd(3, '0').take(3) + match.groupValues[3]
        val format = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSSXXX", Locale.ROOT).apply { isLenient = false }
        val position = ParsePosition(0)
        val date = format.parse(normalized, position) ?: return null
        return date.time.takeIf { position.index == normalized.length }
    }
    fun scope(input: Map<*, *>?, requireSession: Boolean = true): Scope? {
        if (input == null) return null
        val tenant = input["tenantId"] as? String ?: return null
        val user = input["localUserId"] as? String ?: return null
        if (!id.matches(tenant) || !id.matches(user)) return null
        val fields = linkedMapOf<String, Any>("tenantId" to tenant, "localUserId" to user)
        for (key in identityKeys.drop(2)) fields[key] = version(input[key]) ?: return null
        val expires = expiry(input["expiresAt"]) ?: return null
        fields["expiresAt"] = input["expiresAt"] as String
        val session = input["callSessionId"]
        if (session != null && (session !is String || !generation.matches(session))) return null
        if (requireSession && session == null) return null
        if (session != null) fields["callSessionId"] = session
        return Scope(fields, expires)
    }
    fun incoming(state: Map<*, *>?, payload: Map<*, *>, provider: String, now: Long): Scope? {
        val current = scope(state?.get("scope") as? Map<*, *>) ?: return null
        val pushed = scope(payload, requireSession = false) ?: return null
        if (current.expires <= now || pushed.expires <= now || !current.sameIdentity(pushed)) return null
        val bindingId = payload["pushBindingId"] as? String ?: return null
        if (!id.matches(bindingId)) return null
        val revision = version(payload["pushBindingRevision"]) ?: return null
        val bindings = state?.get("bindings") as? List<*> ?: return null
        val valid = bindings.any { item ->
            val binding = item as? Map<*, *> ?: return@any false
            binding["provider"] == provider && binding["pushBindingId"] == bindingId &&
                version(binding["pushBindingRevision"]) == revision &&
                (expiry(binding["leaseExpiresAt"]) ?: 0) > now &&
                scope(binding + mapOf("expiresAt" to binding["leaseExpiresAt"]), false)?.sameIdentity(current) == true
        }
        return if (valid) current else null
    }
    fun action(state: Map<*, *>?, extra: Map<*, *>, now: Long): Boolean {
        val current = scope(state?.get("scope") as? Map<*, *>) ?: return false
        val action = scope(extra["tenantScope"] as? Map<*, *>) ?: return false
        return current.expires > now && action.expires > now && current.sameSession(action)
    }
}
