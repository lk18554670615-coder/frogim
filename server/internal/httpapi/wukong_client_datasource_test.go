package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/auth"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/store"
)

func TestWukongClientDatasourceForcesAuthenticatedUID(t *testing.T) {
	var received map[string]any
	wukongServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversation/sync" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer wukongServer.Close()
	a, err := app.New(t.Context(), store.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	user, err := a.Login("13800002001", "Sync user")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		JWTSecret: "test-secret", WukongEnabled: true,
		WukongAPIURL: wukongServer.URL, WukongManagerURL: wukongServer.URL, WukongManagerToken: "manager-secret",
		WukongTokenSecret: "01234567890123456789012345678901", WukongTCPURL: "tcp://im:5100", WukongWSURL: "wss://im.example/im",
	}
	api := New(cfg, a)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	accessToken, _, err := (auth.Manager{Secret: []byte(cfg.JWTSecret), AccessTTL: time.Minute, RefreshTTL: time.Hour}).Issue(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	response := authenticatedRequest(t, http.MethodPost, server.URL+"/v2/im/datasource/conversations", accessToken, `{"version":0,"lastMsgSeqs":"","msgCount":200,"onlyUnread":false,"excludeChannelTypes":[],"page":1,"pageSize":100}`)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	if received["uid"] != user.ID || received["msg_count"] != float64(200) {
		t.Fatalf("received=%v", received)
	}

	tooMany := authenticatedRequest(t, http.MethodPost, server.URL+"/v2/im/datasource/conversations", accessToken, `{"version":0,"lastMsgSeqs":"","msgCount":201,"page":0,"pageSize":100}`)
	tooMany.Body.Close()
	if tooMany.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized status=%d", tooMany.StatusCode)
	}

	bad := authenticatedRequest(t, http.MethodPost, server.URL+"/v2/im/datasource/conversations", accessToken, strings.Repeat(`{`, 2))
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad status=%d", bad.StatusCode)
	}
}

