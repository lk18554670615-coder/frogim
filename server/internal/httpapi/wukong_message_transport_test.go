package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
	"github.com/linli/im/server/internal/wukong"
)

type adminAuditCaptureStore struct {
	teststore.Memory
	entry *model.AuditEntry
}

func (*adminAuditCaptureStore) AdminStats(context.Context) (map[string]any, error) {
	return map[string]any{}, nil
}
func (*adminAuditCaptureStore) ListAdminGroups(context.Context, string, string, string, int) ([]map[string]any, int64, string, error) {
	return nil, 0, "", nil
}
func (*adminAuditCaptureStore) ListAdminFriendships(context.Context, string, string, int) ([]store.AdminFriendship, int64, string, error) {
	return nil, 0, "", nil
}
func (*adminAuditCaptureStore) ListAdminFeedback(context.Context, string, string, string, int) ([]store.AdminFeedback, int64, string, error) {
	return nil, 0, "", nil
}
func (*adminAuditCaptureStore) AdminPushStatus(context.Context) (map[string]any, error) {
	return map[string]any{}, nil
}
func (*adminAuditCaptureStore) AdminTaskStatus(context.Context) (map[string]any, error) {
	return map[string]any{}, nil
}
func (*adminAuditCaptureStore) AdminUserOverview(context.Context, string) (map[string]any, error) {
	return map[string]any{}, nil
}
func (*adminAuditCaptureStore) AdminGroupOverview(context.Context, string) (map[string]any, error) {
	return map[string]any{}, nil
}
func (*adminAuditCaptureStore) ListAdminGroupMembers(context.Context, string, string, string, int) ([]*model.ConversationMember, int64, string, error) {
	return nil, 0, "", nil
}
func (s *adminAuditCaptureStore) RecordAdminAudit(_ context.Context, entry *model.AuditEntry) error {
	s.entry = entry
	return nil
}

func TestAdminMessageBodiesLoadFromWuKongByCurrentPage(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/messages" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		requestCount.Add(1)
		var input wukong.MessageSearchRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.LoginUID != "usr_a" || input.ChannelID != "usr_b" || input.ChannelType != wukong.ChannelPerson || !reflect.DeepEqual(input.MessageIDs, []int64{101, 102}) {
			t.Fatalf("search request=%+v", input)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"messages": []wukong.SyncedMessage{
			{"message_idstr": "102", "client_msg_no": "client-2", "from_uid": "usr_b", "message_seq": 2, "timestamp": 1700000001, "payload": map[string]any{"type": wukong.ContentTypeText, "content": "original"}},
			{"message_idstr": "101", "client_msg_no": "client-1", "from_uid": "usr_a", "message_seq": 1, "timestamp": 1700000000, "payload": map[string]any{"type": wukong.ContentTypeText, "content": "live body"}},
		}})
	}))
	defer server.Close()
	client, err := wukong.NewClient(wukong.Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	api := &API{wukongClient: client}
	items := []*model.Message{
		{ID: "101", ConversationID: "conv-direct", SenderID: "usr_a", WukongChannelID: "usr_b", WukongChannelType: wukong.ChannelPerson, Type: "text"},
		{ID: "102", ConversationID: "conv-direct", SenderID: "usr_b", WukongChannelID: "usr_a", WukongChannelType: wukong.ChannelPerson, Type: "text", Body: map[string]any{"text": "edited body"}},
	}
	loaded, missing, err := api.loadAdminMessageBodies(t.Context(), items)
	if err != nil || loaded != 2 || missing != 0 || requestCount.Load() != 1 {
		t.Fatalf("loaded=%d missing=%d requests=%d err=%v", loaded, missing, requestCount.Load(), err)
	}
	if items[0].Body["text"] != "live body" || items[1].Body["text"] != "edited body" {
		t.Fatalf("items=%+v", items)
	}
}

