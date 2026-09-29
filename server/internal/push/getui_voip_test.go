package push

import (
	"context"
	"encoding/json"
	"github.com/linli/im/server/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetuiVoIPOnlyVendorChannelAndStableRetry(t *testing.T) {
	var requests []map[string]any
	reject := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth") {
			writeGetui(w, 0, map[string]any{"token": "fixture-token", "expire_time": time.Now().Add(time.Hour).UnixMilli()})
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("body")
		}
		requests = append(requests, body)
		if reject {
			writeGetui(w, 10002, nil)
			return
		}
		writeGetui(w, 0, map[string]any{})
	}))
	defer server.Close()
	g := &Getui{AppID: "app", AppKey: "key", MasterSecret: "secret", BaseURL: server.URL, Client: server.Client(), VoIP: true}
	item := store.OutboxItem{ID: 81, EventType: "call.invited", Payload: map[string]any{"tenantId": "a", "localUserId": "u", "assignmentVersion": 1, "authVersion": 2, "realmVersion": 3, "callId": "call-1", "expiresAt": time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339Nano), "pushBindingId": "binding", "pushBindingRevision": 4}, Devices: []store.Device{{ID: "binding", Provider: "getui_voip", Platform: "ios", PushToken: "cid-not-apns-token"}}}
	for range 2 {
		if e := g.Send(t.Context(), item); e != nil {
			t.Fatal(e)
		}
	}
	if len(requests) != 2 || requests[0]["request_id"] != requests[1]["request_id"] {
		t.Fatal("unstable duplicate key")
	}
	b := requests[0]
	channels := b["push_channel"].(map[string]any)
	ios := channels["ios"].(map[string]any)
	if b["push_message"] != nil || len(channels) != 1 || ios["type"] != "voip" || ios["aps"] != nil || ios["auto_badge"] != nil {
		t.Fatal("VoIP used ordinary channel")
	}
	if b["settings"].(map[string]any)["strategy"].(map[string]any)["ios"] != float64(2) {
		t.Fatal("SDK fallback enabled")
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(ios["payload"].(string)), &payload)
	if payload["callId"] != "call-1" || payload["pushBindingId"] != "binding" || payload["authVersion"] != float64(2) {
		t.Fatal("identity lost")
	}
	reject = true
	if e := g.Send(t.Context(), item); e == nil {
		t.Fatal("provider rejection reported success")
	}
	before := len(requests)
	item.Payload["expiresAt"] = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	if e := g.Send(context.Background(), item); e == nil || len(requests) != before {
		t.Fatal("expired call sent")
	}
	item.EventType = "message.created"
	if e := g.Send(t.Context(), item); e == nil {
		t.Fatal("non-call accepted by VoIP")
	}
}
