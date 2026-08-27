package wukong

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestProvisionUserUsesPinnedContract(t *testing.T) {
	var received UserTokenRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/token" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("token"); got != "manager-secret" {
			t.Fatalf("manager token header=%q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	want := UserTokenRequest{UID: "usr_1", Token: "token_1", DeviceFlag: DeviceWeb, DeviceLevel: DeviceLevelMaster}
	if err = client.ProvisionUser(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if received != want {
		t.Fatalf("received %#v, want %#v", received, want)
	}
}

func TestSystemUIDsUsePinnedContracts(t *testing.T) {
	var paths []string
	var payloads []struct {
		UIDs []string `json:"uids"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var payload struct {
			UIDs []string `json:"uids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.AddSystemUIDs(t.Context(), []string{" usr_notice ", "usr_notice"}); err != nil {
		t.Fatal(err)
	}
	if err = client.RemoveSystemUIDs(t.Context(), []string{"usr_notice"}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/user/systemuids_add" || paths[1] != "/user/systemuids_remove" || len(payloads[0].UIDs) != 1 || payloads[0].UIDs[0] != "usr_notice" {
		t.Fatalf("paths=%v payloads=%+v", paths, payloads)
	}
}

func TestClientRetriesServerFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret", MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts=%d, want 2", got)
	}
}

func TestSessionIssuerMapsPlatformsAndUsesStableToken(t *testing.T) {
	var last UserTokenRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&last)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, _ := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	issuer, err := NewSessionIssuer(client, "01234567890123456789012345678901", "tcp://im:5100", "wss://im.example/im")
	if err != nil {
		t.Fatal(err)
	}
	first, err := issuer.Issue(context.Background(), "usr_1", "web")
	if err != nil {
		t.Fatal(err)
	}
	second, err := issuer.Issue(context.Background(), "usr_1", "web")
	if err != nil {
		t.Fatal(err)
	}
	if first.Token != second.Token || first.DeviceFlag != DeviceWeb || first.SDK != "wukongimjssdk" || last.DeviceFlag != DeviceWeb {
		t.Fatalf("unexpected sessions %#v %#v request %#v", first, second, last)
	}
}

func TestAIChannelTypesAreReserved(t *testing.T) {
	if SupportedChannelType(ChannelAgent) || SupportedChannelType(ChannelAgentCommunity) {
		t.Fatal("AI channel types 11 and 12 must remain reserved")
	}
}

func TestAccessListsUsePinnedEndpointAndFields(t *testing.T) {
	var path string
	var received AccessListRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.AddAllowlist(t.Context(), "usr_b", ChannelPerson, []string{"usr_a"}); err != nil {
		t.Fatal(err)
	}
	if path != "/channel/whitelist_add" || received.ChannelID != "usr_b" || received.ChannelType != ChannelPerson || len(received.UIDs) != 1 || received.UIDs[0] != "usr_a" {
		t.Fatalf("path=%s request=%#v", path, received)
	}
	if err = client.AddDenylist(t.Context(), "usr_a", ChannelPerson, []string{"usr_b"}); err != nil {
		t.Fatal(err)
	}
	if path != "/channel/blacklist_add" || received.ChannelID != "usr_a" || received.UIDs[0] != "usr_b" {
		t.Fatalf("path=%s request=%#v", path, received)
	}
}

func TestSendCommandUsesPinnedTargetedCMDContract(t *testing.T) {
	var received sendMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/message/send" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.SendCommand(t.Context(), []string{"usr_b", "usr_b", "usr_a"}, "call.accepted", map[string]any{
		"schemaVersion": 1,
		"contentType":   ContentTypeCallEvent,
		"callId":        "call-1",
	}); err != nil {
		t.Fatal(err)
	}
	if received.Header != (MessageHeader{NoPersist: 1, RedDot: 0, SyncOnce: 1}) {
		t.Fatalf("header=%#v", received.Header)
	}
	if len(received.Subscribers) != 2 || received.Subscribers[0] != "usr_b" || received.Subscribers[1] != "usr_a" {
		t.Fatalf("subscribers=%v", received.Subscribers)
	}
	if received.ChannelID != "" || received.ChannelType != 0 {
		t.Fatalf("targeted command must not set channel: %#v", received)
	}
	var content map[string]any
	if err = json.Unmarshal(received.Payload, &content); err != nil {
		t.Fatal(err)
	}
	param, _ := content["param"].(map[string]any)
	if content["type"] != float64(ContentTypeCommand) || content["cmd"] != "call.accepted" || param["contentType"] != float64(ContentTypeCallEvent) || param["schemaVersion"] != float64(1) {
		t.Fatalf("content=%#v", content)
	}
}

