package push

import (
	"encoding/json"
	"github.com/linli/im/server/internal/store"
	"testing"
	"time"
)

func TestTenantNotificationRoutingScope(t *testing.T) {
	item := store.OutboxItem{EventType: "call.invited", Payload: map[string]any{"tenantId": "tenant", "localUserId": "local", "assignmentVersion": int64(7), "callId": "call", "conversationId": "conversation", "mediaType": "audio", "pushToken": "never-copy", "accountId": "never-copy"}}
	item.Payload["authVersion"], item.Payload["realmVersion"], item.Payload["expiresAt"] = int64(3), int64(4), time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	_, _, navigation := getuiNotification(item)
	raw, e := apnsVoIPPayload(item)
	if e != nil {
		t.Fatal(e)
	}
	var voip map[string]any
	if e = json.Unmarshal(raw, &voip); e != nil {
		t.Fatal(e)
	}
	for _, p := range []map[string]any{navigation, voip} {
		if p["tenantId"] != "tenant" || p["localUserId"] != "local" || p["assignmentVersion"] == nil || p["authVersion"] == nil || p["realmVersion"] == nil || p["expiresAt"] != item.Payload["expiresAt"] || p["pushToken"] != nil || p["accountId"] != nil {
			t.Fatal("invalid scoped payload")
		}
	}
	for _, version := range []any{float64(1.5), float64(-1), "7", nil} {
		item.Payload["assignmentVersion"] = version
		_, _, p := getuiNotification(item)
		if p["tenantId"] != nil || p["localUserId"] != nil {
			t.Fatal("partial scope accepted")
		}
	}
	item.Payload["assignmentVersion"] = int64(7)
	for _, field := range []string{"authVersion", "realmVersion", "expiresAt"} {
		value := item.Payload[field]
		delete(item.Payload, field)
		_, _, p := getuiNotification(item)
		if p["tenantId"] != nil {
			t.Fatalf("partial scope accepted without %s", field)
		}
		item.Payload[field] = value
	}
}
