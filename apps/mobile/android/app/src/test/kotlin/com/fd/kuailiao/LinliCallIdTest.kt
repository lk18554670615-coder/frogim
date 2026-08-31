package com.fd.kuailiao

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Test

class LinliCallIdTest {
    @Test
    fun `same server call id maps to same version 5 uuid`() {
        val first = LinliCallId.deterministicUuid("call-20260801-0001")
        val second = LinliCallId.deterministicUuid("call-20260801-0001")

        assertEquals(first, second)
        assertEquals(5, first.version())
        assertEquals(2, first.variant())
    }

    @Test
    fun `different server call ids do not collide`() {
        val first = LinliCallId.deterministicUuid("call-20260801-0001")
        val second = LinliCallId.deterministicUuid("call-20260801-0002")

        assertNotEquals(first, second)
    }
}