func TestSendStoredMessageUsesPinnedPersistentChannelContract(t *testing.T) {
	var request sendMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/message/send" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": http.StatusOK,
			"data":   map[string]any{"message_id": 123456789, "client_msg_no": "scheduled-1"},
		})
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.SendStoredMessage(t.Context(), StoredMessageRequest{
		ClientMsgNo: "scheduled-1", FromUID: "usr_a", ChannelID: "usr_b",
		ChannelType: ChannelPerson, Expire: 3600,
		Payload: map[string]any{"type": ContentTypeText, "content": "scheduled hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID != 123456789 || result.ClientMsgNo != "scheduled-1" {
		t.Fatalf("result=%+v", result)
	}
	if request.Header.NoPersist != 0 || request.Header.RedDot != 1 || request.Header.SyncOnce != 0 || request.ClientMsgNo != "scheduled-1" || request.FromUID != "usr_a" || request.ChannelID != "usr_b" || request.ChannelType != ChannelPerson || request.Expire != 3600 || len(request.Subscribers) != 0 {
		t.Fatalf("request=%+v", request)
	}
	var payload map[string]any
	if err = json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["type"] != float64(ContentTypeText) || payload["content"] != "scheduled hello" {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestStreamMessageAndEventsUsePinnedContracts(t *testing.T) {
	var anchor sendMessageRequest
	var appended MessageEventRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/message/send":
			if err := json.NewDecoder(r.Body).Decode(&anchor); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "data": map[string]any{"message_id": 88, "client_msg_no": "stream-1"}})
		case "/message/event":
			if err := json.NewDecoder(r.Body).Decode(&appended); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "data": map[string]any{
				"client_msg_no": "stream-1", "event_key": "main", "event_id": "event-1",
				"msg_event_seq": 3, "stream_status": "open", "channel_id": "usr_b",
				"channel_type": 1, "from_uid": "usr_a",
			}})
		case "/message/eventsync":
			var request MessageEventSyncRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.FromMsgEventSeq != 2 || request.Limit != 100 || request.EventKey != "main" {
				t.Fatalf("sync request=%+v", request)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "data": map[string]any{
				"client_msg_no": "stream-1", "from_msg_event_seq": 2, "next_msg_event_seq": 3,
				"more": 0, "filtered_by_event_key": "main", "events": []any{map[string]any{
					"msg_event_seq": 3, "event_id": "event-1", "event_key": "main",
					"event_type": "stream.snapshot", "payload": map[string]any{"kind": "text", "text": "hello"},
				}},
			}})
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.SendStreamMessage(t.Context(), StoredMessageRequest{
		ClientMsgNo: "stream-1", FromUID: "usr_a", ChannelID: "usr_b", ChannelType: ChannelPerson,
		Payload: map[string]any{"type": ContentTypeText, "content": ""},
	})
	if err != nil || result.MessageID != 88 || anchor.IsStream != 1 {
		t.Fatalf("anchor=%+v result=%+v err=%v", anchor, result, err)
	}
	eventResult, err := client.AppendMessageEvent(t.Context(), MessageEventRequest{
		ChannelID: "usr_b", ChannelType: ChannelPerson, FromUID: "usr_a", MessageID: 88,
		ClientMsgNo: "stream-1", EventID: "event-1", EventType: MessageEventStreamDelta,
		Payload: map[string]any{"kind": "text", "delta": "hello"},
	})
	if err != nil || eventResult.MsgEventSeq != 3 || appended.EventKey != "main" || appended.MessageID != 88 {
		t.Fatalf("appended=%+v result=%+v err=%v", appended, eventResult, err)
	}
	synced, err := client.SyncMessageEvents(t.Context(), MessageEventSyncRequest{
		ChannelID: "usr_b", ChannelType: ChannelPerson, FromUID: "usr_a", ClientMsgNo: "stream-1",
		EventKey: "main", FromMsgEventSeq: 2, Limit: 100,
	})
	if err != nil || len(synced.Events) != 1 || synced.Events[0].Payload["text"] != "hello" {
		t.Fatalf("sync=%+v err=%v", synced, err)
	}
}

