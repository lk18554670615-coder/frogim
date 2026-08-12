package wukong

import (
	"context"
	"sync"
	"testing"
	"time"

	hookpb "github.com/linli/im/server/internal/wukong/hookpb"
)

type memoryWebhookStore struct {
	mu     sync.Mutex
	events map[string]WebhookEvent
}

func newMemoryWebhookStore() *memoryWebhookStore {
	return &memoryWebhookStore{events: map[string]WebhookEvent{}}
}

func (s *memoryWebhookStore) PutWukongWebhookEvent(_ context.Context, event WebhookEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.events[event.ID]; exists {
		return false, nil
	}
	s.events[event.ID] = event
	return true, nil
}

func (s *memoryWebhookStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func TestWebhookHandlerDeduplicatesRetry(t *testing.T) {
	store := newMemoryWebhookStore()
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
	store := newMemoryWebhookStore()
	handler, _ := NewWebhookHandler(store)
	response, err := handler.SendWebhook(context.Background(), &hookpb.EventReq{Event: "unknown", Data: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != hookpb.EventStatus_Error || store.Count() != 0 {
		t.Fatalf("unexpected response %#v", response)
	}
}
