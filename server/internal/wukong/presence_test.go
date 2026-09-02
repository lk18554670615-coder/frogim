package wukong

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOnlineUsersPinnedContract(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		failure    bool
		online     bool
	}{
		{"multiple devices", `[{"uid":"a","device_flag":0,"online":1},{"uid":"a","device_flag":1,"online":1}]`, false, true},
		{"one device offline", `[{"uid":"a","device_flag":0,"online":1},{"uid":"a","device_flag":1,"online":0}]`, false, true},
		{"none online", `[]`, false, false},
		{"null is not success", `null`, true, false},
		{"error envelope", `{"status":500}`, true, false},
		{"missing flag", `[{"uid":"a"}]`, true, false},
		{"malformed", `[`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/user/onlinestatus" || r.Header.Get("token") != "presence-test" {
					t.Errorf("wrong request %s %s", r.Method, r.URL.Path)
				}
				var ids []string
				if json.NewDecoder(r.Body).Decode(&ids) != nil || len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
					t.Errorf("raw UID array: %v", ids)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			c, err := NewClient(Config{APIURL: ts.URL, ManagerURL: ts.URL, ManagerToken: "presence-test"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := c.OnlineUsers(t.Context(), []string{"a", "b"})
			if (err != nil) != tc.failure {
				t.Fatalf("err=%v", err)
			}
			if !tc.failure && (result["a"] != tc.online || result["b"] || len(result) != 2) {
				t.Fatalf("result=%v", result)
			}
		})
	}
}

func TestPresenceCacheExpiryFailureAndCoalescing(t *testing.T) {
	now := time.Now()
	var calls atomic.Int32
	gate := make(chan struct{})
	started := make(chan struct{})
	cache := NewPresenceCache(func(ctx context.Context, ids []string) (map[string]bool, error) {
		n := calls.Add(1)
		if n == 1 {
			close(started)
			<-gate
		}
		if n == 2 {
			return nil, errors.New("unavailable")
		}
		return map[string]bool{"a": true}, nil
	})
	cache.now = func() time.Time { return now }
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := cache.Query(t.Context(), []string{"a"})["a"].Status; got != "online" {
				t.Errorf("status=%s", got)
			}
		}()
	}
	<-started
	close(gate)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("duplicate calls=%d", calls.Load())
	}
	now = now.Add(5 * time.Second)
	if got := cache.Query(t.Context(), []string{"a"})["a"].Status; got != "unknown" {
		t.Fatal(got)
	}
	if got := cache.Query(t.Context(), []string{"a"})["a"].Status; got != "online" {
		t.Fatal(got)
	}
	if calls.Load() != 3 {
		t.Fatal(calls.Load())
	}
}

func TestPresenceCacheBoundedConcurrencyAndCancellation(t *testing.T) {
	var running, maxRunning atomic.Int32
	cache := NewPresenceCache(func(ctx context.Context, ids []string) (map[string]bool, error) {
		n := running.Add(1)
		defer running.Add(-1)
		for old := maxRunning.Load(); n > old; old = maxRunning.Load() {
			if maxRunning.CompareAndSwap(old, n) {
				break
			}
		}
		if len(ids) > 200 {
			t.Error("oversized batch")
		}
		time.Sleep(5 * time.Millisecond)
		out := map[string]bool{}
		for _, id := range ids {
			out[id] = false
		}
		return out, nil
	})
	ids := make([]string, 601)
	for i := range ids {
		ids[i] = string(rune(i + 1000))
	}
	result := cache.Query(t.Context(), ids)
	if len(result) != 601 || maxRunning.Load() > 2 {
		t.Fatalf("result=%d concurrency=%d", len(result), maxRunning.Load())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := cache.Query(ctx, []string{"cancelled"})["cancelled"].Status; got != "unknown" {
		t.Fatal(got)
	}
	// Let the detached, bounded flight finish before the test exits.
	cache.Query(t.Context(), []string{"cancelled"})
}
