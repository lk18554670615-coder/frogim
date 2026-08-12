package wukong

import (
	"context"
	"testing"
	"time"

	hookpb "github.com/linli/im/server/internal/wukong/hookpb"
)

func TestWebhookHandlerDeduplicatesRetry(t *testing.T) {
	store := NewMemoryWebhookStore()
	handler, err := NewWebhookHandler(store)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return time.Unix(123, 0) }
	request := &hookpb.EventReq{Event: EventMessageNotify, Data: []byte(`[{"message_id":1,"client_msg_no":"c1"},{"message_id":2,"client_msg_no":"c2"}]`)}
	for range 2 {
		response, callErr := handler.SendWebhook(context.Background(), request)
		if callErr != nil {
			t.Fatal(callErr)
		}
		if response.Status != hookpb.EventStatus_Success {
			t.Fatalf("status=%s data=%s", response.Status, response.Data)
		}
	}
	if got := store.Count(); got != 2 {
		t.Fatalf("stored events=%d, want 2", got)
	}
}

func TestWebhookRejectsUnknownEvent(t *testing.T) {
	store := NewMemoryWebhookStore()
	handler, _ := NewWebhookHandler(store)
	response, err := handler.SendWebhook(context.Background(), &hookpb.EventReq{Event: "unknown", Data: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != hookpb.EventStatus_Error || store.Count() != 0 {
		t.Fatalf("unexpected response %#v", response)
	}
}
