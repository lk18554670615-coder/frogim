package top.hongjinghuanqiu.app

import org.junit.Assert.*
import org.junit.Test

class LinliTenantCallPolicyTest {
    private val now = LinliTenantCallPolicy.expiry("2026-09-29T00:00:00Z")!!
    private val scope = mapOf<String, Any>(
        "tenantId" to "tenant-a", "localUserId" to "user-a",
        "assignmentVersion" to 1, "authVersion" to 2, "realmVersion" to 3,
        "expiresAt" to "2099-01-01T00:00:00Z", "callSessionId" to "a".repeat(43),
    )
    private val binding = scope.filterKeys { it != "callSessionId" && it != "expiresAt" } + mapOf(
        "provider" to "getui", "pushBindingId" to "binding-1", "pushBindingRevision" to 1,
        "leaseExpiresAt" to "2099-01-01T00:00:00Z",
    )
    private val payload = binding + mapOf("expiresAt" to "2026-09-29T00:00:30Z")
    private val state = mapOf("scope" to scope, "bindings" to listOf(binding))

    @Test fun `valid bound push acquires current local login generation`() {
        val incoming = LinliTenantCallPolicy.incoming(state, payload, "getui", now)!!
        assertEquals("a".repeat(43), incoming.fields["callSessionId"])
        assertEquals("tenant-call-v1|tenant-a|user-a|1|2|3|${"a".repeat(43)}|call-1", incoming.seed("call-1"))
        assertTrue(LinliTenantCallPolicy.action(state, mapOf("tenantScope" to incoming.fields), now))
    }

    @Test fun `every identity and binding field is mandatory and exact`() {
        val changes = mapOf<String, Any>(
            "tenantId" to "tenant-b", "localUserId" to "user-b", "assignmentVersion" to 2,
            "authVersion" to 3, "realmVersion" to 4, "pushBindingId" to "binding-2", "pushBindingRevision" to 2,
        )
        changes.forEach { (key, value) ->
            assertNull(key, LinliTenantCallPolicy.incoming(state, payload + (key to value), "getui", now))
            assertNull("missing $key", LinliTenantCallPolicy.incoming(state, payload - key, "getui", now))
        }
        assertNull(LinliTenantCallPolicy.incoming(state, payload, "apns_voip", now))
        assertNull(LinliTenantCallPolicy.incoming(null, payload, "getui", now))
    }

    @Test fun `payload binding and local session all expire at exact boundary`() {
        val deadline = "2026-09-29T00:00:00Z"
        assertNull(LinliTenantCallPolicy.incoming(state, payload + ("expiresAt" to deadline), "getui", now))
        assertNull(LinliTenantCallPolicy.incoming(state + ("scope" to (scope + ("expiresAt" to deadline))), payload, "getui", now))
        assertNull(LinliTenantCallPolicy.incoming(state + ("bindings" to listOf(binding + ("leaseExpiresAt" to deadline))), payload, "getui", now))
        assertNotNull(LinliTenantCallPolicy.incoming(state, payload + ("expiresAt" to deadline), "getui", now - 1))
    }

    @Test fun `old same-account action cannot act in a new login`() {
        assertFalse(LinliTenantCallPolicy.action(state,
            mapOf("tenantScope" to (scope + ("callSessionId" to "b".repeat(43)))), now))
        assertFalse(LinliTenantCallPolicy.action(state, mapOf("tenantScope" to (scope - "callSessionId")), now))
        assertFalse(LinliTenantCallPolicy.action(state, emptyMap<String, Any>(), now))
        assertFalse(LinliTenantCallPolicy.action(mapOf("scope" to null), mapOf("tenantScope" to scope), now))
    }

    @Test fun `malformed timestamps and versions fail closed`() {
        listOf("2099-02-30T00:00:00Z", "2099-01-01", "2099-01-01T00:00:00", "2099-01-01T25:00:00Z",
            "2099-01-01T00:00:00+25:00").forEach { assertNull(it, LinliTenantCallPolicy.expiry(it)) }
        listOf(0, -1, 1.5, "1", true, 9007199254740992L).forEach { assertNull(LinliTenantCallPolicy.version(it)) }
        assertEquals(now, LinliTenantCallPolicy.expiry("2026-09-29T08:00:00+08:00"))
        assertEquals(now + 123, LinliTenantCallPolicy.expiry("2026-09-29T00:00:00.123456789Z"))
    }

    @Test fun `native call id differs between tenant and local generations`() {
        val original = LinliTenantCallPolicy.scope(scope)!!
        val relogin = LinliTenantCallPolicy.scope(scope + ("callSessionId" to "b".repeat(43)))!!
        val tenant = LinliTenantCallPolicy.scope(scope + ("tenantId" to "tenant-b"))!!
        val refreshed = LinliTenantCallPolicy.scope(scope + ("expiresAt" to "2099-02-01T00:00:00Z"))!!
        assertNotEquals(LinliCallId.deterministicUuid(original.seed("call-1")), LinliCallId.deterministicUuid(relogin.seed("call-1")))
        assertNotEquals(LinliCallId.deterministicUuid(original.seed("call-1")), LinliCallId.deterministicUuid(tenant.seed("call-1")))
        assertEquals(LinliCallId.deterministicUuid(original.seed("call-1")), LinliCallId.deterministicUuid(refreshed.seed("call-1")))
    }
}