func TestWukongMessageExtensionDatasourceValidatesBatch(t *testing.T) {
	a, err := app.New(t.Context(), store.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	user, _ := a.Login("13800002009", "Extension user")
	cfg := config.Config{JWTSecret: strings.Repeat("e", 32), AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	server := httptest.NewServer(New(cfg, a).Handler())
	defer server.Close()
	token, _, _ := (auth.Manager{Secret: []byte(cfg.JWTSecret), AccessTTL: time.Minute, RefreshTTL: time.Hour}).Issue(user.ID)

	response := authenticatedRequest(t, http.MethodPost, server.URL+"/v2/im/datasource/extensions", token, `{"messageIds":["123","123"]}`)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var decoded struct {
		Items map[string]map[string]any `json:"items"`
	}
	if err = json.NewDecoder(response.Body).Decode(&decoded); err != nil || decoded.Items == nil {
		t.Fatalf("response=%+v err=%v", decoded, err)
	}

	bad := authenticatedRequest(t, http.MethodPost, server.URL+"/v2/im/datasource/extensions", token, `{"messageIds":[]}`)
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad status=%d", bad.StatusCode)
	}
}

func TestWukongMessageDatasourceEnrichesBoundMediaWithFreshSignedURL(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte(`{"type":2,"mediaId":"med_bound"}`))
	wukongServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/channel/messagesync" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"start_message_seq":1,"end_message_seq":1,"more":0,"messages":[{"message_idstr":"1","message_seq":1,"client_msg_no":"c1","from_uid":"owner","channel_id":"recipient","channel_type":1,"timestamp":1786406400,"payload":"` + payload + `"}]}`))
	}))
	defer wukongServer.Close()

	a, _ := app.New(t.Context(), store.Memory{})
	owner, _ := a.Login("13800002011", "Owner")
	recipient, _ := a.Login("13800002012", "Recipient")
	friendRequest, _ := a.RequestFriendWithSource(owner.ID, recipient.ID, "hello", "search")
	if err := a.AcceptFriend(recipient.ID, friendRequest.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateMedia(store.Media{ID: "med_bound", OwnerID: owner.ID, ObjectKey: "objects/med_bound", MIME: "image/png", Size: 10, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if err := a.BindMediaChannel(store.MediaChannelBinding{MediaID: "med_bound", ChannelID: recipient.ID, ChannelType: 1, SenderID: owner.ID}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		JWTSecret: "test-secret", WukongEnabled: true,
		WukongAPIURL: wukongServer.URL, WukongManagerURL: wukongServer.URL, WukongManagerToken: "manager-secret",
		WukongTokenSecret: "01234567890123456789012345678901", WukongTCPURL: "tcp://im:5100", WukongWSURL: "wss://im.example/im",
	}
	api := New(cfg, a)
	api.media = signedMediaService{}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	token, _, _ := (auth.Manager{Secret: []byte(cfg.JWTSecret), AccessTTL: time.Minute, RefreshTTL: time.Hour}).Issue(recipient.ID)
	response := authenticatedRequest(t, http.MethodPost, server.URL+"/v2/im/datasource/messages", token,
		`{"channelId":"`+owner.ID+`","channelType":1,"startMessageSeq":0,"endMessageSeq":0,"limit":50,"pullMode":1,"eventSummaryMode":"full"}`)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var decoded struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Messages) != 1 {
		t.Fatalf("messages=%v", decoded.Messages)
	}
	messagePayload, _ := decoded.Messages[0]["payload"].(map[string]any)
	if url, _ := messagePayload["url"].(string); !strings.Contains(url, "X-Amz-Signature") {
		t.Fatalf("payload=%v", messagePayload)
	}
}

func TestWukongMessageDatasourceProjectsPinnedStreamSnapshot(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte(`{"type":1,"content":"placeholder"}`))
	wukongServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/channel/messagesync" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"start_message_seq":1,"end_message_seq":1,"more":0,"messages":[{"message_idstr":"2","message_seq":2,"client_msg_no":"stream-2","from_uid":"owner","channel_id":"recipient","channel_type":1,"setting":2,"timestamp":1786406400,"payload":"` + payload + `","event_meta":{"has_events":true,"completed":true,"last_msg_event_seq":3,"events":[{"event_key":"main","status":"closed","snapshot":{"kind":"text","text":"final stream text"}}]}}]}`))
	}))
	defer wukongServer.Close()

	a, _ := app.New(t.Context(), store.Memory{})
	owner, _ := a.Login("13800002021", "Owner")
	recipient, _ := a.Login("13800002022", "Recipient")
	friendRequest, _ := a.RequestFriendWithSource(owner.ID, recipient.ID, "hello", "search")
	if err := a.AcceptFriend(recipient.ID, friendRequest.ID); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		JWTSecret: "test-secret", WukongEnabled: true,
		WukongAPIURL: wukongServer.URL, WukongManagerURL: wukongServer.URL, WukongManagerToken: "manager-secret",
		WukongTokenSecret: "01234567890123456789012345678901", WukongTCPURL: "tcp://im:5100", WukongWSURL: "wss://im.example/im",
	}
	server := httptest.NewServer(New(cfg, a).Handler())
	defer server.Close()
	token, _, _ := (auth.Manager{Secret: []byte(cfg.JWTSecret), AccessTTL: time.Minute, RefreshTTL: time.Hour}).Issue(recipient.ID)
	response := authenticatedRequest(t, http.MethodPost, server.URL+"/v2/im/datasource/messages", token,
		`{"channelId":"`+owner.ID+`","channelType":1,"startMessageSeq":0,"endMessageSeq":0,"limit":50,"pullMode":1,"eventSummaryMode":"full"}`)
	defer response.Body.Close()
	var decoded struct {
		Messages []map[string]any `json:"messages"`
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil || len(decoded.Messages) != 1 {
		t.Fatalf("messages=%v err=%v", decoded.Messages, err)
	}
	messagePayload, _ := decoded.Messages[0]["payload"].(map[string]any)
	if messagePayload["content"] != "final stream text" {
		t.Fatalf("payload=%v", messagePayload)
	}
}