func TestFinishEventStripsPayloadAndKey(t *testing.T) {
	var received MessageEventRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "data": map[string]any{
			"client_msg_no": "stream-1", "event_key": "__finish__", "event_id": "finish-1",
			"msg_event_seq": 4, "stream_status": "closed", "channel_id": "usr_b", "channel_type": 1, "from_uid": "usr_a",
		}})
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.AppendMessageEvent(t.Context(), MessageEventRequest{
		ChannelID: "usr_b", ChannelType: ChannelPerson, FromUID: "usr_a", ClientMsgNo: "stream-1",
		EventID: "finish-1", EventType: MessageEventStreamFinish, EventKey: "main", Payload: map[string]any{"bad": true},
	})
	if err != nil || received.EventKey != "" || received.Payload != nil {
		t.Fatalf("received=%+v err=%v", received, err)
	}
}

func TestConversationReadStateUsesPinnedContracts(t *testing.T) {
	var unreadRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("token") != "manager-secret" {
			t.Fatalf("manager token=%q", r.Header.Get("token"))
		}
		switch r.URL.Path {
		case "/channel/max_message_seq":
			if r.Method != http.MethodGet || r.URL.Query().Get("login_uid") != "usr_a" || r.URL.Query().Get("channel_id") != "usr_b" || r.URL.Query().Get("channel_type") != "1" {
				t.Fatalf("max sequence request=%s %s", r.Method, r.URL.String())
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"message_seq": 17})
		case "/conversations/setUnread":
			if r.Method != http.MethodPost {
				t.Fatalf("set unread method=%s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&unreadRequest); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := client.ChannelMaxMessageSeq(t.Context(), "usr_a", "usr_b", ChannelPerson)
	if err != nil || sequence != 17 {
		t.Fatalf("sequence=%d err=%v", sequence, err)
	}
	if err = client.SetConversationUnread(t.Context(), "usr_a", "usr_b", ChannelPerson, 4); err != nil {
		t.Fatal(err)
	}
	if unreadRequest["uid"] != "usr_a" || unreadRequest["channel_id"] != "usr_b" || unreadRequest["channel_type"] != float64(ChannelPerson) || unreadRequest["unread"] != float64(4) {
		t.Fatalf("unread request=%#v", unreadRequest)
	}
}

func TestSendBusinessEventVersionsPayloadAndChunksRecipients(t *testing.T) {
	var requests []sendMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message/send" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		var request sendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	recipients := make([]string, 0, MaxCommandRecipients+2)
	for index := 0; index <= MaxCommandRecipients; index++ {
		recipients = append(recipients, fmt.Sprintf("usr_%04d", index))
	}
	recipients = append(recipients, recipients[0])
	if err = client.SendBusinessEvent(t.Context(), recipients, "typing", map[string]any{"conversationId": "c1", "typing": true}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || len(requests[0].Subscribers) != MaxCommandRecipients || len(requests[1].Subscribers) != 1 {
		t.Fatalf("request chunks=%d first=%d second=%d", len(requests), len(requests[0].Subscribers), len(requests[1].Subscribers))
	}
	for _, request := range requests {
		if request.Header.NoPersist != 1 || request.Header.SyncOnce != 1 || request.ChannelID != "" || request.ChannelType != 0 {
			t.Fatalf("request=%+v", request)
		}
		var command map[string]any
		if err = json.Unmarshal(request.Payload, &command); err != nil {
			t.Fatal(err)
		}
		param, _ := command["param"].(map[string]any)
		payload, _ := param["payload"].(map[string]any)
		if command["type"] != float64(ContentTypeCommand) || command["cmd"] != "typing" || param["schemaVersion"] != float64(1) || param["event"] != "typing" || payload["conversationId"] != "c1" || payload["typing"] != true {
			t.Fatalf("command=%#v", command)
		}
	}
}

func TestConnectionsUsesPinnedManagerConnzContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/connz" || r.URL.Query().Get("offset") != "20" || r.URL.Query().Get("limit") != "50" || r.URL.Query().Get("sort") != "id" || r.URL.Query().Get("uid") != "usr_a" {
			t.Fatalf("request=%s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("token") != "manager-secret" {
			t.Fatalf("token=%q", r.Header.Get("token"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"connections": []map[string]any{{"id": 9, "uid": "usr_a", "device": "App(master)", "device_id": "device-a", "node_id": 1}},
			"total":       1, "offset": 20, "limit": 50, "now": "2026-08-11T00:00:00Z",
		})
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	connections, err := client.Connections(t.Context(), "usr_a", 20, 50)
	if err != nil {
		t.Fatal(err)
	}
	if connections.Total != 1 || len(connections.Connections) != 1 || connections.Connections[0].UID != "usr_a" || connections.Connections[0].DeviceID != "device-a" {
		t.Fatalf("connections=%+v", connections)
	}
}

func TestSyncContractsUsePinnedFieldNames(t *testing.T) {
	requests := map[string]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		requests[r.URL.Path] = body
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/conversation/sync" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`{"start_message_seq":1,"end_message_seq":2,"more":0,"messages":[]}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.SyncConversations(t.Context(), ConversationSyncRequest{UID: "usr_a", LastMsgSeqs: "usr_b:1:3", MsgCount: 200, Page: 1, PageSize: 100}); err != nil {
		t.Fatal(err)
	}
	if requests["/conversation/sync"]["last_msg_seqs"] != "usr_b:1:3" || requests["/conversation/sync"]["msg_count"] != float64(200) {
		t.Fatalf("conversation request=%v", requests["/conversation/sync"])
	}
	if _, err = client.SyncConversations(t.Context(), ConversationSyncRequest{UID: "usr_a", MsgCount: 201}); err == nil {
		t.Fatal("expected an oversized conversation sync request to fail")
	}
	if _, err = client.SyncMessages(t.Context(), MessageSyncRequest{LoginUID: "usr_a", ChannelID: "usr_b", ChannelType: ChannelPerson, StartMessageSeq: 1, EndMessageSeq: 3, Limit: 50, PullMode: 1, EventSummaryMode: "full"}); err != nil {
		t.Fatal(err)
	}
	if requests["/channel/messagesync"]["login_uid"] != "usr_a" || requests["/channel/messagesync"]["event_summary_mode"] != "full" || requests["/channel/messagesync"]["pull_mode"] != float64(1) {
		t.Fatalf("message request=%v", requests["/channel/messagesync"])
	}
}

func TestSearchMessagesUsesPinnedChannelAndIDContract(t *testing.T) {
	var request MessageSearchRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"messages":[{"message_idstr":"123","message_seq":7,"client_msg_no":"client-1","from_uid":"usr_a","channel_id":"usr_b","channel_type":1,"timestamp":1700000000,"payload":"eyJ0eXBlIjoxLCJjb250ZW50IjoiaGVsbG8ifQ=="}]}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := client.SearchMessages(t.Context(), MessageSearchRequest{LoginUID: "usr_a", ChannelID: "usr_b", ChannelType: ChannelPerson, MessageIDs: []int64{123}})
	if err != nil {
		t.Fatal(err)
	}
	content, _ := messages[0]["payload"].(map[string]any)
	if request.LoginUID != "usr_a" || request.ChannelID != "usr_b" || len(request.MessageIDs) != 1 || request.MessageIDs[0] != 123 || content["content"] != "hello" {
		t.Fatalf("request=%+v messages=%#v", request, messages)
	}
}

func TestSyncDecodesPinnedBase64PayloadForSDKDataSources(t *testing.T) {
	payload := "eyJ0eXBlIjoxLCJjb250ZW50IjoiaGVsbG8ifQ=="
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		message := `{"message_idstr":"123","message_seq":7,"client_msg_no":"client-1","from_uid":"usr_b","channel_id":"usr_b","channel_type":1,"timestamp":1700000000,"payload":"` + payload + `"}`
		if r.URL.Path == "/conversation/sync" {
			_, _ = w.Write([]byte(`[{"channel_id":"usr_b","channel_type":1,"recents":[` + message + `]}]`))
			return
		}
		_, _ = w.Write([]byte(`{"start_message_seq":7,"end_message_seq":7,"more":0,"messages":[` + message + `]}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	conversations, err := client.SyncConversations(t.Context(), ConversationSyncRequest{UID: "usr_a", MsgCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	content, ok := conversations[0].Recents[0]["payload"].(map[string]any)
	if !ok || content["content"] != "hello" || content["type"] != float64(1) {
		t.Fatalf("conversation payload=%#v", conversations[0].Recents[0]["payload"])
	}
	messages, err := client.SyncMessages(t.Context(), MessageSyncRequest{LoginUID: "usr_a", ChannelID: "usr_b", ChannelType: ChannelPerson, Limit: 50, PullMode: 1})
	if err != nil {
		t.Fatal(err)
	}
	content, ok = messages.Messages[0]["payload"].(map[string]any)
	if !ok || content["content"] != "hello" {
		t.Fatalf("message payload=%#v", messages.Messages[0]["payload"])
	}
}

func TestPinnedManagerOperationsNeverExposeOrOmitManagerAuthentication(t *testing.T) {
	requests := make([]string, 0, 8)
	bodies := map[string]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("token") != "manager-secret" {
			t.Fatalf("manager token=%q", r.Header.Get("token"))
		}
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			bodies[r.URL.Path] = body
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/varz":
			_, _ = w.Write([]byte(`{"server_id":"1","version":"v2.2.5","connections":3}`))
		case "/varz/setting":
			_, _ = w.Write([]byte(`{"logger":{"trace_on":1,"loki_on":0},"prometheus_on":1,"stress_on":0}`))
		case "/cluster/nodes":
			_, _ = w.Write([]byte(`{"total":1,"data":[{"id":1,"online":1}]}`))
		case "/cluster/channels":
			_, _ = w.Write([]byte(`{"data":[{"channel_id":"group-1","channel_type":2}],"more":0}`))
		case "/cluster/messages":
			_, _ = w.Write([]byte(`{"data":[{"message_id":9}],"total":1}`))
		case "/cluster/devices":
			_, _ = w.Write([]byte(`{"data":[{"uid":"u1","device_flag":1}],"total":1}`))
		case "/plugins":
			_, _ = w.Write([]byte(`[{"no":"wk.plugin.im-policy","status":"normal","config":{"secret":"******"}}]`))
		case "/pluginlogs/wk.plugin.im-policy":
			_, _ = w.Write([]byte(`{"plugin_no":"wk.plugin.im-policy","node_id":1,"entries":[{"sequence":7,"stream":"stderr","timestamp":1770000000000,"message":"policy timeout"}]}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-secret"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if overview, err := client.ManagerVarz(ctx); err != nil || overview["connections"] != float64(3) {
		t.Fatalf("overview=%v err=%v", overview, err)
	}
	if settings, settingsErr := client.ManagerSettings(ctx); settingsErr != nil || settings["prometheus_on"] != float64(1) {
		t.Fatalf("settings=%v err=%v", settings, settingsErr)
	}
	if _, err = client.ManagerNodes(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ManagerChannels(ctx, ManagerChannelQuery{ChannelID: "group-1", ChannelType: ChannelGroup, OffsetCreated: 100, Previous: true, Limit: 25}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ManagerMessages(ctx, ManagerMessageQuery{FromUID: "u1", ChannelID: "group-1", ChannelType: ChannelGroup, MessageID: 9, ClientMsgNo: "c9", Limit: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ManagerDevices(ctx, ManagerDeviceQuery{UID: "u1", DeviceFlag: DeviceWeb, Limit: 20}); err != nil {
		t.Fatal(err)
	}
	plugins, err := client.ManagerPlugins(ctx, 1)
	if err != nil || len(plugins) != 1 || plugins[0]["no"] != "wk.plugin.im-policy" {
		t.Fatalf("plugins=%v err=%v", plugins, err)
	}
	if err = client.UpdatePluginConfig(ctx, "wk.plugin.im-policy", 1, map[string]any{"endpoint": "http://server/policy"}); err != nil {
		t.Fatal(err)
	}
	if err = client.UninstallPlugin(ctx, "wk.plugin.im-policy", 1); err != nil {
		t.Fatal(err)
	}
	logs, err := client.ManagerPluginLogs(ctx, "wk.plugin.im-policy", 1, 100)
	if err != nil || logs.PluginNo != "wk.plugin.im-policy" || len(logs.Entries) != 1 || logs.Entries[0].Message != "policy timeout" {
		t.Fatalf("plugin logs=%v err=%v", logs, err)
	}
	wantChannel := "GET /cluster/channels?channel_id=group-1&channel_type=2&limit=25&offset_created_at=100&pre=1"
	wantMessage := "GET /cluster/messages?channel_id=group-1&channel_type=2&client_msg_no=c9&from_uid=u1&limit=20&message_id=9"
	if !containsString(requests, "GET /varz/setting") || !containsString(requests, wantChannel) || !containsString(requests, wantMessage) || !containsString(requests, "GET /cluster/devices?device_flag=1&limit=20&uid=u1") || !containsString(requests, "GET /plugins?node_id=1") || !containsString(requests, "GET /pluginlogs/wk.plugin.im-policy?limit=100&node_id=1") {
		t.Fatalf("requests=%v", requests)
	}
	if bodies["/pluginconfig/wk.plugin.im-policy"]["node_id"] != float64(1) || bodies["/plugin/uninstall"]["plugin_no"] != "wk.plugin.im-policy" {
		t.Fatalf("bodies=%v", bodies)
	}
}

func containsString(items []string, expected string) bool {
	for _, item := range items {
		if item == expected {
			return true
		}
	}
	return false
}
