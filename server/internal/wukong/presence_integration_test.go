package wukong

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// Run once against the isolated running container (offline), then once after
// stopping that same container (unknown). Exercises the production HTTP client
// and cache, not a mocked availability flag.
func TestPresenceRealServerAvailability(t *testing.T) {
	api := os.Getenv("IM_TEST_WUKONG_PATCH_URL")
	if api == "" {
		t.Skip("isolated WuKongIM required")
	}
	expected := os.Getenv("IM_TEST_PRESENCE_EXPECT_STATUS")
	if expected == "" {
		expected = "offline"
	}
	if expected != "offline" && expected != "unknown" {
		t.Fatal("expected status must be offline or unknown")
	}
	client, err := NewClient(Config{APIURL: api, ManagerURL: api, ManagerToken: os.Getenv("IM_TEST_WUKONG_MANAGER_TOKEN"), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("presence_availability_%d", time.Now().UnixNano())
	cache := NewPresenceCache(client.OnlineUsers)
	got := cache.Query(t.Context(), []string{id})[id]
	if got.Status != expected {
		t.Fatalf("real server status=%s want=%s", got.Status, expected)
	}
	t.Logf("production client/cache result: %s", got.Status)
}
