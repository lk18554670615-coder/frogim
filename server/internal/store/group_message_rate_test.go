package store

import "testing"

func TestGroupMessageRateStatusBoundsAndRoundsRetry(t *testing.T) {
	status := groupMessageRateStatus(5, 5, 1001)
	if status.LimitPerMinute != 5 || status.Used != 5 || status.Remaining != 0 || status.RetryAfterSeconds != 2 {
		t.Fatalf("unexpected rate status: %#v", status)
	}
	status = groupMessageRateStatus(5, 8, 0)
	if status.Remaining != 0 {
		t.Fatalf("remaining count must never become negative: %#v", status)
	}
}

func TestGroupMessageRateKeyIsScopedByGroupMemberAndVersion(t *testing.T) {
	base := groupMessageRateRedisKey("group-a", "member-a", 1)
	for name, candidate := range map[string]string{
		"group":   groupMessageRateRedisKey("group-b", "member-a", 1),
		"member":  groupMessageRateRedisKey("group-a", "member-b", 1),
		"version": groupMessageRateRedisKey("group-a", "member-a", 2),
	} {
		if candidate == base {
			t.Fatalf("%s must use an independent rate window", name)
		}
	}
	if groupMessageRateRedisKey("group-a", "member-a", 1) != base {
		t.Fatal("rate key must be stable for the same policy scope")
	}
}