func TestAdminMessageViewAuditExcludesMessageBodies(t *testing.T) {
	capture := &adminAuditCaptureStore{}
	application, err := app.New(t.Context(), capture)
	if err != nil {
		t.Fatal(err)
	}
	api := &API{app: application}
	request := httptest.NewRequest(http.MethodGet, "/v2/admin/messages?q=conv-1&type=text&cursor=20", nil)
	request = request.WithContext(context.WithValue(request.Context(), userKey, "admin-1"))
	api.recordAdminMessageView(request, "success", 20, 19, 1, nil)
	audit := capture.entry
	if audit == nil || audit.Action != "message.search.viewed" || audit.ActorID != "admin-1" || audit.Metadata["contentLoaded"] != 19 || audit.Metadata["query"] != "conv-1" {
		t.Fatalf("audit=%+v", audit)
	}
	if _, leaked := audit.Metadata["body"]; leaked {
		t.Fatalf("audit leaked message body: %+v", audit.Metadata)
	}
}

func TestWukongMessagePayloadMapsLegacyFieldsWithoutChangingBusinessBody(t *testing.T) {
	body := map[string]any{"text": "你好", "forwarded": true}
	payload, err := wukongMessagePayload(app.MessageTransportRequest{
		Type: "text", Body: body, ReplyToID: "123",
		Mentions: []string{"u2"}, MentionAll: true,
	}, time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if payload["type"] != wukong.ContentTypeText || payload["content"] != "你好" || payload["text"] != nil || payload["forwarded"] != true {
		t.Fatalf("unexpected text payload %#v", payload)
	}
	if !reflect.DeepEqual(payload["mention"], map[string]any{"uids": []string{"u2"}, "all": 1}) {
		t.Fatalf("unexpected mention %#v", payload["mention"])
	}
	if reply, _ := payload["reply"].(map[string]any); reply["message_id"] != "123" {
		t.Fatalf("unexpected reply %#v", payload["reply"])
	}
	if _, changed := body["content"]; changed || body["text"] != "你好" {
		t.Fatalf("business body was mutated %#v", body)
	}
}

func TestWukongMessagePayloadUsesPinnedGIFContentType(t *testing.T) {
	payload, err := wukongMessagePayload(app.MessageTransportRequest{
		Type: "image",
		Body: map[string]any{"mediaId": "gif-1", "mime": "image/gif"},
	}, time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if payload["type"] != wukong.ContentTypeGIF || payload["mediaId"] != "gif-1" {
		t.Fatalf("unexpected GIF payload %#v", payload)
	}
}

func TestWukongForwardSourceMapsPinnedMessageWithoutPersistingPayload(t *testing.T) {
	raw := wukong.SyncedMessage{
		"message_idstr": "2087037357243928576", "client_msg_no": "client-1",
		"from_uid": "usr_a", "message_seq": float64(7), "timestamp": float64(1700000000),
		"payload": map[string]any{
			"type": float64(wukong.ContentTypeText), "content": "hello",
			"reply": map[string]any{"message_id": "123"},
		},
		"event_meta": map[string]any{"events": []any{map[string]any{
			"event_key": "main", "status": "closed",
			"snapshot": map[string]any{"kind": "text", "text": "streamed hello"},
		}}},
	}
	message, err := wukongForwardSource(raw, store.WukongMessageRef{
		MessageID: "2087037357243928576", ConversationID: "conv-1", ChannelID: "usr_b", ChannelType: wukong.ChannelPerson,
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.ID != "2087037357243928576" || message.Type != "text" || message.Body["text"] != "streamed hello" || message.ReplyToID != "123" || message.Seq != 7 || !message.CreatedAt.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("message=%+v", message)
	}
}

func TestWukongMessagePayloadVersionsMergedHistory(t *testing.T) {
	payload, err := wukongMessagePayload(app.MessageTransportRequest{
		Type: "chat_history",
		Body: map[string]any{"entries": []map[string]any{{"sourceMessageId": "1"}}},
	}, time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if payload["type"] != wukong.ContentTypeMergedHistory || payload["schemaVersion"] != 1 || payload["digest"] != "[聊天记录]" {
		t.Fatalf("unexpected merged payload %#v", payload)
	}
}

func TestWukongMessagePayloadCarriesPortableAbsoluteExpiry(t *testing.T) {
	createdAt := time.Date(2026, 8, 11, 1, 2, 3, 400, time.UTC)
	payload, err := wukongMessagePayload(app.MessageTransportRequest{
		Type: "text", Body: map[string]any{"text": "temporary"}, ExpiresInSeconds: 300,
	}, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	want := createdAt.Add(5 * time.Minute).Format(time.RFC3339Nano)
	if payload["expiresAt"] != want {
		t.Fatalf("expiresAt=%v want %s", payload["expiresAt"], want)
	}
}

func TestWukongMessageMediaIDOnlyAcceptsMediaMessages(t *testing.T) {
	for _, messageType := range []string{"image", "audio", "video", "file"} {
		if got := wukongMessageMediaID(app.MessageTransportRequest{Type: messageType, Body: map[string]any{"mediaId": " media-1 "}}); got != "media-1" {
			t.Fatalf("type=%s media id=%q", messageType, got)
		}
	}
	if got := wukongMessageMediaID(app.MessageTransportRequest{Type: "text", Body: map[string]any{"mediaId": "media-1"}}); got != "" {
		t.Fatalf("text message unexpectedly bound media %q", got)
	}
}

func TestWukongMessageTransportDeduplicatesImmediateAndConcurrentRetries(t *testing.T) {
	application, err := app.New(t.Context(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if err = application.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	friendRequest, err := application.RequestFriend("usr_alice", "usr_bob", "transport test")
	if err != nil {
		t.Fatal(err)
	}
	if err = application.AcceptFriend("usr_bob", friendRequest.ID); err != nil {
		t.Fatal(err)
	}
	conversation, err := application.DirectConversation("usr_alice", "usr_bob")
	if err != nil {
		t.Fatal(err)
	}
	var sendCount atomic.Int32
	var storedMu sync.RWMutex
	var stored wukong.SyncedMessage
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/messages":
			storedMu.RLock()
			current := stored
			storedMu.RUnlock()
			messages := []wukong.SyncedMessage{}
			if current != nil {
				messages = append(messages, current)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"messages": messages})
		case "/message/send":
			var input struct {
				ClientMsgNo string `json:"client_msg_no"`
				FromUID     string `json:"from_uid"`
				ChannelID   string `json:"channel_id"`
				ChannelType uint8  `json:"channel_type"`
				Expire      int64  `json:"expire"`
				Payload     []byte `json:"payload"`
			}
			if decodeErr := json.NewDecoder(request.Body).Decode(&input); decodeErr != nil {
				t.Error(decodeErr)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			var payload map[string]any
			if decodeErr := json.Unmarshal(input.Payload, &payload); decodeErr != nil {
				t.Error(decodeErr)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			sendCount.Add(1)
			storedMu.Lock()
			stored = wukong.SyncedMessage{
				"message_idstr": "987654321", "client_msg_no": input.ClientMsgNo,
				"from_uid": input.FromUID, "channel_id": input.ChannelID,
				"channel_type": float64(input.ChannelType), "message_seq": float64(9),
				"expire": float64(input.Expire), "timestamp": float64(1700000000), "payload": payload,
			}
			storedMu.Unlock()
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"status": 200, "data": map[string]any{"message_id": 987654321, "client_msg_no": input.ClientMsgNo},
			})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := wukong.NewClient(wukong.Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	transport := newWukongMessageTransport(client, application)
	request := app.MessageTransportRequest{
		UserID: "usr_alice", ConversationID: conversation.ID, ClientMsgID: "immediate-retry",
		Type: "text", Body: map[string]any{"text": "same body"},
	}
	first, err := transport(t.Context(), request)
	if err != nil || first.Duplicate {
		t.Fatalf("first=%+v err=%v", first, err)
	}

	const retries = 8
	results := make(chan app.MessageTransportResult, retries)
	errorsChannel := make(chan error, retries)
	var workers sync.WaitGroup
	for range retries {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, sendErr := transport(context.Background(), request)
			results <- result
			errorsChannel <- sendErr
		}()
	}
	workers.Wait()
	close(results)
	close(errorsChannel)
	for sendErr := range errorsChannel {
		if sendErr != nil {
			t.Fatal(sendErr)
		}
	}
	for result := range results {
		if !result.Duplicate || result.MessageID != first.MessageID || result.MessageSeq != 9 {
			t.Fatalf("retry=%+v first=%+v", result, first)
		}
	}
	if got := sendCount.Load(); got != 1 {
		t.Fatalf("WuKong sends=%d want 1", got)
	}

	changed := request
	changed.Body = map[string]any{"text": "changed body"}
	if _, err = transport(t.Context(), changed); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("changed retry err=%v", err)
	}
}
