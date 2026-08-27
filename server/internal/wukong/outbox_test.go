package wukong

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type outboxTestStore struct {
	mu        sync.Mutex
	items     []OutboxItem
	completed []int64
	failed    []int64
}

func (s *outboxTestStore) ClaimWukongOutbox(context.Context, int) ([]OutboxItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.items
	s.items = nil
	return items, nil
}
func (s *outboxTestStore) CompleteWukongOutbox(_ context.Context, id int64) error {
	s.completed = append(s.completed, id)
	return nil
}
func (s *outboxTestStore) FailWukongOutbox(_ context.Context, id int64, _ string, _ bool) error {
	s.failed = append(s.failed, id)
	return nil
}

func TestOutboxWorkerMirrorsFriendAllowlist(t *testing.T) {
	var mu sync.Mutex
	requests := []AccessListRequest{}
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request AccessListRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		requests = append(requests, request)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(friendOperationPayload{UserID: "usr_a", FriendID: "usr_b"})
	store := &outboxTestStore{items: []OutboxItem{{ID: 7, Operation: OperationFriendAllow, Payload: payload}}}
	worker, err := NewOutboxWorker(store, client)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.runOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(store.completed) != 1 || len(paths) != 2 || paths[0] != "/channel/whitelist_add" || paths[1] != "/channel/whitelist_add" {
		t.Fatalf("completed=%v paths=%v", store.completed, paths)
	}
	if requests[0].ChannelID != "usr_a" || requests[0].UIDs[0] != "usr_b" || requests[1].ChannelID != "usr_b" || requests[1].UIDs[0] != "usr_a" {
		t.Fatalf("requests=%#v", requests)
	}
}

func TestOutboxWorkerRefreshesSystemUIDCache(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	add, _ := json.Marshal(systemUIDOperationPayload{UIDs: []string{"usr_notice"}})
	remove, _ := json.Marshal(systemUIDOperationPayload{UIDs: []string{"usr_notice"}})
	store := &outboxTestStore{items: []OutboxItem{
		{ID: 12, Operation: OperationSystemUIDAdd, Payload: add},
		{ID: 13, Operation: OperationSystemUIDRemove, Payload: remove},
	}}
	worker, _ := NewOutboxWorker(store, client)
	if err = worker.runOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(store.completed) != 2 || len(store.failed) != 0 || len(paths) != 2 || paths[0] != "/user/systemuids_add" || paths[1] != "/user/systemuids_remove" {
		t.Fatalf("completed=%v failed=%v paths=%v", store.completed, store.failed, paths)
	}
}

func TestOutboxWorkerPublishesCallEventAsWukongCommand(t *testing.T) {
	var request sendMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message/send" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(CallEventPayload{
		Recipients: []string{"usr_a", "usr_b"},
		Event:      "call.ended",
		Param: map[string]any{
			"schemaVersion": 1,
			"contentType":   ContentTypeCallEvent,
			"event":         "call.ended",
			"callId":        "call-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &outboxTestStore{items: []OutboxItem{{ID: 9, Operation: OperationCallEvent, Payload: payload}}}
	worker, err := NewOutboxWorker(store, client)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.runOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(store.completed) != 1 || store.completed[0] != 9 || len(store.failed) != 0 {
		t.Fatalf("completed=%v failed=%v", store.completed, store.failed)
	}
	var command map[string]any
	if err = json.Unmarshal(request.Payload, &command); err != nil {
		t.Fatal(err)
	}
	param, _ := command["param"].(map[string]any)
	if command["type"] != float64(ContentTypeCommand) || command["cmd"] != "call.ended" || param["callId"] != "call-1" {
		t.Fatalf("command=%#v", command)
	}
}

func TestOutboxWorkerPublishesBusinessEventAsWukongCommand(t *testing.T) {
	var request sendMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message/send" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(CommandPayload{
		Recipients: []string{"usr_a", "usr_b"},
		Event:      "message.edited",
		Param: map[string]any{
			"schemaVersion": 1,
			"event":         "message.edited",
			"messageId":     "msg-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &outboxTestStore{items: []OutboxItem{{ID: 10, Operation: OperationBusinessEvent, Payload: payload}}}
	worker, err := NewOutboxWorker(store, client)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.runOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(store.completed) != 1 || store.completed[0] != 10 || len(store.failed) != 0 {
		t.Fatalf("completed=%v failed=%v", store.completed, store.failed)
	}
	var command map[string]any
	if err = json.Unmarshal(request.Payload, &command); err != nil {
		t.Fatal(err)
	}
	param, _ := command["param"].(map[string]any)
	if command["type"] != float64(ContentTypeCommand) || command["cmd"] != "message.edited" || param["messageId"] != "msg-1" {
		t.Fatalf("command=%#v", command)
	}
}

func TestOutboxWorkerPublishesPersistentSystemMessage(t *testing.T) {
	var request sendMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message/send" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": http.StatusOK,
			"data":   map[string]any{"message_id": 456, "client_msg_no": "group-system-1"},
		})
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(StoredMessageRequest{
		ClientMsgNo: "group-system-1", FromUID: "usr_owner", ChannelID: "group-1", ChannelType: ChannelGroup,
		Payload: map[string]any{"type": ContentTypeSystemEvent, "schemaVersion": 1, "event": "group.member.joined"},
	})
	store := &outboxTestStore{items: []OutboxItem{{ID: 11, Operation: OperationStoredMessage, Payload: payload}}}
	worker, err := NewOutboxWorker(store, client)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.runOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(store.completed) != 1 || len(store.failed) != 0 || request.Header.NoPersist != 0 || request.ChannelID != "group-1" || request.ChannelType != ChannelGroup {
		t.Fatalf("completed=%v failed=%v request=%+v", store.completed, store.failed, request)
	}
	var content map[string]any
	if err = json.Unmarshal(request.Payload, &content); err != nil {
		t.Fatal(err)
	}
	if content["type"] != float64(ContentTypeSystemEvent) || content["schemaVersion"] != float64(1) || content["event"] != "group.member.joined" {
		t.Fatalf("content=%#v", content)
	}
}
