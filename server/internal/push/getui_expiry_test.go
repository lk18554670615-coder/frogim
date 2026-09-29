package push

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linli/im/server/internal/store"
)

func TestGetuiTenantTTLBounds(t *testing.T) {
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name    string
		payload map[string]any
		want    int64
	}{
		{"legacy", nil, (72 * time.Hour).Milliseconds()},
		{"call", map[string]any{"tenantId": "a", "expiresAt": now.Add(45 * time.Second).Format(time.RFC3339Nano)}, 45000},
		{"message-cap", map[string]any{"tenantId": "a", "expiresAt": now.Add(48 * time.Hour).Format(time.RFC3339Nano)}, 86400000},
		{"last-millisecond", map[string]any{"tenantId": "a", "expiresAt": now.Add(time.Millisecond).Format(time.RFC3339Nano)}, 1},
		{"sub-millisecond", map[string]any{"tenantId": "a", "expiresAt": now.Add(time.Microsecond).Format(time.RFC3339Nano)}, 0},
		{"expired", map[string]any{"tenantId": "a", "expiresAt": now.Format(time.RFC3339Nano)}, 0},
		{"missing", map[string]any{"tenantId": "a"}, 0},
		{"malformed", map[string]any{"tenantId": "a", "expiresAt": "not-a-date"}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, e := getuiTTL(tt.payload, now)
			if got != tt.want || (e != nil) != (tt.want == 0) {
				t.Fatal(got, e)
			}
		})
	}
	p := map[string]any{"tenantId": "a", "expiresAt": now.Add(time.Minute).Format(time.RFC3339Nano)}
	if got, e := getuiTTL(p, now.Add(13*time.Second)); got != 47000 || e != nil {
		t.Fatal(got, e)
	}
}

func TestGetuiRefreshCannotExtendNotificationTTL(t *testing.T) {
	var mu sync.Mutex
	var auth int
	var ttls []int64
	var ids []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/auth") {
			auth++
			if auth == 2 {
				time.Sleep(20 * time.Millisecond)
			}
			writeGetui(w, 0, map[string]any{"token": "synthetic", "expire_time": time.Now().Add(time.Hour).UnixMilli()})
			return
		}
		var body struct {
			RequestID string `json:"request_id"`
			Settings  struct {
				TTL int64 `json:"ttl"`
			} `json:"settings"`
		}
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error("invalid request")
			w.WriteHeader(400)
			return
		}
		ttls = append(ttls, body.Settings.TTL)
		ids = append(ids, body.RequestID)
		if len(ttls) == 1 {
			writeGetui(w, 10001, nil)
		} else {
			writeGetui(w, 0, nil)
		}
	}))
	defer server.Close()
	provider := &Getui{AppID: "test", AppKey: "synthetic", MasterSecret: "synthetic", BaseURL: server.URL, Client: server.Client()}
	item := store.OutboxItem{ID: 2, EventType: "message.created", Payload: map[string]any{"tenantId": "a", "expiresAt": time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}, Devices: []store.Device{{ID: "binding", Provider: "getui", PushToken: "synthetic"}}}
	if e := provider.Send(t.Context(), item); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if auth != 2 || len(ttls) != 2 || ttls[0] > 60000 || ttls[1] < 1 || ttls[1] >= ttls[0] || ids[0] != ids[1] {
		t.Fatal("retry extended TTL or changed id", auth, ttls, ids)
	}
}

func TestGetuiExpiredTenantNotificationMakesNoRequest(t *testing.T) {
	provider := &Getui{AppID: "test", AppKey: "synthetic", MasterSecret: "synthetic", Client: &http.Client{Transport: rejectPushTransport{t: t}}}
	item := store.OutboxItem{EventType: "call.invited", Payload: map[string]any{"tenantId": "a", "expiresAt": time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)}, Devices: []store.Device{{ID: "binding", Provider: "getui", PushToken: "synthetic"}}}
	if e := provider.Send(t.Context(), item); e == nil {
		t.Fatal("expired notification accepted")
	}
}

type rejectPushTransport struct{ t *testing.T }

func (r rejectPushTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.t.Fatal("unexpected provider request")
	return nil, nil
}
