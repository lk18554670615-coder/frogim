package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/media"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/realtime"
	"github.com/linli/im/server/internal/store"
	"golang.org/x/crypto/bcrypt"
)

type signedMediaService struct{}

func (signedMediaService) Prepare(context.Context, string, string, string, int64) (media.Prepared, error) {
	return media.Prepared{}, nil
}
func (signedMediaService) Complete(context.Context, string, string, string) (store.Media, error) {
	return store.Media{}, nil
}
func (signedMediaService) DownloadURL(_ context.Context, id string) (string, error) {
	return "https://media.example.test/object/" + id + "?X-Amz-Signature=test&X-Amz-Expires=900", nil
}

func TestRESTAndWebSocketMessageFlow(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", AdminKey: "admin", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	token := loginToken(t, ts.URL, "13800000001")
	conversationID := directConversation(t, ts.URL, token, "usr_bob")
	wsTicket := webSocketTicket(t, ts.URL, token)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/ws?ticket=" + wsTicket
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var ready realtime.Envelope
	if err := conn.ReadJSON(&ready); err != nil || ready.Type != "session.ready" {
		t.Fatalf("ready=%+v err=%v", ready, err)
	}
	replayed, response, replayErr := websocket.DefaultDialer.Dial(wsURL, nil)
	if replayed != nil {
		_ = replayed.Close()
	}
	if replayErr == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed websocket ticket err=%v response=%v", replayErr, response)
	}
	payload, _ := json.Marshal(map[string]any{"conversationId": conversationID, "clientMsgId": "ws-client-1", "messageType": "text", "body": map[string]any{"text": "hello over websocket"}})
	request := realtime.Envelope{Version: 1, RequestID: "r1", Type: "message.send", Payload: payload}
	if err := conn.WriteJSON(request); err != nil {
		t.Fatal(err)
	}
	ack := readType(t, conn, "message.ack")
	var result struct {
		Message struct {
			ID  string `json:"id"`
			Seq int64  `json:"conversationSeq"`
		} `json:"message"`
		Duplicate bool `json:"duplicate"`
	}
	if err := json.Unmarshal(ack.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Message.ID == "" || result.Message.Seq != 1 || result.Duplicate {
		t.Fatalf("ack result=%+v", result)
	}
	request.RequestID = "r2"
	if err := conn.WriteJSON(request); err != nil {
		t.Fatal(err)
	}
	ack = readType(t, conn, "message.ack")
	if err := json.Unmarshal(ack.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Duplicate || result.Message.Seq != 1 {
		t.Fatalf("duplicate ack=%+v", result)
	}
	bobToken := loginToken(t, ts.URL, "13800000002")
	bob, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/v1/ws?ticket="+webSocketTicket(t, ts.URL, bobToken), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bob.Close()
	var bobReady realtime.Envelope
	if err = bob.ReadJSON(&bobReady); err != nil {
		t.Fatal(err)
	}
	callPayload, _ := json.Marshal(map[string]any{"conversationId": conversationID, "callId": "call-1", "mediaType": "video", "sdp": "offer-sdp"})
	if err = conn.WriteJSON(realtime.Envelope{Version: 1, RequestID: "call-r1", Type: "call.offer", Payload: callPayload}); err != nil {
		t.Fatal(err)
	}
	_ = readType(t, conn, "call.offer.ack")
	offer := readType(t, bob, "call.offer")
	var offerBody map[string]any
	if err = json.Unmarshal(offer.Payload, &offerBody); err != nil {
		t.Fatal(err)
	}
	if offerBody["fromUserId"] != "usr_alice" || offerBody["callId"] != "call-1" {
		t.Fatalf("offer=%v", offerBody)
	}
}

func TestHealthProbeBypassesApplicationRateLimit(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	cfg := config.Config{JWTSecret: "test-secret", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	h := New(cfg, a).Handler()
	for i := 0; i < 350; i++ {
		r := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("probe %d returned %d", i, w.Code)
		}
	}
}

func TestMaintenanceModeBlocksUsersButKeepsOperationsReachable(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	if err := a.UpdateSettings("admin", map[string]any{
		"maintenanceMode": true,
		"announcement":    "计划维护至 23:30",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/auth/login", "application/json", strings.NewReader(`{"phone":"13800000001","code":"654321"}`))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(res.Body).Decode(&body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable || body.Error.Code != "MAINTENANCE" || body.Error.Message != "计划维护至 23:30" {
		t.Fatalf("maintenance login status=%d body=%+v", res.StatusCode, body)
	}
	res, err = http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("maintenance health status=%d", res.StatusCode)
	}
	res, err = http.Post(ts.URL+"/v1/admin/auth/login", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode == http.StatusServiceUnavailable {
		t.Fatalf("admin login was blocked by maintenance")
	}
}

func TestCallLifecycleRESTPermissionsAndIdempotency(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour, CallInviteTTL: 30 * time.Second}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	bob := loginToken(t, ts.URL, "13800000002")
	charlie := loginToken(t, ts.URL, "13800000003")
	cid := directConversation(t, ts.URL, alice, "usr_bob")

	inviteBody := fmt.Sprintf(`{"callId":"rest-call-1","conversationId":%q,"calleeUserId":"usr_bob","mediaType":"video"}`, cid)
	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/calls/invite", alice, inviteBody)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("invite status=%d", res.StatusCode)
	}
	var invite struct {
		Call      model.CallSession `json:"call"`
		Duplicate bool              `json:"duplicate"`
	}
	if err := json.NewDecoder(res.Body).Decode(&invite); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if invite.Duplicate || invite.Call.Status != "invited" || invite.Call.CallerID != "usr_alice" || invite.Call.CalleeID != "usr_bob" {
		t.Fatalf("invite=%+v", invite)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/calls/invite", alice, inviteBody)
	var retry struct {
		Duplicate bool `json:"duplicate"`
	}
	_ = json.NewDecoder(res.Body).Decode(&retry)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || !retry.Duplicate {
		t.Fatalf("invite retry status=%d duplicate=%v", res.StatusCode, retry.Duplicate)
	}

	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/calls/rest-call-1/accept", alice, `{}`)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("caller accept status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/calls/rest-call-1/accept", bob, `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("callee accept status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/calls/rest-call-1/accept", bob, `{}`)
	var acceptedRetry struct {
		Duplicate bool `json:"duplicate"`
	}
	_ = json.NewDecoder(res.Body).Decode(&acceptedRetry)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !acceptedRetry.Duplicate {
		t.Fatalf("accept retry status=%d duplicate=%v", res.StatusCode, acceptedRetry.Duplicate)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/calls/rest-call-1/hangup", alice, `{"reason":"completed"}`)
	var ended struct {
		Call model.CallSession `json:"call"`
	}
	_ = json.NewDecoder(res.Body).Decode(&ended)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || ended.Call.Status != "ended" || ended.Call.EndReason != "completed" {
		t.Fatalf("hangup status=%d call=%+v", res.StatusCode, ended.Call)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/calls/rest-call-1", charlie, "")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("non-participant get status=%d", res.StatusCode)
	}
	res.Body.Close()
}

func TestFriendRequestLifecycleMetadataDeleteAndBlock(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	bob := loginToken(t, ts.URL, "13800000002")
	requestBody := `{"userId":"usr_bob","message":"我是邻居","source":"search"}`
	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/friend-requests", alice, requestBody)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("request status=%d", res.StatusCode)
	}
	var first model.FriendRequest
	if err := json.NewDecoder(res.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if first.Source != "search" || first.Status != "pending" || first.Message != "我是邻居" {
		t.Fatalf("request=%+v", first)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/friend-requests", alice, requestBody)
	var retry model.FriendRequest
	_ = json.NewDecoder(res.Body).Decode(&retry)
	res.Body.Close()
	if retry.ID != first.ID {
		t.Fatalf("retry id=%s first=%s", retry.ID, first.ID)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/friend-requests/"+first.ID+"/accept", alice, `{}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("sender accept=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/friend-requests/"+first.ID+"/reject", bob, `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reject=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/friend-requests/"+first.ID+"/reject", bob, `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reject retry=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/friend-requests", alice, requestBody)
	var second model.FriendRequest
	_ = json.NewDecoder(res.Body).Decode(&second)
	res.Body.Close()
	if second.ID == first.ID {
		t.Fatal("new request reused terminal id")
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/friend-requests/"+second.ID+"/accept", bob, `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("accept=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v1/friends/usr_bob", alice, `{"remark":"隔壁小林","tags":["邻居","摄影"]}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("metadata=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/friends", alice, "")
	var friends struct {
		Items []model.User `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&friends)
	res.Body.Close()
	if len(friends.Items) != 1 || friends.Items[0].Remark != "隔壁小林" || len(friends.Items[0].Tags) != 2 {
		t.Fatalf("friends=%+v", friends.Items)
	}
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v1/friends/usr_bob", alice, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete friend=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v1/users/usr_bob/block", alice, `{"blocked":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("block=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/friend-requests", alice, requestBody)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("blocked request=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/users/me/blocks", alice, "")
	var blocked struct {
		Items []model.User `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&blocked)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(blocked.Items) != 1 || blocked.Items[0].ID != "usr_bob" || blocked.Items[0].Phone != "" {
		t.Fatalf("blocked users status=%d items=%+v", res.StatusCode, blocked.Items)
	}
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v1/users/usr_bob/block", alice, `{"blocked":false}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unblock=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/users/me/blocks", alice, "")
	_ = json.NewDecoder(res.Body).Decode(&blocked)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(blocked.Items) != 0 {
		t.Fatalf("blocked users after unblock status=%d items=%+v", res.StatusCode, blocked.Items)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/friend-requests", alice, requestBody)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("unblocked request=%d", res.StatusCode)
	}
	res.Body.Close()
}

func TestConversationPreferencesHideAndReappear(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	aliceToken := loginToken(t, ts.URL, "13800000001")
	cid := directConversation(t, ts.URL, aliceToken, "usr_bob")

	preferenceBody := `{"pinned":true,"notificationsMuted":true,"manualUnread":true}`
	res := authenticatedRequest(t, http.MethodPatch, ts.URL+"/v1/conversations/"+cid+"/preferences", aliceToken, preferenceBody)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("preference status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	list := listConversations(t, ts.URL, aliceToken)
	if len(list) == 0 || !list[0].Membership.Pinned || !list[0].Membership.NotificationsMuted || !list[0].Membership.ManualUnread {
		t.Fatalf("preferences missing from list: %+v", list)
	}

	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v1/conversations/"+cid+"/read", aliceToken, `{"seq":0}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("read status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	list = listConversations(t, ts.URL, aliceToken)
	if len(list) == 0 || list[0].Membership.ManualUnread {
		t.Fatalf("mark read did not clear manual unread: %+v", list)
	}

	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v1/conversations/"+cid, aliceToken, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("hide status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	if list = listConversations(t, ts.URL, aliceToken); len(list) != 0 {
		t.Fatalf("hidden conversation remains visible: %+v", list)
	}
	if _, _, err := a.SendMessage("usr_bob", cid, "reappear-1", "text", map[string]any{"text": "new message"}, ""); err != nil {
		t.Fatal(err)
	}
	list = listConversations(t, ts.URL, aliceToken)
	if len(list) != 1 || list[0].Conversation.ID != cid || list[0].Membership.Pinned {
		t.Fatalf("conversation did not reappear cleanly: %+v", list)
	}
}

func TestUserProfilePhoneDevicesFavoritesAndFeedback(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	_ = a.CreateMedia(store.Media{ID: "avatar-alice", OwnerID: "usr_alice", ObjectKey: "a", MIME: "image/png", Status: "ready", Size: 12})
	_ = a.CreateMedia(store.Media{ID: "avatar-bob", OwnerID: "usr_bob", ObjectKey: "b", MIME: "image/png", Status: "ready", Size: 12})
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	token := loginToken(t, ts.URL, "13800000001")

	res := authenticatedRequest(t, http.MethodPatch, ts.URL+"/v1/users/me", token, `{"name":"Alice Chen","handle":"alice_2026","signature":"Hello","avatarMediaId":"avatar-alice"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("profile status=%d", res.StatusCode)
	}
	var profile model.User
	if err := json.NewDecoder(res.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if profile.Handle != "alice_2026" || profile.AvatarMediaID != "avatar-alice" || !strings.HasPrefix(profile.AvatarURL, "/v1/avatars/avatar-alice?") {
		t.Fatalf("profile=%+v", profile)
	}
	avatarResponse, err := http.Get(ts.URL + profile.AvatarURL)
	if err != nil {
		t.Fatal(err)
	}
	if avatarResponse.StatusCode == http.StatusForbidden {
		t.Fatalf("signed avatar URL was rejected: %s", profile.AvatarURL)
	}
	_ = avatarResponse.Body.Close()
	invalidAvatar, _ := http.Get(ts.URL + "/v1/avatars/avatar-alice?expires=1&signature=invalid")
	if invalidAvatar.StatusCode != http.StatusForbidden {
		t.Fatalf("invalid avatar status=%d", invalidAvatar.StatusCode)
	}
	_ = invalidAvatar.Body.Close()
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v1/users/me", token, `{"avatarMediaId":"avatar-bob"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign avatar status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	bobAvatarID := "avatar-bob"
	bobHandle := "bob_avatar"
	if _, err = a.UpdateUserProfile("usr_bob", store.UserProfileUpdate{Handle: &bobHandle, AvatarMediaID: &bobAvatarID}); err != nil {
		t.Fatal(err)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/users/search?q=bob_avatar&by=handle", token, "")
	var search struct {
		Items []model.User `json:"items"`
	}
	if err = json.NewDecoder(res.Body).Decode(&search); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || len(search.Items) != 1 || !strings.HasPrefix(search.Items[0].AvatarURL, "/v1/avatars/avatar-bob?") {
		t.Fatalf("search avatar status=%d items=%+v", res.StatusCode, search.Items)
	}

	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/users/me/phone/code", token, `{"phone":"13900000001"}`)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("phone code status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v1/users/me/phone", token, `{"phone":"13900000001","code":"654321"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("phone update status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/devices", token, `{"deviceId":"device-1","platform":"ios","provider":"apns","pushToken":"secret-token","notificationsEnabled":true,"previewEnabled":false,"soundEnabled":false,"vibrationEnabled":true}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("device register status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/users/me/devices", token, "")
	var devices struct {
		Items []model.Device `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&devices); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if len(devices.Items) != 1 || devices.Items[0].PushToken != "" || !devices.Items[0].NotificationsEnabled || devices.Items[0].PreviewEnabled || devices.Items[0].SoundEnabled || !devices.Items[0].VibrationEnabled {
		t.Fatalf("devices leak or mismatch: %+v", devices.Items)
	}
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v1/users/me/devices/device-1", token, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("device delete status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/users/me/favorites", token, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("favorites status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/feedback", token, `{"category":"product","content":"Please add dark mode","contact":"alice@example.com"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("feedback status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestForwardMessagesSeparateMergedAndIdempotent(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	cid, err := a.DirectConversation("usr_alice", "usr_bob")
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := a.SendMessage("usr_alice", cid.ID, "source-1", "text", map[string]any{"text": "hello"}, "")
	second, _, _ := a.SendMessage("usr_bob", cid.ID, "source-2", "text", map[string]any{"text": "world"}, "")
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	token := loginToken(t, ts.URL, "13800000001")

	separate := fmt.Sprintf(`{"sourceMessageIds":[%q,%q],"mode":"separate","clientBatchId":"batch-one"}`, first.ID, second.ID)
	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/"+cid.ID+"/forward", token, separate)
	var forwarded struct {
		Messages  []model.Message `json:"messages"`
		Duplicate bool            `json:"duplicate"`
	}
	if err = json.NewDecoder(res.Body).Decode(&forwarded); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || forwarded.Duplicate || len(forwarded.Messages) != 2 || forwarded.Messages[0].SenderID != "usr_alice" || forwarded.Messages[0].Body["sourceMessageId"] != first.ID {
		t.Fatalf("separate forward=%+v status=%d", forwarded, res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/"+cid.ID+"/forward", token, separate)
	forwarded = struct {
		Messages  []model.Message `json:"messages"`
		Duplicate bool            `json:"duplicate"`
	}{}
	_ = json.NewDecoder(res.Body).Decode(&forwarded)
	_ = res.Body.Close()
	if !forwarded.Duplicate || len(forwarded.Messages) != 2 {
		t.Fatalf("separate retry=%+v", forwarded)
	}

	merged := fmt.Sprintf(`{"sourceMessageIds":[%q,%q],"mode":"merged","clientBatchId":"batch-two"}`, first.ID, second.ID)
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/"+cid.ID+"/forward", token, merged)
	_ = json.NewDecoder(res.Body).Decode(&forwarded)
	_ = res.Body.Close()
	if len(forwarded.Messages) != 1 || forwarded.Messages[0].Type != "chat_history" || forwarded.Messages[0].SenderID != "usr_alice" {
		t.Fatalf("merged forward=%+v", forwarded)
	}
	if _, exists := forwarded.Messages[0].Body["sdp"]; exists {
		t.Fatal("merged forward contains untrusted transport data")
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/"+cid.ID+"/forward", token, `{"sourceMessageIds":["missing"],"mode":"merged","clientBatchId":"batch-missing"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("inaccessible source status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestPasswordRegistrationLoginAndReset(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	cfg := config.Config{JWTSecret: "test-secret-password-auth-32-bytes", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	phone := "13912345678"

	registerBody := fmt.Sprintf(`{"phone":%q,"code":"654321","password":"StrongPass123!","name":"New User"}`, phone)
	res, err := http.Post(ts.URL+"/v1/auth/register", "application/json", strings.NewReader(registerBody))
	if err != nil {
		t.Fatal(err)
	}
	var registered struct{ AccessToken, RefreshToken string }
	_ = json.NewDecoder(res.Body).Decode(&registered)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || registered.AccessToken == "" || registered.RefreshToken == "" {
		t.Fatalf("register status=%d response=%+v", res.StatusCode, registered)
	}

	wrong, _ := http.Post(ts.URL+"/v1/auth/password-login", "application/json", strings.NewReader(fmt.Sprintf(`{"phone":%q,"password":"wrong-pass"}`, phone)))
	if wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d", wrong.StatusCode)
	}
	_ = wrong.Body.Close()
	unknown, _ := http.Post(ts.URL+"/v1/auth/password-login", "application/json", strings.NewReader(`{"phone":"13900009999","password":"wrong-pass"}`))
	if unknown.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown account enumeration status=%d", unknown.StatusCode)
	}
	_ = unknown.Body.Close()
	correct, _ := http.Post(ts.URL+"/v1/auth/password-login", "application/json", strings.NewReader(fmt.Sprintf(`{"phone":%q,"password":"StrongPass123!"}`, phone)))
	if correct.StatusCode != http.StatusOK {
		t.Fatalf("password login status=%d", correct.StatusCode)
	}
	_ = correct.Body.Close()

	resetCode, _ := http.Post(ts.URL+"/v1/auth/password/reset-code", "application/json", strings.NewReader(fmt.Sprintf(`{"phone":%q}`, phone)))
	if resetCode.StatusCode != http.StatusAccepted {
		t.Fatalf("reset code status=%d", resetCode.StatusCode)
	}
	_ = resetCode.Body.Close()
	reset, _ := http.Post(ts.URL+"/v1/auth/password/reset", "application/json", strings.NewReader(fmt.Sprintf(`{"phone":%q,"code":"654321","password":"ChangedPass456!"}`, phone)))
	if reset.StatusCode != http.StatusNoContent {
		t.Fatalf("reset status=%d", reset.StatusCode)
	}
	_ = reset.Body.Close()
	oldPassword, _ := http.Post(ts.URL+"/v1/auth/password-login", "application/json", strings.NewReader(fmt.Sprintf(`{"phone":%q,"password":"StrongPass123!"}`, phone)))
	if oldPassword.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password status=%d", oldPassword.StatusCode)
	}
	_ = oldPassword.Body.Close()
	newPassword, _ := http.Post(ts.URL+"/v1/auth/password-login", "application/json", strings.NewReader(fmt.Sprintf(`{"phone":%q,"password":"ChangedPass456!"}`, phone)))
	if newPassword.StatusCode != http.StatusOK {
		t.Fatalf("new password status=%d", newPassword.StatusCode)
	}
	_ = newPassword.Body.Close()
}

func TestAnnouncementLifecycleTargetingAndReadReceipt(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	hash, _ := bcrypt.GenerateFromPassword([]byte("announcement-admin-password"), 12)
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour, AdminID: "admin", AdminEmail: "admin@example.com", AdminPasswordHash: string(hash), AdminRole: "platform_admin"}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	userToken := loginToken(t, ts.URL, "13800000001")
	adminBody := `{"email":"admin@example.com","password":"announcement-admin-password"}`
	res, _ := http.Post(ts.URL+"/v1/admin/auth/login", "application/json", strings.NewReader(adminBody))
	var adminSession struct {
		AccessToken string `json:"accessToken"`
	}
	_ = json.NewDecoder(res.Body).Decode(&adminSession)
	_ = res.Body.Close()
	if adminSession.AccessToken == "" {
		t.Fatal("missing admin token")
	}

	create := `{"title":"Targeted update","content":"Only Alice should see this","status":"draft","pinned":true,"targetType":"users","targetUserIds":["usr_alice"],"pushOnPublish":false}`
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/admin/announcements", adminSession.AccessToken, create)
	var item model.Announcement
	_ = json.NewDecoder(res.Body).Decode(&item)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || item.ID == "" {
		t.Fatalf("create status=%d item=%+v", res.StatusCode, item)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/announcements", userToken, "")
	var list struct {
		Items []model.Announcement `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&list)
	_ = res.Body.Close()
	if len(list.Items) != 0 {
		t.Fatalf("draft leaked: %+v", list.Items)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/admin/announcements/"+item.ID+"/publish", adminSession.AccessToken, `{"enqueuePush":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("publish status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/announcements", userToken, "")
	list.Items = nil
	_ = json.NewDecoder(res.Body).Decode(&list)
	_ = res.Body.Close()
	if len(list.Items) != 1 || !list.Items[0].Pinned {
		t.Fatalf("published list=%+v", list.Items)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/announcements/"+item.ID+"/read", userToken, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("read status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/announcements", userToken, "")
	list.Items = nil
	_ = json.NewDecoder(res.Body).Decode(&list)
	_ = res.Body.Close()
	if len(list.Items) != 1 || list.Items[0].ReadAt == nil {
		t.Fatalf("read receipt missing: %+v", list.Items)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/admin/announcements/"+item.ID+"/withdraw", adminSession.AccessToken, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("withdraw status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/announcements", userToken, "")
	list.Items = nil
	_ = json.NewDecoder(res.Body).Decode(&list)
	_ = res.Body.Close()
	if len(list.Items) != 0 {
		t.Fatalf("withdrawn leaked: %+v", list.Items)
	}
}

func TestAdminRuntimeSettingsValidateAuditAndNeverExposeSecrets(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	hash, _ := bcrypt.GenerateFromPassword([]byte("settings-admin-password"), 12)
	cfg := config.Config{JWTSecret: strings.Repeat("s", 32), DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, MediaMaxBytes: 25 << 20, AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour, CallInviteTTL: 45 * time.Second, AdminID: "admin", AdminEmail: "admin@example.com", AdminPasswordHash: string(hash), AdminRole: "platform_admin", DatabaseURL: "postgres://secret-database", RedisURL: "redis://secret-redis", S3Endpoint: "storage", S3AccessKey: "secret-access", S3SecretKey: "secret-storage", GetuiAppID: "app", GetuiAppKey: "key", GetuiMasterSecret: "secret-getui-master", PushProvider: "getui", RTCTURNURLs: []string{"turn:example"}, RTCTURNUsername: "turn-user", RTCTURNCredential: "secret-turn-credential", AdminTOTPSecret: ""}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	login, _ := http.Post(ts.URL+"/v1/admin/auth/login", "application/json", strings.NewReader(`{"email":"admin@example.com","password":"settings-admin-password"}`))
	var session struct {
		AccessToken string `json:"accessToken"`
	}
	_ = json.NewDecoder(login.Body).Decode(&session)
	_ = login.Body.Close()
	if session.AccessToken == "" {
		t.Fatalf("admin login status=%d", login.StatusCode)
	}
	res := authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/admin/settings", session.AccessToken, "")
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	for _, secret := range []string{"secret-database", "secret-redis", "secret-access", "secret-storage", "secret-getui-master", "secret-turn-credential"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("settings response leaked secret %q: %s", secret, raw)
		}
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	status := settings["configurationStatus"].(map[string]any)
	infra := settings["infrastructure"].(map[string]any)
	if status["pushProvider"] != true || status["turn"] != true || infra["mediaMaxSizeMB"] != float64(25) || infra["callInviteTimeoutSeconds"] != float64(45) {
		t.Fatalf("status=%v infrastructure=%v", status, infra)
	}
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v1/admin/settings", session.AccessToken, `{"passwordMinLength":12,"maxMessageTextLength":8000,"messageRecallMinutes":5,"maxGroupMembers":1000,"friendRequestExpiryDays":10,"allowFriendRequests":false,"allowSearchByHandle":true,"allowSearchByPhone":true,"announcementPushEnabled":false,"callsEnabled":true,"videoCallsEnabled":false,"sensitiveWordEnabled":true,"reportSlaHours":12}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update settings status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	updated := a.Settings()
	if updated["passwordMinLength"] != float64(12) || updated["allowFriendRequests"] != false || updated["allowSearchByHandle"] != true || updated["allowSearchByPhone"] != true || updated["videoCallsEnabled"] != false {
		t.Fatalf("updated settings=%v", updated)
	}
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v1/admin/settings", session.AccessToken, `{"messageRecallMinutes":0}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid setting status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}

type conversationListItem struct {
	Conversation struct {
		ID string `json:"id"`
	} `json:"conversation"`
	Membership struct {
		Pinned             bool `json:"pinned"`
		NotificationsMuted bool `json:"notificationsMuted"`
		ManualUnread       bool `json:"manualUnread"`
	} `json:"membership"`
}

func listConversations(t *testing.T, base, token string) []conversationListItem {
	t.Helper()
	res := authenticatedRequest(t, http.MethodGet, base+"/v1/conversations", token, "")
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list status=%d", res.StatusCode)
	}
	var payload struct {
		Items []conversationListItem `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload.Items
}

func authenticatedRequest(t *testing.T, method, url, token, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRefreshRotationLogoutAndAdminRBAC(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery staple"), bcrypt.MinCost)
	cfg := config.Config{JWTSecret: strings.Repeat("s", 32), DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour, AdminID: "root-admin", AdminEmail: "admin@example.com", AdminPasswordHash: string(hash), AdminRole: "platform_admin"}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	body, _ := json.Marshal(map[string]string{"phone": "13800000001", "code": "654321"})
	res, _ := http.Post(ts.URL+"/v1/auth/login", "application/json", bytes.NewReader(body))
	var first struct{ AccessToken, RefreshToken string }
	_ = json.NewDecoder(res.Body).Decode(&first)
	_ = res.Body.Close()
	raw, _ := json.Marshal(map[string]string{"refreshToken": first.RefreshToken})
	res, _ = http.Post(ts.URL+"/v1/auth/refresh", "application/json", bytes.NewReader(raw))
	var second struct{ AccessToken, RefreshToken string }
	_ = json.NewDecoder(res.Body).Decode(&second)
	_ = res.Body.Close()
	if second.RefreshToken == "" {
		t.Fatal("refresh rotation failed")
	}
	res, _ = http.Post(ts.URL+"/v1/auth/refresh", "application/json", bytes.NewReader(raw))
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused refresh status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	logoutRaw, _ := json.Marshal(map[string]string{"refreshToken": second.RefreshToken})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/auth/logout", bytes.NewReader(logoutRaw))
	req.Header.Set("authorization", "Bearer "+second.AccessToken)
	req.Header.Set("content-type", "application/json")
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res, _ = http.Post(ts.URL+"/v1/auth/refresh", "application/json", bytes.NewReader(logoutRaw))
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("logged out refresh status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	adminRaw, _ := json.Marshal(map[string]string{"email": "admin@example.com", "password": "correct horse battery staple"})
	res, _ = http.Post(ts.URL+"/v1/admin/auth/login", "application/json", bytes.NewReader(adminRaw))
	var admin struct{ AccessToken string }
	_ = json.NewDecoder(res.Body).Decode(&admin)
	_ = res.Body.Close()
	if admin.AccessToken == "" {
		t.Fatal("admin login failed")
	}
	support, _ := New(cfg, a).auth.IssueAdmin("support-1", "support", time.Hour)
	banReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/admin/users/usr_bob/ban", strings.NewReader(`{"reason":"x"}`))
	banReq.Header.Set("authorization", "Bearer "+support)
	banReq.Header.Set("content-type", "application/json")
	res, _ = http.DefaultClient.Do(banReq)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("support mutation status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/admin/users/usr_bob/ban", admin.AccessToken, `{}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("ban without reason status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/admin/users/usr_bob/ban", admin.AccessToken, `{"reason":"abuse investigation","durationHours":24}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("timed ban status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestAuthenticationDoesNotFallbackAndBanBlocksRefreshAndWebSocket(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: strings.Repeat("s", 32), AdminKey: strings.Repeat("a", 24), DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()

	bad, _ := json.Marshal(map[string]string{"phone": "13800000001", "code": "000000"})
	res, err := http.Post(ts.URL+"/v1/auth/login", "application/json", bytes.NewReader(bad))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad OTP status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	body, _ := json.Marshal(map[string]string{"phone": "13800000001", "code": "654321"})
	res, err = http.Post(ts.URL+"/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var tokens struct{ AccessToken, RefreshToken string }
	if err = json.NewDecoder(res.Body).Decode(&tokens); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	ticket := webSocketTicket(t, ts.URL, tokens.AccessToken)
	if err = a.AdminBan("admin", "usr_alice", true, 24, "test"); err != nil {
		t.Fatal(err)
	}

	refreshBody, _ := json.Marshal(map[string]string{"refreshToken": tokens.RefreshToken})
	res, err = http.Post(ts.URL+"/v1/auth/refresh", "application/json", bytes.NewReader(refreshBody))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("banned refresh status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/ws?ticket=" + ticket
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("banned ws err=%v response=%v", err, response)
	}
}

func TestAccessTokensAreRejectedFromURLsAndWebSocketUpgrade(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: strings.Repeat("s", 32), DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	token := loginToken(t, ts.URL, "13800000001")

	res, err := http.Get(ts.URL + "/v1/me?token=" + url.QueryEscape(token))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("REST query token status=%d", res.StatusCode)
	}

	header := http.Header{"Authorization": []string{"Bearer " + token}}
	conn, response, dialErr := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/v1/ws", header)
	if conn != nil {
		_ = conn.Close()
	}
	if dialErr == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("websocket access token err=%v response=%v", dialErr, response)
	}
}

func readType(t *testing.T, conn *websocket.Conn, typ string) realtime.Envelope {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 10; i++ {
		var env realtime.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			t.Fatal(err)
		}
		if env.Type == typ {
			return env
		}
	}
	t.Fatalf("did not receive %s", typ)
	return realtime.Envelope{}
}
func loginToken(t *testing.T, base, phone string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"phone": phone, "code": "654321"})
	res, err := http.Post(base+"/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.AccessToken == "" {
		t.Fatal("missing access token")
	}
	return out.AccessToken
}

func webSocketTicket(t *testing.T, base, accessToken string) string {
	t.Helper()
	res := authenticatedRequest(t, http.MethodPost, base+"/v1/ws/ticket", accessToken, "")
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("websocket ticket status=%d body=%s", res.StatusCode, raw)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("websocket ticket cache control=%q", res.Header.Get("Cache-Control"))
	}
	var response struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil || response.Ticket == "" {
		t.Fatalf("websocket ticket decode=%v ticket=%q", err, response.Ticket)
	}
	return response.Ticket
}
func TestProfileHandleSearchCapabilitiesAndGroupMemberContract(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	bob := loginToken(t, ts.URL, "13800000002")
	res := authenticatedRequest(t, http.MethodPatch, ts.URL+"/v1/users/me", alice, `{"handle":"official_support"}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("reserved handle status=%d", res.StatusCode)
	}
	res.Body.Close()

	for i, handle := range []string{"alice_one", "alice_two"} {
		res := authenticatedRequest(t, http.MethodPatch, ts.URL+"/v1/users/me", alice, `{"handle":"`+handle+`"}`)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("handle update %d status=%d", i+1, res.StatusCode)
		}
		var profile model.User
		_ = json.NewDecoder(res.Body).Decode(&profile)
		res.Body.Close()
		if profile.HandleChangeCount != i+1 || profile.HandleChangesRemaining != 1-i || !profile.AllowSearchByHandle || profile.AllowSearchByPhone {
			t.Fatalf("profile after update %d: %+v", i+1, profile)
		}
	}
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v1/users/me", alice, `{"handle":"alice_three"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("third handle update status=%d", res.StatusCode)
	}
	res.Body.Close()

	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/users/search/capabilities", alice, "")
	var caps map[string]bool
	_ = json.NewDecoder(res.Body).Decode(&caps)
	res.Body.Close()
	if !caps["allowSearchByHandle"] || caps["allowSearchByPhone"] {
		t.Fatalf("capabilities=%v", caps)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/users/search?q=13800000002&by=phone", alice, "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("disabled phone search status=%d", res.StatusCode)
	}
	res.Body.Close()

	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v1/users/me", bob, `{"handle":"bob_public"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bob handle update status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/users/search?q=bob_public&by=handle", alice, "")
	var search struct {
		Items []model.User `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&search)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(search.Items) != 1 || search.Items[0].Phone != "" || search.Items[0].Handle != "bob_public" {
		t.Fatalf("handle search status=%d items=%+v", res.StatusCode, search.Items)
	}

	group, err := a.CreateGroup("usr_alice", "Neighbors", []string{"usr_bob"})
	if err != nil {
		t.Fatal(err)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/groups/"+group.ID+"/members", alice, "")
	var members struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&members)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(members.Items) != 2 {
		t.Fatalf("group members status=%d items=%v", res.StatusCode, members.Items)
	}
	for _, member := range members.Items {
		if member["name"] == nil || member["handle"] == nil || member["phone"] != nil {
			t.Fatalf("unsafe/incomplete member=%v", member)
		}
	}
	directConversation(t, ts.URL, alice, "usr_bob")
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/conversations", alice, "")
	var conversations struct {
		Items []struct {
			Conversation model.Conversation `json:"conversation"`
			Members      []map[string]any   `json:"members"`
		} `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&conversations)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(conversations.Items) != 2 {
		t.Fatalf("conversation list status=%d items=%+v", res.StatusCode, conversations.Items)
	}
	for _, item := range conversations.Items {
		if len(item.Members) != 2 {
			t.Fatalf("conversation %s members=%v", item.Conversation.ID, item.Members)
		}
		for _, member := range item.Members {
			if member["id"] == nil || member["userId"] == nil || member["id"] != member["userId"] || member["name"] == nil || member["handle"] == nil || member["phone"] != nil {
				t.Fatalf("unsafe conversation member=%v", member)
			}
		}
	}
}

func TestMediaAuthorizationAndResponseOnlySignedURLs(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	mediaID := "med_secure"
	if err := a.CreateMedia(store.Media{ID: mediaID, OwnerID: "usr_alice", ObjectKey: "users/alice/media.jpg", MIME: "image/jpeg", Size: 10, Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := a.CompleteMedia(mediaID, "usr_alice", 10, "checksum"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	api := New(cfg, a)
	api.media = signedMediaService{}
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()
	alice, bob, outsider := loginToken(t, ts.URL, "13800000001"), loginToken(t, ts.URL, "13800000002"), loginToken(t, ts.URL, "13800000000")
	cid := directConversation(t, ts.URL, alice, "usr_bob")
	sendURL := ts.URL + "/v1/conversations/" + cid + "/messages"
	res := authenticatedRequest(t, http.MethodPost, sendURL, alice, `{"clientMsgId":"audio-mismatch","type":"audio","body":{"mediaId":"med_secure"}}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("media type mismatch status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, sendURL, alice, `{"clientMsgId":"media-extra","type":"image","body":{"mediaId":"med_secure","phone":"13800000000"}}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("media unexpected metadata status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, sendURL, alice, `{"clientMsgId":"image-1","type":"image","body":{"mediaId":"med_secure","mime":"application/x-forged","size":999,"checksum":"forged"}}`)
	var sent struct {
		Message model.Message `json:"message"`
	}
	_ = json.NewDecoder(res.Body).Decode(&sent)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(fmt.Sprint(sent.Message.Body["downloadUrl"]), "X-Amz-Signature") || sent.Message.Body["mime"] != "image/jpeg" || sent.Message.Body["size"] != float64(10) || sent.Message.Body["checksum"] != "checksum" {
		t.Fatalf("send status=%d body=%v", res.StatusCode, sent.Message.Body)
	}
	persisted, err := a.History("usr_alice", cid, 0, 10)
	if err != nil || len(persisted) != 1 || persisted[0].Body["downloadUrl"] != nil {
		t.Fatalf("signed url persisted: items=%+v err=%v", persisted, err)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/conversations/"+cid+"/messages", bob, "")
	var history struct {
		Items []model.Message `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&history)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(history.Items) != 1 || history.Items[0].Body["downloadUrl"] == nil {
		t.Fatalf("history status=%d items=%+v", res.StatusCode, history.Items)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/sync?after=0", bob, "")
	var syncResponse struct {
		Events []model.SyncEvent `json:"events"`
	}
	_ = json.NewDecoder(res.Body).Decode(&syncResponse)
	res.Body.Close()
	foundSigned := false
	for _, event := range syncResponse.Events {
		if message, ok := event.Payload["message"].(map[string]any); ok {
			if body, ok := message["body"].(map[string]any); ok && body["downloadUrl"] != nil {
				foundSigned = true
			}
		}
	}
	if !foundSigned {
		t.Fatalf("sync missing signed media: %+v", syncResponse.Events)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for name, token := range map[string]string{"owner": alice, "member": bob} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/media/"+mediaID, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err = client.Do(req)
		if err != nil || res.StatusCode != http.StatusTemporaryRedirect || !strings.Contains(res.Header.Get("Location"), "X-Amz-Signature") {
			t.Fatalf("%s download status=%d location=%q err=%v", name, res.StatusCode, res.Header.Get("Location"), err)
		}
		res.Body.Close()
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/media/"+mediaID, nil)
	req.Header.Set("Authorization", "Bearer "+outsider)
	res, err = client.Do(req)
	if err != nil || res.StatusCode != http.StatusNotFound {
		t.Fatalf("outsider status=%d err=%v", res.StatusCode, err)
	}
	res.Body.Close()
}

func TestAccountDeletionRequiresOTPAndResolvedGroupOwnership(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	group, err := a.CreateGroup("usr_alice", "Owned Group", []string{"usr_bob"})
	if err != nil {
		t.Fatal(err)
	}
	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/users/me/deletion/code", alice, `{}`)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("deletion code status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v1/users/me", alice, `{"code":"000000"}`)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid deletion code status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v1/users/me", alice, `{"code":"654321"}`)
	var conflictBody map[string]any
	_ = json.NewDecoder(res.Body).Decode(&conflictBody)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict || !strings.Contains(fmt.Sprint(conflictBody), "GROUP_OWNERSHIP_REQUIRED") {
		t.Fatalf("ownership conflict status=%d body=%v", res.StatusCode, conflictBody)
	}
	if err = a.DisbandGroup("usr_alice", group.ID, "account deletion"); err != nil {
		t.Fatal(err)
	}
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v1/users/me", alice, `{"code":"654321"}`)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d", res.StatusCode)
	}
	res.Body.Close()
	deleted, err := a.User("usr_alice")
	if err != nil || !deleted.Banned || deleted.DeletedAt == nil || deleted.Phone == "13800000001" || deleted.Handle == "alice_two" || deleted.Name != "已注销用户" {
		t.Fatalf("deleted profile=%+v err=%v", deleted, err)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/users/me", alice, "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("deleted account still active status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v1/users/me", alice, `{}`)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("idempotent delete status=%d", res.StatusCode)
	}
	res.Body.Close()
}

func TestContactAndLocationMessageContracts(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	cid := directConversation(t, ts.URL, alice, "usr_bob")
	sendURL := ts.URL + "/v1/conversations/" + cid + "/messages"

	res := authenticatedRequest(t, http.MethodPost, sendURL, alice, `{"clientMsgId":"contact-bad","type":"contact","body":{"userId":"usr_bob","phone":"13800000002"}}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("contact sensitive field status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, sendURL, alice, `{"clientMsgId":"contact-ok","type":"contact","body":{"userId":"usr_bob"}}`)
	var contact struct {
		Message model.Message `json:"message"`
	}
	_ = json.NewDecoder(res.Body).Decode(&contact)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || contact.Message.Body["userId"] != "usr_bob" || contact.Message.Body["name"] != "Bob" || contact.Message.Body["phone"] != nil || len(contact.Message.Body) != 4 {
		t.Fatalf("contact status=%d body=%v", res.StatusCode, contact.Message.Body)
	}

	res = authenticatedRequest(t, http.MethodPost, sendURL, alice, `{"clientMsgId":"location-bad","type":"location","body":{"latitude":91,"longitude":116.4,"name":"Home","address":"Private","phone":"forbidden"}}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid location status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, sendURL, alice, `{"clientMsgId":"location-ok","type":"location","body":{"latitude":39.9,"longitude":116.4,"name":" Community Gate ","address":" Road 1 "}}`)
	var location struct {
		Message model.Message `json:"message"`
	}
	_ = json.NewDecoder(res.Body).Decode(&location)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || location.Message.Body["name"] != "Community Gate" || location.Message.Body["address"] != "Road 1" || len(location.Message.Body) != 4 {
		t.Fatalf("location status=%d body=%v", res.StatusCode, location.Message.Body)
	}
}

func TestGroupManagementHTTPContractPostgres(t *testing.T) {
	databaseURL := os.Getenv("IM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := store.NewPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	a, err := app.New(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	phones := []string{"181" + suffix, "182" + suffix, "183" + suffix}
	users := make([]*model.User, 3)
	for i := range phones {
		users[i], err = a.Login(phones[i], fmt.Sprintf("Group User %d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	ownerToken, memberToken, inviteeToken := loginToken(t, ts.URL, phones[0]), loginToken(t, ts.URL, phones[1]), loginToken(t, ts.URL, phones[2])

	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/groups", ownerToken, fmt.Sprintf(`{"name":"API Group","memberIds":[%q]}`, users[1].ID))
	var group model.Conversation
	_ = json.NewDecoder(res.Body).Decode(&group)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || group.ID == "" {
		t.Fatalf("create group status=%d group=%+v", res.StatusCode, group)
	}
	avatarID := "api_group_avatar_" + suffix
	if err = p.CreateMedia(ctx, store.Media{ID: avatarID, OwnerID: users[0].ID, ObjectKey: "objects/" + avatarID, MIME: "image/png", Size: 96, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v1/groups/"+group.ID, memberToken, `{"name":"Denied"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("member profile update status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v1/groups/"+group.ID, ownerToken, fmt.Sprintf(`{"name":"Renamed API Group","avatarMediaId":%q,"joinPolicy":"qr","allowMemberAddFriend":false,"rotateQR":true}`, avatarID))
	var profile model.GroupProfile
	_ = json.NewDecoder(res.Body).Decode(&profile)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || profile.QRToken == "" || profile.AllowMemberAddFriend || strings.Contains(profile.QRToken, phones[0]) || !strings.HasPrefix(profile.AvatarURL, "/v1/avatars/"+avatarID+"?") {
		t.Fatalf("profile status=%d value=%+v", res.StatusCode, profile)
	}
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v1/groups/"+group.ID+"/announcement", ownerToken, `{"content":"Welcome"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("announcement status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/groups/"+group.ID+"/invites", ownerToken, fmt.Sprintf(`{"userId":%q}`, users[2].ID))
	var inviteResponse struct {
		Invite model.GroupInvite `json:"invite"`
	}
	_ = json.NewDecoder(res.Body).Decode(&inviteResponse)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || inviteResponse.Invite.ID == "" {
		t.Fatalf("invite status=%d value=%+v", res.StatusCode, inviteResponse)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/group-invites?status=pending", inviteeToken, "")
	var inviteList struct {
		Items []struct {
			Invite    model.GroupInvite `json:"invite"`
			GroupName string            `json:"groupName"`
			Inviter   model.User        `json:"inviter"`
			Outgoing  bool              `json:"outgoing"`
		} `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&inviteList)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(inviteList.Items) != 1 || inviteList.Items[0].Invite.ID != inviteResponse.Invite.ID || inviteList.Items[0].GroupName != "Renamed API Group" || inviteList.Items[0].Inviter.ID != users[0].ID || inviteList.Items[0].Inviter.Phone != "" || inviteList.Items[0].Outgoing {
		t.Fatalf("group invites status=%d items=%+v", res.StatusCode, inviteList.Items)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/group-invites/"+inviteResponse.Invite.ID+"/accept", inviteeToken, `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("accept status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/groups/"+group.ID+"/members", inviteeToken, "")
	var members struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&members)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(members.Items) != 3 {
		t.Fatalf("members status=%d value=%v", res.StatusCode, members.Items)
	}
	for _, member := range members.Items {
		if member["name"] == nil || member["handle"] == nil || member["phone"] != nil {
			t.Fatalf("unsafe member projection=%v", member)
		}
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/conversations/"+group.ID+"/messages", ownerToken, "")
	var history struct {
		Items []model.Message `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&history)
	res.Body.Close()
	foundSystem := false
	for _, message := range history.Items {
		foundSystem = foundSystem || message.Type == "system"
	}
	if !foundSystem {
		t.Fatalf("missing persistent operation system message: %+v", history.Items)
	}
}

func directConversation(t *testing.T, base, token, other string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"userId": other})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/conversations/direct", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.ID == "" {
		t.Fatalf("missing conversation id status=%d", res.StatusCode)
	}
	return out.ID
}

func TestMessageCollaborationRESTContracts(t *testing.T) {
	a, _ := app.New(context.Background(), store.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", WSMaxPerUser: 5, WSMaxPerIP: 20, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	alice, bob := loginToken(t, ts.URL, "13800000001"), loginToken(t, ts.URL, "13800000002")
	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/groups", alice, `{"name":"Collaboration","memberIds":["usr_bob"]}`)
	var group model.Conversation
	_ = json.NewDecoder(res.Body).Decode(&group)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || group.ID == "" {
		t.Fatalf("create group status=%d group=%+v", res.StatusCode, group)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/"+group.ID+"/messages", alice, `{"clientMsgId":"collab-1","type":"text","body":{"text":"first","mentions":["usr_bob"]}}`)
	var sent struct {
		Message model.Message `json:"message"`
	}
	_ = json.NewDecoder(res.Body).Decode(&sent)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || sent.Message.ID == "" {
		t.Fatalf("send status=%d message=%+v", res.StatusCode, sent.Message)
	}
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v1/messages/"+sent.Message.ID, alice, `{"text":"final searchable","mentions":["usr_bob"]}`)
	var edited struct {
		Message   model.Message `json:"message"`
		Duplicate bool          `json:"duplicate"`
	}
	_ = json.NewDecoder(res.Body).Decode(&edited)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || edited.Duplicate || edited.Message.EditVersion != 1 {
		t.Fatalf("edit status=%d value=%+v", res.StatusCode, edited)
	}
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v1/messages/"+sent.Message.ID, bob, `{"editId":"hijack","text":"bad"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("non-author edit status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v1/messages/"+sent.Message.ID+"/reactions/%F0%9F%91%8D", bob, "")
	var reacted struct {
		Message model.Message `json:"message"`
	}
	_ = json.NewDecoder(res.Body).Decode(&reacted)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(reacted.Message.Reactions) != 1 || reacted.Message.Reactions[0].Count != 1 || !reacted.Message.Reactions[0].ReactedByMe {
		t.Fatalf("reaction status=%d value=%+v", res.StatusCode, reacted)
	}
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v1/conversations/"+group.ID+"/pinned-messages/"+sent.Message.ID, bob, "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("member pin status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v1/conversations/"+group.ID+"/pinned-messages/"+sent.Message.ID, alice, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("owner pin status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/conversations/"+group.ID+"/pinned-messages", bob, "")
	var pins struct {
		Items []model.MessagePin `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&pins)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(pins.Items) != 1 || pins.Items[0].Message.ID != sent.Message.ID {
		t.Fatalf("pins status=%d items=%+v", res.StatusCode, pins.Items)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/conversations/"+group.ID+"/messages/search?q=SEARCHABLE&limit=10", bob, "")
	var search struct {
		Items []model.Message `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&search)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(search.Items) != 1 || search.Items[0].ID != sent.Message.ID {
		t.Fatalf("search status=%d items=%+v", res.StatusCode, search.Items)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v1/messages/"+sent.Message.ID+"/edits", bob, "")
	var edits struct {
		Items []model.MessageEdit `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&edits)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(edits.Items) != 2 {
		t.Fatalf("edits status=%d items=%+v", res.StatusCode, edits.Items)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/"+group.ID+"/messages", alice, `{"clientMsgId":"collab-invalid","type":"text","body":{"text":"bad mention","mentions":[{"userId":"usr_bob","name":"Bob"}]}}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("object mention status=%d", res.StatusCode)
	}
	res.Body.Close()
}
