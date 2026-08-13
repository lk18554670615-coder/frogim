package push

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linli/im/server/internal/store"
)

func TestGetuiTokenCacheAndCIDFiltering(t *testing.T) {
	var authCalls, pushCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/app-id/auth":
			authCalls.Add(1)
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			sum := sha256.Sum256([]byte("app-key" + body["timestamp"] + "master-secret"))
			if body["appkey"] != "app-key" || body["sign"] != hex.EncodeToString(sum[:]) {
				t.Fatalf("invalid auth body: %v", body)
			}
			writeGetui(w, 0, map[string]any{"token": "token-one", "expire_time": time.Now().Add(time.Hour).UnixMilli()})
		case "/v2/app-id/push/single/cid":
			pushCalls.Add(1)
			if r.Header.Get("token") != "token-one" {
				t.Fatalf("token header=%q", r.Header.Get("token"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			audience := body["audience"].(map[string]any)["cid"].([]any)
			if len(audience) != 1 || audience[0] != "getui-cid" || len(body["request_id"].(string)) != 30 {
				t.Fatalf("push body=%v", body)
			}
			assertGetuiChannels(t, body, "邻里通讯", "你收到一条新消息", "c1", "")
			writeGetui(w, 0, map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := &Getui{AppID: "app-id", AppKey: "app-key", MasterSecret: "master-secret", BaseURL: server.URL + "/v2", Client: server.Client()}
	item := store.OutboxItem{ID: 9, UserID: "u1", EventType: "message.created", Payload: map[string]any{"conversationId": "c1"}, Devices: []store.Device{{ID: "d1", Provider: "getui", PushToken: "getui-cid", NotificationsEnabled: true, PreviewEnabled: true, SoundEnabled: true, VibrationEnabled: true}, {ID: "d2", Provider: "apns", PushToken: "apns-token", NotificationsEnabled: true, PreviewEnabled: true, SoundEnabled: true, VibrationEnabled: true}}}
	if err := provider.Send(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	item.ID++
	if err := provider.Send(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if authCalls.Load() != 1 || pushCalls.Load() != 2 {
		t.Fatalf("auth=%d push=%d", authCalls.Load(), pushCalls.Load())
	}
}

func TestGetuiOfflineChannelsUseSafeEventSummaryAndRoutingPayload(t *testing.T) {
	var pushed map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth") {
			writeGetui(w, 0, map[string]any{"token": "token", "expire_time": time.Now().Add(time.Hour).UnixMilli()})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/push/single/cid") {
			if err := json.NewDecoder(r.Body).Decode(&pushed); err != nil {
				t.Fatal(err)
			}
			writeGetui(w, 0, map[string]any{})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	provider := &Getui{AppID: "app", AppKey: "key", MasterSecret: "master-secret", BaseURL: server.URL + "/v2", Client: server.Client()}
	item := store.OutboxItem{ID: 21, EventType: "message.created", Payload: map[string]any{"message": map[string]any{"id": "msg-1", "conversationId": "conv-1", "type": "image", "body": map[string]any{"caption": "private caption must never enter notification"}}}, Devices: []store.Device{{ID: "d1", Provider: "getui", PushToken: "cid", NotificationsEnabled: true, PreviewEnabled: true, SoundEnabled: true, VibrationEnabled: true}}}
	if err := provider.Send(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	assertGetuiChannels(t, pushed, "邻里通讯", "你收到一张图片", "conv-1", "msg-1")
	raw, _ := json.Marshal(pushed)
	if strings.Contains(string(raw), "private caption") {
		t.Fatalf("private message text leaked into push body: %s", raw)
	}
}

func TestGetuiAnnouncementSummaryIsBounded(t *testing.T) {
	title, body, payload := getuiNotification(store.OutboxItem{EventType: "announcement.published", Payload: map[string]any{"announcementId": "a1", "title": strings.Repeat("公", 50), "content": "  版本发布\n   请及时更新  "}})
	if len([]rune(title)) != 41 || body != "版本发布 请及时更新" || payload["announcementId"] != "a1" || payload["badge"] != "+1" || payload["unread"] != 1 {
		t.Fatalf("title=%q body=%q payload=%v", title, body, payload)
	}
}

func TestMessageSummaryForContactAndLocationDoesNotExposeBody(t *testing.T) {
	for _, tc := range []struct {
		typ, want string
	}{{"contact", "你收到一张联系人名片"}, {"location", "你收到一个位置"}} {
		title, summary, navigation := getuiNotification(store.OutboxItem{EventType: "message.created", Payload: map[string]any{"message": map[string]any{"id": "m1", "conversationId": "c1", "type": tc.typ, "body": map[string]any{"address": "secret address", "phone": "secret phone"}}}})
		if title != "邻里通讯" || summary != tc.want || navigation["address"] != nil || navigation["phone"] != nil {
			t.Fatalf("type=%s title=%q summary=%q navigation=%v", tc.typ, title, summary, navigation)
		}
	}
}

func TestMessageSummaryCoversWukongCustomContentWithoutExposingBody(t *testing.T) {
	for _, tc := range []struct {
		typ, want string
	}{
		{"chat_history", "你收到一条聊天记录"}, {"system", "你收到一条系统消息"},
		{"sticker", "你收到一个表情"}, {"moment", "你收到一条朋友圈分享"},
		{"call", "你有一条通话记录"}, {"live", "你收到一条直播互动"},
		{"support", "你收到一条客服消息"}, {"screenshot", "你收到一条截屏提示"},
	} {
		title, summary, navigation := getuiNotification(store.OutboxItem{EventType: "message.created", Payload: map[string]any{"message": map[string]any{"id": "m1", "conversationId": "c1", "type": tc.typ, "body": map[string]any{"content": "private content", "digest": "private digest"}}}})
		if title != "邻里通讯" || summary != tc.want || navigation["content"] != nil || navigation["digest"] != nil {
			t.Fatalf("type=%s title=%q summary=%q navigation=%v", tc.typ, title, summary, navigation)
		}
	}
}

func TestCallInviteNavigationUsesCanonicalEventAndRoutingFields(t *testing.T) {
	title, summary, navigation := getuiNotification(store.OutboxItem{EventType: "call.invited", Payload: map[string]any{"callId": "call-1", "conversationId": "conv-1", "mediaType": "video"}})
	if title != "视频通话" || summary != "你收到一个视频通话邀请" || navigation["type"] != "call.invited" || navigation["callId"] != "call-1" || navigation["conversationId"] != "conv-1" || navigation["mediaType"] != "video" {
		t.Fatalf("title=%q summary=%q navigation=%v", title, summary, navigation)
	}
}

func assertGetuiChannels(t *testing.T, body map[string]any, title, summary, conversationID, messageID string) {
	t.Helper()
	transmission := body["push_message"].(map[string]any)["transmission"].(string)
	var navigation map[string]any
	if err := json.Unmarshal([]byte(transmission), &navigation); err != nil {
		t.Fatalf("transmission payload: %v", err)
	}
	if navigation["conversationId"] != conversationID || (messageID != "" && navigation["messageId"] != messageID) || navigation["badge"] != "+1" || navigation["unread"].(float64) != 1 {
		t.Fatalf("navigation=%v", navigation)
	}
	channels := body["push_channel"].(map[string]any)
	android := channels["android"].(map[string]any)["ups"].(map[string]any)["notification"].(map[string]any)
	if android["title"] != title || android["body"] != summary || android["click_type"] != "payload" || android["payload"] != transmission {
		t.Fatalf("android=%v", android)
	}
	ios := channels["ios"].(map[string]any)
	aps := ios["aps"].(map[string]any)
	alert := aps["alert"].(map[string]any)
	if ios["type"] != "notify" || ios["auto_badge"] != "+1" || ios["payload"] != transmission || alert["title"] != title || alert["body"] != summary || aps["sound"] != "default" || aps["content-available"].(float64) != 0 {
		t.Fatalf("ios=%v", ios)
	}
}

func TestGetuiRefreshesExpiredTokenAndRedactsErrors(t *testing.T) {
	var authCalls, pushCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth") {
			call := authCalls.Add(1)
			writeGetui(w, 0, map[string]any{"token": "token-" + string(rune('0'+call)), "expire_time": time.Now().Add(time.Hour).UnixMilli()})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/push/single/cid") {
			if pushCalls.Add(1) == 1 {
				writeGetui(w, 10001, nil)
			} else {
				writeGetui(w, 20001, nil)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	provider := &Getui{AppID: "app", AppKey: "key", MasterSecret: "do-not-leak-secret", BaseURL: server.URL + "/v2", Client: server.Client()}
	err := provider.Send(context.Background(), store.OutboxItem{ID: 10, Devices: []store.Device{{ID: "device", Provider: "getui", PushToken: "do-not-leak-cid", NotificationsEnabled: true, PreviewEnabled: true, SoundEnabled: true, VibrationEnabled: true}}})
	if err == nil || authCalls.Load() != 2 || pushCalls.Load() != 2 {
		t.Fatalf("err=%v auth=%d push=%d", err, authCalls.Load(), pushCalls.Load())
	}
	if strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("secret leaked in error: %v", err)
	}
	var delivery *DeliveryError
	if !errors.As(err, &delivery) || len(delivery.InvalidDeviceIDs) != 0 || !delivery.Retryable || delivery.InvalidOnly {
		t.Fatalf("unconfirmed 20001 must remain retryable and must not invalidate a device: %+v", delivery)
	}
}

func TestGetuiContinuesAfterInvalidCIDAndAggregatesDeviceID(t *testing.T) {
	var pushedCIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth") {
			writeGetui(w, 0, map[string]any{"token": "token", "expire_time": time.Now().Add(time.Hour).UnixMilli()})
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/push/single/cid") {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		cid := body["audience"].(map[string]any)["cid"].([]any)[0].(string)
		pushedCIDs = append(pushedCIDs, cid)
		if cid == "invalid-secret-cid" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 20001, "msg": "target user is invalid", "data": nil})
			return
		}
		writeGetui(w, 0, map[string]any{})
	}))
	defer server.Close()

	provider := &Getui{AppID: "app", AppKey: "key", MasterSecret: "master-secret", BaseURL: server.URL + "/v2", Client: server.Client()}
	err := provider.Send(context.Background(), store.OutboxItem{ID: 31, Devices: []store.Device{
		{ID: "invalid-device", Provider: "getui", PushToken: "invalid-secret-cid", NotificationsEnabled: true},
		{ID: "healthy-device", Provider: "getui", PushToken: "healthy-cid", NotificationsEnabled: true},
	}})
	if len(pushedCIDs) != 2 || pushedCIDs[0] != "invalid-secret-cid" || pushedCIDs[1] != "healthy-cid" {
		t.Fatalf("all Getui devices must be attempted in order, pushed=%v", pushedCIDs)
	}
	var delivery *DeliveryError
	if !errors.As(err, &delivery) {
		t.Fatalf("error type=%T value=%v", err, err)
	}
	if len(delivery.InvalidDeviceIDs) != 1 || delivery.InvalidDeviceIDs[0] != "invalid-device" || delivery.Retryable || !delivery.InvalidOnly {
		t.Fatalf("delivery=%+v", delivery)
	}
	if strings.Contains(err.Error(), "invalid-secret-cid") {
		t.Fatalf("CID leaked in error: %v", err)
	}
}

func TestGetuiCombinedModeSuppressesOnlyIOSCallNotification(t *testing.T) {
	var pushedCIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth") {
			writeGetui(w, 0, map[string]any{"token": "token", "expire_time": time.Now().Add(time.Hour).UnixMilli()})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		cid := body["audience"].(map[string]any)["cid"].([]any)[0].(string)
		pushedCIDs = append(pushedCIDs, cid)
		writeGetui(w, 0, map[string]any{})
	}))
	defer server.Close()
	provider := &Getui{AppID: "app", AppKey: "key", MasterSecret: "master-secret", BaseURL: server.URL + "/v2", Client: server.Client(), SuppressIOSCallsWithVoIP: true}
	item := testCallOutbox()
	item.Devices = []store.Device{
		{ID: "ios-getui", Platform: "ios", Provider: "getui", PushToken: "ios-cid"},
		{ID: "android-getui", Platform: "android", Provider: "getui", PushToken: "android-cid"},
		{ID: "ios-voip", Platform: "ios", Provider: "apns_voip", PushToken: strings.Repeat("a", 64)},
	}
	if err := provider.Send(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if len(pushedCIDs) != 1 || pushedCIDs[0] != "android-cid" {
		t.Fatalf("pushed CIDs=%v", pushedCIDs)
	}
}

func writeGetui(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": "", "data": data})
}
