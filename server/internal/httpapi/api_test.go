package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/auth"
	"github.com/linli/im/server/internal/config"
	livekitcontrol "github.com/linli/im/server/internal/livekit"
	"github.com/linli/im/server/internal/media"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
	"github.com/linli/im/server/internal/wukong"
	"github.com/linli/im/server/internal/wukongplugin"
	"golang.org/x/crypto/bcrypt"
)

type signedMediaService struct{}

type robotAPITestStore struct {
	teststore.Memory
	mu             sync.Mutex
	conversationID string
	profiles       map[string]*store.RobotProfile
}

func (s *robotAPITestStore) ListRobotProfiles(context.Context) ([]*store.RobotProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]*store.RobotProfile, 0, len(s.profiles))
	for _, item := range s.profiles {
		copy := *item
		copy.Menus = append([]store.RobotMenu(nil), item.Menus...)
		items = append(items, &copy)
	}
	return items, nil
}

func (s *robotAPITestStore) RobotProfilesForConversation(_ context.Context, userID, conversationID string) ([]*store.RobotProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if userID != "usr_alice" || conversationID != s.conversationID {
		return nil, store.ErrNotFound
	}
	items := []*store.RobotProfile{}
	for _, item := range s.profiles {
		if item.Enabled {
			copy := *item
			copy.Menus = append([]store.RobotMenu(nil), item.Menus...)
			items = append(items, &copy)
		}
	}
	return items, nil
}

func (s *robotAPITestStore) ConfigureRobotProfile(_ context.Context, profile store.RobotProfile, actorID, reason string, at time.Time) (*store.RobotProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profiles == nil {
		s.profiles = map[string]*store.RobotProfile{}
	}
	profile.Name = "机器人小青"
	profile.Version = 1
	if current := s.profiles[profile.UserID]; current != nil {
		profile.Version = current.Version + 1
	}
	profile.UpdatedBy, profile.Reason, profile.UpdatedAt = actorID, reason, at
	copy := profile
	copy.Menus = append([]store.RobotMenu(nil), profile.Menus...)
	s.profiles[profile.UserID] = &copy
	return &copy, nil
}

type httpTestWukongRuntime struct {
	mu       sync.Mutex
	seq      map[string]int64
	byClient map[string]*model.Message
	byID     map[string]*model.Message
}

func installHTTPTestWukongRuntime(a *app.App) *httpTestWukongRuntime {
	runtime := &httpTestWukongRuntime{seq: map[string]int64{}, byClient: map[string]*model.Message{}, byID: map[string]*model.Message{}}
	a.SetMessageTransport(func(_ context.Context, request app.MessageTransportRequest) (app.MessageTransportResult, error) {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		key := request.UserID + ":" + request.ClientMsgID
		if existing := runtime.byClient[key]; existing != nil {
			return app.MessageTransportResult{MessageID: existing.ID, ClientMsgID: existing.ClientMsgID, MessageSeq: existing.Seq, CreatedAt: existing.CreatedAt, Duplicate: true}, nil
		}
		runtime.seq[request.ConversationID]++
		created := time.Now().UTC()
		message := &model.Message{ID: fmt.Sprintf("%d", len(runtime.byID)+5001), ClientMsgID: request.ClientMsgID, ConversationID: request.ConversationID, SenderID: request.UserID, Seq: runtime.seq[request.ConversationID], Type: request.Type, Body: request.Body, ReplyToID: request.ReplyToID, CreatedAt: created}
		runtime.byClient[key], runtime.byID[message.ID] = message, message
		if mediaID, _ := request.Body["mediaId"].(string); mediaID != "" {
			if route, err := a.ResolveWukongChannel(context.Background(), request.UserID, request.ConversationID); err == nil {
				_ = a.BindMediaChannel(store.MediaChannelBinding{MediaID: mediaID, ChannelID: route.ChannelID, ChannelType: route.ChannelType, SenderID: request.UserID})
			}
		}
		return app.MessageTransportResult{MessageID: message.ID, ClientMsgID: message.ClientMsgID, MessageSeq: message.Seq, CreatedAt: created}, nil
	})
	a.SetMessageSourceLoader(func(_ context.Context, _ string, ids []string) ([]*model.Message, error) {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		items := make([]*model.Message, 0, len(ids))
		for _, id := range ids {
			message := runtime.byID[id]
			if message == nil {
				return nil, store.ErrForbidden
			}
			copy := *message
			items = append(items, &copy)
		}
		return items, nil
	})
	a.SetMessageHistoryLoader(func(_ context.Context, _ string, conversationID string, before int64, limit int) ([]*model.Message, error) {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		items := []*model.Message{}
		for _, message := range runtime.byID {
			if message.ConversationID == conversationID && (before == 0 || message.Seq < before) {
				copy := *message
				items = append(items, &copy)
			}
		}
		slices.SortFunc(items, func(left, right *model.Message) int { return int(left.Seq - right.Seq) })
		if limit > 0 && len(items) > limit {
			items = items[len(items)-limit:]
		}
		return items, nil
	})
	return runtime
}

type pluginLifecycleTestStore struct {
	teststore.Memory
	mu       sync.Mutex
	releases map[string]*store.WukongPluginRelease
	events   []*store.WukongPluginEvent
}

func (s *pluginLifecycleTestStore) SaveWukongPluginRelease(_ context.Context, input store.WukongPluginRelease) (*store.WukongPluginRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.releases == nil {
		s.releases = map[string]*store.WukongPluginRelease{}
	}
	input.UpdatedAt = time.Now().UTC()
	copy := input
	s.releases[input.PluginNo] = &copy
	return &copy, nil
}

func (s *pluginLifecycleTestStore) GetWukongPluginRelease(_ context.Context, pluginNo string) (*store.WukongPluginRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.releases[pluginNo]
	if item == nil {
		return nil, store.ErrNotFound
	}
	copy := *item
	return &copy, nil
}

func (s *pluginLifecycleTestStore) ListWukongPluginReleases(context.Context) ([]*store.WukongPluginRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]*store.WukongPluginRelease, 0, len(s.releases))
	for _, item := range s.releases {
		copy := *item
		items = append(items, &copy)
	}
	return items, nil
}

func (s *pluginLifecycleTestStore) RecordWukongPluginEvent(_ context.Context, input store.WukongPluginEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := input
	s.events = append(s.events, &copy)
	return nil
}

func (s *pluginLifecycleTestStore) ListWukongPluginEvents(_ context.Context, pluginNo string, limit int) ([]*store.WukongPluginEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []*store.WukongPluginEvent{}
	for index := len(s.events) - 1; index >= 0 && len(items) < limit; index-- {
		if pluginNo == "" || s.events[index].PluginNo == pluginNo {
			copy := *s.events[index]
			items = append(items, &copy)
		}
	}
	return items, nil
}

type fakeLiveKitControl struct {
	ensured []string
	deleted []string
	issued  []string
	rooms   []livekitcontrol.RoomSummary
	members map[string][]livekitcontrol.ParticipantSummary
	metrics livekitcontrol.MetricsSummary
	removed []string
	err     error
}

func (f *fakeLiveKitControl) URL() string             { return "wss://chat.example.test/rtc" }
func (f *fakeLiveKitControl) TokenTTL() time.Duration { return 5 * time.Minute }
func (f *fakeLiveKitControl) EnsureCallRoom(_ context.Context, callID, _, _ string) error {
	f.ensured = append(f.ensured, callID)
	return f.err
}
func (f *fakeLiveKitControl) DeleteCallRoom(_ context.Context, callID string) error {
	f.deleted = append(f.deleted, callID)
	return f.err
}
func (f *fakeLiveKitControl) IssueParticipant(callID, userID, _, _ string) (livekitcontrol.ParticipantSession, error) {
	f.issued = append(f.issued, callID+":"+userID)
	if f.err != nil {
		return livekitcontrol.ParticipantSession{}, f.err
	}
	return livekitcontrol.ParticipantSession{
		URL: "wss://chat.example.test/rtc", RoomName: livekitcontrol.CallRoomName(callID),
		Token: "signed-test-token", ExpiresAt: time.Now().Add(5 * time.Minute).UTC(),
	}, nil
}
func (f *fakeLiveKitControl) ListRooms(context.Context) ([]livekitcontrol.RoomSummary, error) {
	return f.rooms, f.err
}
func (f *fakeLiveKitControl) ListParticipants(_ context.Context, room string) ([]livekitcontrol.ParticipantSummary, error) {
	return f.members[room], f.err
}
func (f *fakeLiveKitControl) Metrics(context.Context) (livekitcontrol.MetricsSummary, error) {
	return f.metrics, f.err
}
func (f *fakeLiveKitControl) RemoveParticipant(_ context.Context, room, identity string) error {
	f.removed = append(f.removed, room+"/"+identity)
	return f.err
}
func (f *fakeLiveKitControl) DeleteRoom(_ context.Context, room string) error {
	f.deleted = append(f.deleted, room)
	return f.err
}

func (signedMediaService) Prepare(context.Context, string, string, string, int64) (media.Prepared, error) {
	return media.Prepared{}, nil
}
func (signedMediaService) Complete(context.Context, string, string, string) (store.Media, error) {
	return store.Media{}, nil
}
func (signedMediaService) DownloadURL(_ context.Context, id string) (string, error) {
	return "https://media.example.test/object/" + id + "?X-Amz-Signature=test&X-Amz-Expires=900", nil
}

func TestLegacyWebSocketRoutesAreRemoved(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	token := loginToken(t, ts.URL, "13800000001")
	for _, request := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/v1/ws/ticket"},
		{method: http.MethodGet, path: "/v1/ws"},
		{method: http.MethodGet, path: "/v1/sync"},
	} {
		response := authenticatedRequest(t, request.method, ts.URL+request.path, token, "")
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s %s status=%d", request.method, request.path, response.StatusCode)
		}
	}
}

type clientVersionAPIStore struct {
	teststore.Memory
	policies map[string]store.ClientVersionPolicy
	history  []store.ClientVersionReleaseRecord
}

func (s *clientVersionAPIStore) ListClientVersionPolicies(context.Context) ([]store.ClientVersionPolicy, error) {
	items := make([]store.ClientVersionPolicy, 0, len(s.policies))
	for _, policy := range s.policies {
		items = append(items, policy)
	}
	return items, nil
}

func (s *clientVersionAPIStore) GetClientVersionPolicy(_ context.Context, platform string) (*store.ClientVersionPolicy, error) {
	policy, ok := s.policies[platform]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &policy, nil
}

func (s *clientVersionAPIStore) UpsertClientVersionPolicy(_ context.Context, policy store.ClientVersionPolicy, actor, reason string, at time.Time) (*store.ClientVersionPolicy, error) {
	policy.UpdatedBy, policy.UpdatedAt = actor, at
	s.policies[policy.Platform] = policy
	s.history = append(s.history, store.ClientVersionReleaseRecord{ID: "release-" + policy.Platform, Platform: policy.Platform, MinimumVersion: policy.MinimumVersion, LatestVersion: policy.LatestVersion, ForceUpdate: policy.ForceUpdate, RolloutPercentage: policy.RolloutPercentage, ReleaseNotes: policy.ReleaseNotes, DownloadURL: policy.DownloadURL, Reason: reason, UpdatedBy: actor, UpdatedAt: at})
	return &policy, nil
}

func (s *clientVersionAPIStore) ListClientVersionHistory(_ context.Context, platform, _ string, _ int) ([]store.ClientVersionReleaseRecord, int64, string, error) {
	items := make([]store.ClientVersionReleaseRecord, 0, len(s.history))
	for index := len(s.history) - 1; index >= 0; index-- {
		if s.history[index].Platform == platform {
			items = append(items, s.history[index])
		}
	}
	return items, int64(len(items)), "", nil
}

func TestClientVersionPublicAndAdminContracts(t *testing.T) {
	repository := &clientVersionAPIStore{policies: map[string]store.ClientVersionPolicy{
		"android": {
			Platform: "android", MinimumVersion: "2.0.0", LatestVersion: "2.2.0",
			RolloutPercentage: 0, ReleaseNotes: "必须升级", DownloadURL: "https://downloads.example.com/app.apk",
		},
	}}
	a, err := app.New(context.Background(), repository)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: strings.Repeat("s", 32), DevMode: true, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	adminKey := adminTestToken(t, cfg.JWTSecret)
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()

	response, err := http.Get(ts.URL + "/v2/config/version?platform=android&version=1.5.0&installId=install-api-contract")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("public status=%d body=%s", response.StatusCode, body)
	}
	var publicPayload struct {
		Data app.ClientVersionDecision `json:"data"`
	}
	if json.NewDecoder(response.Body).Decode(&publicPayload) != nil || !publicPayload.Data.ForceUpdate || !publicPayload.Data.UpdateAvailable || !publicPayload.Data.RolloutEligible {
		t.Fatalf("minimum-version decision=%+v", publicPayload.Data)
	}

	request, _ := http.NewRequest(http.MethodPut, ts.URL+"/v2/admin/client-versions/web", strings.NewReader(`{"minimumVersion":"1.0.0","latestVersion":"1.1.0","rolloutPercentage":25,"reason":"分批发布"}`))
	request.Header.Set("Authorization", "Bearer "+adminKey)
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unconfirmed write status=%d", response.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodPut, ts.URL+"/v2/admin/client-versions/web", strings.NewReader(`{"minimumVersion":"1.0.0","latestVersion":"1.1.0","rolloutPercentage":25,"releaseNotes":"Web 更新","downloadUrl":"https://app.example.com/","reason":"分批发布","confirmed":true}`))
	request.Header.Set("Authorization", "Bearer "+adminKey)
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("confirmed write status=%d body=%s", response.StatusCode, body)
	}
	_ = response.Body.Close()
	if repository.policies["web"].LatestVersion != "1.1.0" || repository.policies["web"].RolloutPercentage != 25 {
		t.Fatalf("stored policy=%+v", repository.policies["web"])
	}
	request, _ = http.NewRequest(http.MethodGet, ts.URL+"/v2/admin/client-versions/web/history?limit=10", nil)
	request.Header.Set("Authorization", "Bearer "+adminKey)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var historyPayload struct {
		Items []store.ClientVersionReleaseRecord `json:"items"`
		Total int64                              `json:"total"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&historyPayload) != nil || historyPayload.Total != 1 || len(historyPayload.Items) != 1 {
		t.Fatalf("history status=%d payload=%+v", response.StatusCode, historyPayload)
	}
	if item := historyPayload.Items[0]; item.Reason != "分批发布" || item.ReleaseNotes != "Web 更新" || item.UpdatedBy == "" {
		t.Fatalf("history item=%+v", item)
	}
}

func TestWukongAdminProxyUsesPinnedRoutesAndRequiresAuditedWrites(t *testing.T) {
	var mu sync.Mutex
	requests := make([]string, 0, 16)
	bodies := map[string]map[string]any{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("token") != "manager-secret" {
			t.Fatalf("manager token=%q", r.Header.Get("token"))
		}
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		if r.Method != http.MethodGet {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			bodies[r.URL.Path] = body
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/varz":
			_, _ = w.Write([]byte(`{"server_id":"1","version":"v2.2.5","connections":2,"manager_uid":"admin","manager_token":"must-not-leak"}`))
		case "/varz/setting":
			_, _ = w.Write([]byte(`{"logger":{"trace_on":0,"loki_on":1},"prometheus_on":1,"stress_on":0}`))
		case "/cluster/nodes":
			_, _ = w.Write([]byte(`{"total":1,"data":[{"id":1,"online":1}]}`))
		case "/connz":
			_, _ = w.Write([]byte(`{"connections":[],"total":0,"offset":0,"limit":20}`))
		case "/cluster/channels":
			_, _ = w.Write([]byte(`{"data":[{"channel_id":"group-1","channel_type":2}],"more":0}`))
		case "/cluster/messages":
			_, _ = w.Write([]byte(`{"data":[{"message_id":7}],"total":1}`))
		case "/cluster/devices":
			_, _ = w.Write([]byte(`{"data":[{"uid":"u1","device_flag":1,"token":"must-not-leak"}],"total":1}`))
		case "/plugins":
			_, _ = w.Write([]byte(`[{"no":"wk.plugin.im-policy","node_id":1,"status":"normal","config":{"secret":"******"}}]`))
		case "/pluginlogs/wk.plugin.im-policy":
			_, _ = w.Write([]byte(`{"plugin_no":"wk.plugin.im-policy","node_id":1,"manager_token":"must-not-leak","entries":[{"sequence":3,"stream":"stdout","timestamp":1770000000000,"message":"policy ready"}]}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()
	a, _ := app.New(context.Background(), teststore.Memory{})
	cfg := config.Config{
		JWTSecret: strings.Repeat("j", 32), AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
		WukongEnabled: true, WukongAPIURL: upstream.URL, WukongManagerURL: upstream.URL,
		WukongManagerToken: "manager-secret", WukongTokenSecret: strings.Repeat("t", 32),
		WukongTCPURL: "tcp://127.0.0.1:5100", WukongWSURL: "ws://127.0.0.1:5200",
	}
	adminKey := adminTestToken(t, cfg.JWTSecret)
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()

	res := adminKeyRequest(t, http.MethodGet, ts.URL+"/v2/admin/wukong/overview", adminKey, "")
	var overview map[string]any
	_ = json.NewDecoder(res.Body).Decode(&overview)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || overview["connections"] != float64(2) || overview["manager_uid"] != nil || overview["manager_token"] != nil {
		t.Fatalf("overview status=%d body=%v", res.StatusCode, overview)
	}
	res = adminKeyRequest(t, http.MethodGet, ts.URL+"/v2/admin/dashboard", adminKey, "")
	var dashboard map[string]any
	_ = json.NewDecoder(res.Body).Decode(&dashboard)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || dashboard["wukongConnections"] != float64(2) || dashboard["wukongStatus"] != "ok" || dashboard["websocketConnections"] != nil {
		t.Fatalf("dashboard status=%d body=%v", res.StatusCode, dashboard)
	}
	for _, path := range []string{
		"/v2/admin/wukong/settings",
		"/v2/admin/wukong/nodes",
		"/v2/admin/wukong/connections?uid=u1&page=1&limit=20",
		"/v2/admin/wukong/channels?channelId=group-1&channelType=2&limit=20",
		"/v2/admin/wukong/messages?channelId=group-1&channelType=2&limit=20",
		"/v2/admin/wukong/devices?uid=u1&deviceFlag=1&limit=20",
		"/v2/admin/wukong/plugins?nodeId=1",
		"/v2/admin/wukong/plugins/wk.plugin.im-policy/logs?nodeId=1&limit=25",
	} {
		res = adminKeyRequest(t, http.MethodGet, ts.URL+path, adminKey, "")
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, res.StatusCode, body)
		}
	}
	res = adminKeyRequest(t, http.MethodGet, ts.URL+"/v2/admin/wukong/devices?uid=u1&deviceFlag=1&limit=20", adminKey, "")
	deviceBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(deviceBody), `"token_on":true`) || strings.Contains(string(deviceBody), `"token"`) || strings.Contains(string(deviceBody), "must-not-leak") {
		t.Fatalf("device list status=%d body=%s", res.StatusCode, deviceBody)
	}
	res = adminKeyRequest(t, http.MethodGet, ts.URL+"/v2/admin/wukong/plugins/wk.plugin.im-policy/logs?nodeId=1&limit=25", adminKey, "")
	pluginLogBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(pluginLogBody), `"message":"policy ready"`) || strings.Contains(string(pluginLogBody), "manager_token") || strings.Contains(string(pluginLogBody), "must-not-leak") {
		t.Fatalf("plugin logs status=%d body=%s", res.StatusCode, pluginLogBody)
	}

	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/wukong/devices/u1/quit", adminKey, `{"deviceFlag":1,"reason":"安全处置","confirmed":true}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("device quit status=%d", res.StatusCode)
	}
	res = adminKeyRequest(t, http.MethodPut, ts.URL+"/v2/admin/wukong/plugins/wk.plugin.im-policy/config", adminKey, `{"nodeId":1,"config":{"endpoint":"http://server/policy","secret":"******"},"reason":"更新策略地址","confirmed":true}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("plugin config status=%d", res.StatusCode)
	}
	res = adminKeyRequest(t, http.MethodDelete, ts.URL+"/v2/admin/wukong/plugins/wk.plugin.test", adminKey, `{"nodeId":1,"reason":"移除测试插件","confirmed":true}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("plugin uninstall status=%d", res.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if bodies["/user/device_quit"]["uid"] != "u1" || bodies["/user/device_quit"]["device_flag"] != float64(1) {
		t.Fatalf("device body=%v", bodies["/user/device_quit"])
	}
	if bodies["/pluginconfig/wk.plugin.im-policy"]["node_id"] != float64(1) || bodies["/plugin/uninstall"]["plugin_no"] != "wk.plugin.test" {
		t.Fatalf("plugin bodies=%v", bodies)
	}
	if !containsRequest(requests, "GET /cluster/channels?channel_id=group-1&channel_type=2&limit=20") || !containsRequest(requests, "GET /cluster/devices?device_flag=1&limit=20&uid=u1") || !containsRequest(requests, "GET /pluginlogs/wk.plugin.im-policy?limit=25&node_id=1") {
		t.Fatalf("upstream requests=%v", requests)
	}
}

func TestUserCanListAndQuitOwnWukongPlatformSession(t *testing.T) {
	var quitBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("token") != "manager-secret" {
			t.Fatalf("manager token=%q", r.Header.Get("token"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user/token":
			w.WriteHeader(http.StatusOK)
		case "/cluster/devices":
			if r.URL.Query().Get("uid") != "usr_alice" {
				t.Fatalf("device uid=%q", r.URL.Query().Get("uid"))
			}
			_, _ = w.Write([]byte(`{"data":[{"uid":"usr_alice","device_flag":1,"device_level":1,"conn_count":2,"updated_at":1770000000123456789,"token":"must-not-leak"}],"total":1}`))
		case "/user/device_quit":
			if err := json.NewDecoder(r.Body).Decode(&quitBody); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	a, _ := app.New(context.Background(), teststore.Memory{})
	if err := a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		JWTSecret: strings.Repeat("j", 32), AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
		DevMode: true, DevOTPCode: "654321", WukongEnabled: true,
		WukongAPIURL: upstream.URL, WukongManagerURL: upstream.URL,
		WukongManagerToken: "manager-secret", WukongTokenSecret: strings.Repeat("t", 32),
		WukongTCPURL: "tcp://127.0.0.1:5100", WukongWSURL: "ws://127.0.0.1:5200",
	}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	token := loginToken(t, ts.URL, "13800000001")

	res := authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/users/me/im-devices", token, "")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"deviceFlag":1`) || !strings.Contains(string(body), `"connectionCount":2`) || !strings.Contains(string(body), `"updatedAt":1770000000`) || strings.Contains(string(body), "must-not-leak") || strings.Contains(string(body), `"token"`) {
		t.Fatalf("device sessions status=%d body=%s", res.StatusCode, body)
	}

	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v2/users/me/im-devices/1", token, "")
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent || quitBody["uid"] != "usr_alice" || quitBody["device_flag"] != float64(1) {
		t.Fatalf("quit status=%d body=%v", res.StatusCode, quitBody)
	}
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v2/users/me/im-devices/9", token, "")
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid flag status=%d", res.StatusCode)
	}
}

func TestAuthenticatedStreamMessageLifecycleUsesPinnedWuKongEvents(t *testing.T) {
	var mu sync.Mutex
	var anchor map[string]any
	var appended map[string]any
	var eventSync map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("token") != "manager-secret" {
			t.Fatalf("manager token=%q", r.Header.Get("token"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user/token":
			w.WriteHeader(http.StatusOK)
		case "/message/send":
			mu.Lock()
			_ = json.NewDecoder(r.Body).Decode(&anchor)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "data": map[string]any{"message_id": 987, "client_msg_no": "stream-http-1"}})
		case "/messages":
			_ = json.NewEncoder(w).Encode(map[string]any{"messages": []any{map[string]any{
				"message_idstr": "987", "message_seq": 9, "client_msg_no": "stream-http-1",
				"from_uid": "usr_alice", "channel_id": "usr_bob", "channel_type": 1,
				"setting": wukong.SettingStream, "payload": base64.StdEncoding.EncodeToString([]byte(`{"type":1,"content":""}`)),
			}}})
		case "/message/event":
			mu.Lock()
			_ = json.NewDecoder(r.Body).Decode(&appended)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "data": map[string]any{
				"client_msg_no": "stream-http-1", "event_key": "main", "event_id": "delta-http-1",
				"msg_event_seq": 1, "stream_status": "open", "channel_id": "usr_bob", "channel_type": 1, "from_uid": "usr_alice",
			}})
		case "/message/eventsync":
			_ = json.NewDecoder(r.Body).Decode(&eventSync)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "data": map[string]any{
				"client_msg_no": "stream-http-1", "from_msg_event_seq": 0, "next_msg_event_seq": 1, "more": 0,
				"filtered_by_event_key": "main", "events": []any{map[string]any{
					"msg_event_seq": 1, "event_id": "delta-http-1", "event_key": "main",
					"event_type": "stream.snapshot", "payload": map[string]any{"kind": "text", "text": "实时记录"},
				}},
			}})
		default:
			t.Fatalf("unexpected upstream path=%s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	friendRequest, err := a.RequestFriend("usr_alice", "usr_bob", "stream test")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.AcceptFriend("usr_bob", friendRequest.ID); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		JWTSecret: strings.Repeat("j", 32), DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
		WukongEnabled: true, WukongAPIURL: upstream.URL, WukongManagerURL: upstream.URL,
		WukongManagerToken: "manager-secret", WukongTokenSecret: strings.Repeat("t", 32),
		WukongTCPURL: "tcp://127.0.0.1:5100", WukongWSURL: "ws://127.0.0.1:5200",
	}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	bob := loginToken(t, ts.URL, "13800000002")
	conversationID := directConversation(t, ts.URL, alice, "usr_bob")
	baseURL := ts.URL + "/v2/messages/conversations/" + conversationID + "/streams"
	res := authenticatedRequest(t, http.MethodPost, baseURL, alice, `{"clientMsgNo":"stream-http-1","initialText":""}`)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("start status=%d body=%s", res.StatusCode, body)
	}
	mu.Lock()
	if anchor["is_stream"] != float64(1) || anchor["from_uid"] != "usr_alice" || anchor["channel_id"] != "usr_bob" {
		t.Fatalf("anchor=%v", anchor)
	}
	mu.Unlock()
	res = authenticatedRequest(t, http.MethodPost, baseURL+"/stream-http-1/events", alice, `{"eventId":"delta-http-1","eventType":"stream.delta","eventKey":"main","payload":{"kind":"text","delta":"实时记录"}}`)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("append status=%d body=%s", res.StatusCode, body)
	}
	mu.Lock()
	payload, _ := appended["payload"].(map[string]any)
	if appended["message_id"] != float64(987) || appended["visibility"] != "public" || payload["delta"] != "实时记录" {
		t.Fatalf("event=%v", appended)
	}
	mu.Unlock()
	res = authenticatedRequest(t, http.MethodGet, baseURL+"/stream-http-1/events?eventKey=main&limit=100", bob, "")
	var synced wukong.MessageEventSyncResult
	_ = json.NewDecoder(res.Body).Decode(&synced)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(synced.Events) != 1 || synced.Events[0].Payload["text"] != "实时记录" {
		t.Fatalf("sync status=%d result=%+v", res.StatusCode, synced)
	}

	if eventSync["channel_id"] != "usr_bob" || eventSync["from_uid"] != "usr_alice" {
		t.Fatalf("recipient event sync source=%v", eventSync)
	}

	res = authenticatedRequest(t, http.MethodPost, baseURL+"/stream-http-1/events", alice, `{"eventId":"bad","eventType":"stream.delta","payload":{"kind":"tool_call","delta":"forbidden"}}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-text stream status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodPost, baseURL+"/stream-http-1/events", alice, `{"eventId":"bad-close","eventType":"stream.close","payload":{"end_reason":0,"snapshot":{"kind":"text","text":"caller override"}}}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("caller-supplied close snapshot status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodPost, baseURL+"/stream-http-1/events", bob, `{"eventId":"foreign","eventType":"stream.delta","payload":{"kind":"text","delta":"forbidden"}}`)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign stream append status=%d", res.StatusCode)
	}
}

func TestSignedPluginLifecycleInstallsAttestsMovesAndUninstalls(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pluginNo := "wk.plugin.safe"
	fileName := pluginNo + "-linux-amd64.wkp"
	version := "1.2.3"
	managerReads := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("token") != "manager-secret" {
			t.Fatalf("manager token=%q", r.Header.Get("token"))
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/plugins" {
			managerReads++
			reportedVersion := version
			if managerReads <= 2 {
				reportedVersion = "previous-version-still-stopping"
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"no": pluginNo, "node_id": 1, "name": fileName, "version": reportedVersion, "methods": []string{"Route", "Send"}, "status": 1}})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	persistence := &pluginLifecycleTestStore{releases: map[string]*store.WukongPluginRelease{}}
	a, err := app.New(context.Background(), persistence)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	cfg := config.Config{
		JWTSecret: strings.Repeat("j", 32), AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
		WukongEnabled: true, WukongAPIURL: upstream.URL, WukongManagerURL: upstream.URL,
		WukongManagerToken: "manager-secret", WukongTokenSecret: strings.Repeat("t", 32),
		WukongTCPURL: "tcp://127.0.0.1:5100", WukongWSURL: "ws://127.0.0.1:5200",
		WukongPluginDir: directory, WukongPluginTrustedKeys: "release-key:" + base64.StdEncoding.EncodeToString(publicKey),
		WukongPluginAllowlist: pluginNo, WukongPluginMaxBytes: 1 << 20,
	}
	adminKey := adminTestToken(t, cfg.JWTSecret)
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	bundle := []byte("signed executable payload")
	digest := sha256.Sum256(bundle)
	manifest := wukongplugin.Manifest{SchemaVersion: 1, PluginNo: pluginNo, Name: fileName, Version: version, Methods: []string{"Route", "Send"}, OS: "linux", Arch: "amd64", FileName: fileName, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(bundle)), KeyID: "release-key"}
	manifestJSON, _ := json.Marshal(manifest)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifestJSON))
	res := pluginUploadRequest(t, http.MethodPost, ts.URL+"/v2/admin/wukong/plugins/install", adminKey, manifestJSON, signature, bundle, "发布审批 PLUGIN-1")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("install status=%d body=%s", res.StatusCode, body)
	}
	activePath := filepath.Join(directory, fileName)
	if written, readErr := os.ReadFile(activePath); readErr != nil || !bytes.Equal(written, bundle) {
		t.Fatalf("installed bundle=%q err=%v", written, readErr)
	}
	release, err := persistence.GetWukongPluginRelease(context.Background(), pluginNo)
	if err != nil || release.Status != "active" || release.SHA256 != manifest.SHA256 || release.KeyID != "release-key" {
		t.Fatalf("release=%#v err=%v", release, err)
	}
	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/wukong/plugins/"+pluginNo+"/disable", adminKey, `{"nodeId":1,"reason":"维护窗口","confirmed":true}`)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", res.StatusCode, body)
	}
	if _, err = os.Stat(activePath + ".disabled"); err != nil {
		t.Fatal(err)
	}
	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/wukong/plugins/"+pluginNo+"/enable", adminKey, `{"nodeId":1,"reason":"维护结束","confirmed":true}`)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("enable status=%d body=%s", res.StatusCode, body)
	}
	res = adminKeyRequest(t, http.MethodDelete, ts.URL+"/v2/admin/wukong/plugins/"+pluginNo, adminKey, `{"nodeId":1,"reason":"测试卸载","confirmed":true}`)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("uninstall status=%d body=%s", res.StatusCode, body)
	}
	if _, err = os.Stat(activePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active file remains err=%v", err)
	}
	events, err := persistence.ListWukongPluginEvents(context.Background(), pluginNo, 10)
	if err != nil || len(events) != 4 || events[0].Action != "uninstall" || events[3].Action != "install" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
}

func pluginUploadRequest(t *testing.T, method, target, adminKey string, manifest []byte, signature string, bundle []byte, reason string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"manifest": string(manifest), "signature": signature, "nodeId": "1", "reason": reason, "confirmed": "true"} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("bundle", "plugin.wkp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(bundle); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, target, &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+adminKey)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestLiveKitAdminRoomsParticipantsAndConfirmedRemoval(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	cfg := config.Config{JWTSecret: strings.Repeat("j", 32), AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour, LiveKitEnabled: true}
	adminKey := adminTestToken(t, cfg.JWTSecret)
	api := New(cfg, a)
	fake := &fakeLiveKitControl{
		rooms:   []livekitcontrol.RoomSummary{{SID: "RM_1", Name: "call_1", ParticipantCount: 1, MaxParticipants: 9}},
		members: map[string][]livekitcontrol.ParticipantSummary{"call_1": {{SID: "PA_1", Identity: "u1", State: "ACTIVE", Tracks: []livekitcontrol.ParticipantTrack{}}}},
		metrics: livekitcontrol.MetricsSummary{Healthy: true, ActiveRooms: 1, ActiveParticipants: 1, CPUPercent: 12.5, ResidentMemoryBytes: 1024},
	}
	api.livekit, api.livekitSetupErr = fake, nil
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()

	res := adminKeyRequest(t, http.MethodGet, ts.URL+"/v2/admin/livekit/rooms", adminKey, "")
	var rooms struct {
		Items []livekitcontrol.RoomSummary `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&rooms)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(rooms.Items) != 1 || rooms.Items[0].Name != "call_1" {
		t.Fatalf("rooms status=%d body=%+v", res.StatusCode, rooms)
	}
	res = adminKeyRequest(t, http.MethodGet, ts.URL+"/v2/admin/livekit/metrics", adminKey, "")
	var metrics livekitcontrol.MetricsSummary
	_ = json.NewDecoder(res.Body).Decode(&metrics)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !metrics.Healthy || metrics.ActiveRooms != 1 || metrics.CPUPercent != 12.5 {
		t.Fatalf("metrics status=%d body=%+v", res.StatusCode, metrics)
	}
	res = adminKeyRequest(t, http.MethodGet, ts.URL+"/v2/admin/livekit/rooms/call_1/participants", adminKey, "")
	var participants struct {
		Items []livekitcontrol.ParticipantSummary `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&participants)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(participants.Items) != 1 || participants.Items[0].Identity != "u1" {
		t.Fatalf("participants status=%d body=%+v", res.StatusCode, participants)
	}
	res = adminKeyRequest(t, http.MethodDelete, ts.URL+"/v2/admin/livekit/rooms/call_1/participants/u1", adminKey, `{"reason":"异常连接","confirmed":true}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent || len(fake.removed) != 1 || fake.removed[0] != "call_1/u1" {
		t.Fatalf("remove status=%d calls=%v", res.StatusCode, fake.removed)
	}
	res = adminKeyRequest(t, http.MethodDelete, ts.URL+"/v2/admin/livekit/rooms/call_1", adminKey, `{"reason":"故障房间清理","confirmed":true}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent || len(fake.deleted) != 1 || fake.deleted[0] != "call_1" {
		t.Fatalf("delete status=%d calls=%v", res.StatusCode, fake.deleted)
	}
	res = adminKeyRequest(t, http.MethodDelete, ts.URL+"/v2/admin/livekit/rooms/external-room", adminKey, `{"reason":"测试","confirmed":true}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-application room status=%d", res.StatusCode)
	}
}

func containsRequest(items []string, expected string) bool {
	for _, item := range items {
		if item == expected {
			return true
		}
	}
	return false
}

func TestCORSPreflightAllowsV2ClientPlatformHeader(t *testing.T) {
	a, err := app.New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		JWTSecret:      "test-secret",
		AllowedOrigins: []string{"http://127.0.0.1:52182"},
		AccessTTL:      time.Hour,
		RefreshTTL:     24 * time.Hour,
	}
	request := httptest.NewRequest(http.MethodOptions, "/v2/auth/password-login", nil)
	request.Header.Set("Origin", "http://127.0.0.1:52182")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "content-type,x-client-platform")
	recorder := httptest.NewRecorder()
	New(cfg, a).Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), "x-client-platform") {
		t.Fatalf("preflight headers=%q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:52182" {
		t.Fatalf("allow origin=%q", got)
	}
}

func TestHealthProbeBypassesApplicationRateLimit(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	cfg := config.Config{JWTSecret: "test-secret", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	h := New(cfg, a).Handler()
	for i := 0; i < 350; i++ {
		r := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("probe %d returned %d", i, w.Code)
		}
		if i == 0 && !strings.Contains(w.Body.String(), `"service":"qingwaguagua-im"`) {
			t.Fatalf("health probe exposes stale service identity: %s", w.Body.String())
		}
	}
}

func TestReadyProbeIncludesWukongSessionDependencies(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcpListener.Close()
	var healthStatus atomic.Int32
	healthStatus.Store(http.StatusServiceUnavailable)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(int(healthStatus.Load()))
	}))
	defer upstream.Close()
	a, _ := app.New(context.Background(), teststore.Memory{})
	cfg := config.Config{
		JWTSecret: strings.Repeat("j", 32), AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
		WukongEnabled: true, WukongAPIURL: upstream.URL, WukongManagerURL: upstream.URL,
		WukongManagerToken: "manager-secret", WukongTokenSecret: strings.Repeat("t", 32),
		WukongTCPURL: "tcp://" + tcpListener.Addr().String(), WukongWSURL: "ws://127.0.0.1:5200",
	}
	handler := New(cfg, a).Handler()

	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"code":"NOT_READY"`) {
		t.Fatalf("unhealthy readiness status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	healthStatus.Store(http.StatusOK)
	request = httptest.NewRequest(http.MethodGet, "/ready", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("healthy readiness status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWukongInternalEndpointHasCapacityIndependentOfPublicRateLimit(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	cfg := config.Config{
		JWTSecret:          "test-secret",
		WukongPolicySecret: strings.Repeat("p", 32),
		AccessTTL:          time.Hour,
		RefreshTTL:         24 * time.Hour,
	}
	h := New(cfg, a).Handler()
	for i := 0; i < 350; i++ {
		r := httptest.NewRequest(http.MethodPost, "/internal/wukong/policy/send", strings.NewReader(`{}`))
		r.RemoteAddr = "10.0.0.2:1234"
		r.Header.Set(wukongPolicySecretHeader, cfg.WukongPolicySecret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("trusted WuKong request %d was constrained by the public rate limit", i)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("trusted WuKong request %d returned %d: %s", i, w.Code, w.Body.String())
		}
	}
}

func TestMaintenanceModeBlocksUsersButKeepsOperationsReachable(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	if err := a.UpdateSettings("admin", map[string]any{
		"maintenanceMode": true,
		"announcement":    "计划维护至 23:30",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()

	res, err := publicHTTPPost(t, ts.URL+"/v2/auth/login", "application/json", strings.NewReader(`{"phone":"13800000001","code":"654321"}`))
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
	res, err = http.Post(ts.URL+"/v2/admin/auth/login", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode == http.StatusServiceUnavailable {
		t.Fatalf("admin login was blocked by maintenance")
	}
}

func TestCallLifecycleRESTPermissionsAndIdempotency(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour, CallInviteTTL: 30 * time.Second}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	bob := loginToken(t, ts.URL, "13800000002")
	charlie := loginToken(t, ts.URL, "13800000003")
	cid := directConversation(t, ts.URL, alice, "usr_bob")

	inviteBody := fmt.Sprintf(`{"callId":"rest-call-1","conversationId":%q,"calleeUserId":"usr_bob","mediaType":"video"}`, cid)
	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/invite", alice, inviteBody)
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
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/invite", alice, inviteBody)
	var retry struct {
		Duplicate bool `json:"duplicate"`
	}
	_ = json.NewDecoder(res.Body).Decode(&retry)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || !retry.Duplicate {
		t.Fatalf("invite retry status=%d duplicate=%v", res.StatusCode, retry.Duplicate)
	}

	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/rest-call-1/accept", alice, `{}`)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("caller accept status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/rest-call-1/accept", bob, `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("callee accept status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/rest-call-1/accept", bob, `{}`)
	var acceptedRetry struct {
		Duplicate bool `json:"duplicate"`
	}
	_ = json.NewDecoder(res.Body).Decode(&acceptedRetry)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !acceptedRetry.Duplicate {
		t.Fatalf("accept retry status=%d duplicate=%v", res.StatusCode, acceptedRetry.Duplicate)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/rest-call-1/hangup", alice, `{"reason":"completed"}`)
	var ended struct {
		Call model.CallSession `json:"call"`
	}
	_ = json.NewDecoder(res.Body).Decode(&ended)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || ended.Call.Status != "ended" || ended.Call.EndReason != "completed" {
		t.Fatalf("hangup status=%d call=%+v", res.StatusCode, ended.Call)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/calls/rest-call-1", charlie, "")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("non-participant get status=%d", res.StatusCode)
	}
	res.Body.Close()
}

func TestLiveKitV2CallTokenRequiresAcceptedParticipantAndCleansRoom(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{
		JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321",
		AccessTTL:  time.Hour,
		RefreshTTL: 24 * time.Hour, CallInviteTTL: 30 * time.Second,
		LiveKitEnabled: true, LiveKitURL: "wss://chat.example.test/rtc",
		LiveKitAPIURL: "http://livekit:7880", LiveKitAPIKey: "devkey",
		LiveKitAPISecret: strings.Repeat("l", 32), LiveKitTokenTTL: 5 * time.Minute,
	}
	api := New(cfg, a)
	fake := &fakeLiveKitControl{}
	api.livekit = fake
	api.livekitSetupErr = nil
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	bob := loginToken(t, ts.URL, "13800000002")
	charlie := loginToken(t, ts.URL, "13800000003")
	cid := directConversation(t, ts.URL, alice, "usr_bob")

	res := authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/calls/config", alice, "")
	var configuration struct {
		Provider            string `json:"provider"`
		URL                 string `json:"url"`
		MaxParticipants     int    `json:"maxParticipants"`
		SupportsScreenShare bool   `json:"supportsScreenShare"`
	}
	_ = json.NewDecoder(res.Body).Decode(&configuration)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || configuration.Provider != "livekit" || configuration.URL == "" || configuration.MaxParticipants != 9 || !configuration.SupportsScreenShare {
		t.Fatalf("config status=%d value=%+v", res.StatusCode, configuration)
	}

	invite := fmt.Sprintf(`{"callId":"livekit-v2-call","conversationId":%q,"calleeUserId":"usr_bob","mediaType":"video"}`, cid)
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/invite", alice, invite)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("invite status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/livekit-v2-call/token", alice, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("pre-accept token status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/livekit-v2-call/accept", bob, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(fake.ensured) != 1 {
		t.Fatalf("accept status=%d ensured=%v", res.StatusCode, fake.ensured)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/livekit-v2-call/token", charlie, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("non-participant token status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/livekit-v2-call/token", alice, `{}`)
	var tokenBody struct {
		Session livekitcontrol.ParticipantSession `json:"session"`
	}
	_ = json.NewDecoder(res.Body).Decode(&tokenBody)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || tokenBody.Session.RoomName != "call_livekit-v2-call" || tokenBody.Session.Token == "" || res.Header.Get("Cache-Control") != "no-store" || len(fake.issued) != 1 {
		t.Fatalf("token status=%d headers=%v body=%+v issued=%v", res.StatusCode, res.Header, tokenBody, fake.issued)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/livekit-v2-call/hangup", alice, `{"reason":"completed"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(fake.deleted) != 1 || fake.deleted[0] != "livekit-v2-call" {
		t.Fatalf("hangup status=%d deleted=%v", res.StatusCode, fake.deleted)
	}
}

func TestLiveKitGroupCallTracksNinePartyMembershipIndependently(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	group, err := a.CreateGroup("usr_alice", "RTC group", []string{"usr_bob", "usr_admin"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321",
		AccessTTL:  time.Hour,
		RefreshTTL: 24 * time.Hour, CallInviteTTL: 30 * time.Second,
		LiveKitEnabled: true, LiveKitURL: "wss://chat.example.test/rtc",
		LiveKitAPIURL: "http://livekit:7880", LiveKitAPIKey: "devkey",
		LiveKitAPISecret: strings.Repeat("l", 32), LiveKitTokenTTL: 5 * time.Minute,
	}
	api := New(cfg, a)
	fake := &fakeLiveKitControl{}
	api.livekit, api.livekitSetupErr = fake, nil
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	bob := loginToken(t, ts.URL, "13800000002")
	third := loginToken(t, ts.URL, "13800000000")

	invite := fmt.Sprintf(`{"callId":"group-call","conversationId":%q,"mediaType":"video"}`, group.ID)
	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/invite", alice, invite)
	var invited struct {
		Call model.CallSession `json:"call"`
	}
	_ = json.NewDecoder(res.Body).Decode(&invited)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || invited.Call.Kind != "group" || len(invited.Call.ParticipantIDs) != 3 || !slices.Contains(invited.Call.JoinedUserIDs, "usr_alice") {
		t.Fatalf("group invite status=%d call=%+v", res.StatusCode, invited.Call)
	}

	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/group-call/accept", bob, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(fake.ensured) != 1 {
		t.Fatalf("first group accept status=%d ensured=%v", res.StatusCode, fake.ensured)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/group-call/token", third, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("unaccepted group participant token status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/group-call/accept", third, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("second group accept status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/group-call/token", third, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("accepted group participant token status=%d", res.StatusCode)
	}

	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/group-call/hangup", bob, `{"reason":"left"}`)
	var left struct {
		Call model.CallSession `json:"call"`
	}
	_ = json.NewDecoder(res.Body).Decode(&left)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || left.Call.Status != "accepted" || !slices.Contains(left.Call.LeftUserIDs, "usr_bob") || len(fake.removed) != 1 {
		t.Fatalf("group participant leave status=%d call=%+v removed=%v", res.StatusCode, left.Call, fake.removed)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/group-call/token", bob, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("left group participant token status=%d", res.StatusCode)
	}

	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/calls/group-call/hangup", alice, `{"reason":"host_left"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !slices.Contains(fake.deleted, "group-call") {
		t.Fatalf("group host hangup status=%d deleted=%v", res.StatusCode, fake.deleted)
	}
}

func TestFriendRequestLifecycleMetadataDeleteAndBlock(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	bob := loginToken(t, ts.URL, "13800000002")
	requestBody := `{"userId":"usr_bob","message":"我是邻居","source":"search"}`
	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/contacts/requests", alice, requestBody)
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
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/contacts/requests", bob, "")
	var requestList struct {
		Items []struct {
			model.FriendRequest
			User map[string]any `json:"user"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&requestList); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(requestList.Items) != 1 || requestList.Items[0].User["id"] != "usr_alice" || requestList.Items[0].User["name"] != "Alice" {
		t.Fatalf("friend request peer status=%d items=%+v", res.StatusCode, requestList.Items)
	}
	if _, exposed := requestList.Items[0].User["phone"]; exposed {
		t.Fatalf("friend request peer leaked phone: %+v", requestList.Items[0].User)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/contacts/requests", alice, requestBody)
	var retry model.FriendRequest
	_ = json.NewDecoder(res.Body).Decode(&retry)
	res.Body.Close()
	if retry.ID != first.ID {
		t.Fatalf("retry id=%s first=%s", retry.ID, first.ID)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/contacts/requests/"+first.ID+"/accept", alice, `{}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("sender accept=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/contacts/requests/"+first.ID+"/reject", bob, `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reject=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/contacts/requests/"+first.ID+"/reject", bob, `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reject retry=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/contacts/requests", alice, requestBody)
	var second model.FriendRequest
	_ = json.NewDecoder(res.Body).Decode(&second)
	res.Body.Close()
	if second.ID == first.ID {
		t.Fatal("new request reused terminal id")
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/contacts/requests/"+second.ID+"/accept", bob, `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("accept=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/contacts/friends/usr_bob", alice, `{"remark":"隔壁小林","tags":["邻居","摄影"]}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("metadata=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/contacts/friends", alice, "")
	var friends struct {
		Items []model.User `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&friends)
	res.Body.Close()
	if len(friends.Items) != 1 || friends.Items[0].Remark != "隔壁小林" || len(friends.Items[0].Tags) != 2 {
		t.Fatalf("friends=%+v", friends.Items)
	}
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v2/contacts/friends/usr_bob", alice, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete friend=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v2/contacts/blocks/usr_bob", alice, `{"blocked":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("block=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/contacts/requests", alice, requestBody)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("blocked request=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/contacts/blocks", alice, "")
	var blocked struct {
		Items []model.User `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&blocked)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(blocked.Items) != 1 || blocked.Items[0].ID != "usr_bob" || blocked.Items[0].Phone != "" {
		t.Fatalf("blocked users status=%d items=%+v", res.StatusCode, blocked.Items)
	}
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v2/contacts/blocks/usr_bob", alice, `{"blocked":false}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unblock=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/contacts/blocks", alice, "")
	_ = json.NewDecoder(res.Body).Decode(&blocked)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(blocked.Items) != 0 {
		t.Fatalf("blocked users after unblock status=%d items=%+v", res.StatusCode, blocked.Items)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/contacts/requests", alice, requestBody)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("unblocked request=%d", res.StatusCode)
	}
	res.Body.Close()
}

func TestConversationPreferencesAndHide(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	aliceToken := loginToken(t, ts.URL, "13800000001")
	cid := directConversation(t, ts.URL, aliceToken, "usr_bob")

	preferenceBody := `{"pinned":true,"saved":true,"notificationsMuted":true,"manualUnread":true}`
	res := authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/channels/conversations/"+cid+"/preferences", aliceToken, preferenceBody)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("preference status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	list := listConversations(t, ts.URL, aliceToken)
	if len(list) == 0 || !list[0].Membership.Pinned || !list[0].Membership.Saved || !list[0].Membership.NotificationsMuted || !list[0].Membership.ManualUnread {
		t.Fatalf("preferences missing from list: %+v", list)
	}

	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v2/channels/conversations/"+cid+"/read", aliceToken, `{"seq":0}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("read status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	list = listConversations(t, ts.URL, aliceToken)
	if len(list) == 0 || list[0].Membership.ManualUnread {
		t.Fatalf("mark read did not clear manual unread: %+v", list)
	}

	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v2/channels/conversations/"+cid, aliceToken, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("hide status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	if list = listConversations(t, ts.URL, aliceToken); len(list) != 0 {
		t.Fatalf("hidden conversation remains visible: %+v", list)
	}
}

func TestUserProfilePhoneDevicesFavoritesAndFeedback(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	_ = a.CreateMedia(store.Media{ID: "avatar-alice", OwnerID: "usr_alice", ObjectKey: "a", MIME: "image/png", Status: "ready", Size: 12})
	_ = a.CreateMedia(store.Media{ID: "avatar-bob", OwnerID: "usr_bob", ObjectKey: "b", MIME: "image/png", Status: "ready", Size: 12})
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	token := loginToken(t, ts.URL, "13800000001")

	res := authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/users/me", token, `{"name":"Alice Chen","handle":"alice_2026","signature":"Hello","gender":"female","avatarMediaId":"avatar-alice"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("profile status=%d", res.StatusCode)
	}
	var profile model.User
	if err := json.NewDecoder(res.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if profile.Handle != "alice_2026" || profile.Gender != "female" || profile.AvatarMediaID != "avatar-alice" || !strings.HasPrefix(profile.AvatarURL, "/v2/avatars/avatar-alice?") {
		t.Fatalf("profile=%+v", profile)
	}
	bobToken := loginToken(t, ts.URL, "13800000002")
	handleConflict := authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/users/me", bobToken, `{"handle":"alice_2026"}`)
	if handleConflict.StatusCode != http.StatusConflict {
		t.Fatalf("handle conflict status=%d", handleConflict.StatusCode)
	}
	var handleConflictBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(handleConflict.Body).Decode(&handleConflictBody); err != nil {
		t.Fatal(err)
	}
	_ = handleConflict.Body.Close()
	if handleConflictBody.Error.Code != "HANDLE_TAKEN" {
		t.Fatalf("handle conflict code=%q", handleConflictBody.Error.Code)
	}
	invalidGender := authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/users/me", token, `{"gender":"private"}`)
	if invalidGender.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid gender status=%d", invalidGender.StatusCode)
	}
	_ = invalidGender.Body.Close()
	avatarResponse, err := http.Get(ts.URL + profile.AvatarURL)
	if err != nil {
		t.Fatal(err)
	}
	if avatarResponse.StatusCode == http.StatusForbidden {
		t.Fatalf("signed avatar URL was rejected: %s", profile.AvatarURL)
	}
	_ = avatarResponse.Body.Close()
	invalidAvatar, _ := http.Get(ts.URL + "/v2/avatars/avatar-alice?expires=1&signature=invalid")
	if invalidAvatar.StatusCode != http.StatusForbidden {
		t.Fatalf("invalid avatar status=%d", invalidAvatar.StatusCode)
	}
	_ = invalidAvatar.Body.Close()
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/users/me", token, `{"avatarMediaId":"avatar-bob"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign avatar status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	bobAvatarID := "avatar-bob"
	bobHandle := "bob_avatar"
	if _, err = a.UpdateUserProfile("usr_bob", store.UserProfileUpdate{Handle: &bobHandle, AvatarMediaID: &bobAvatarID}); err != nil {
		t.Fatal(err)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/contacts/search?q=bob_avatar&by=handle", token, "")
	var search struct {
		Items []model.User `json:"items"`
	}
	if err = json.NewDecoder(res.Body).Decode(&search); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || len(search.Items) != 1 || !strings.HasPrefix(search.Items[0].AvatarURL, "/v2/avatars/avatar-bob?") {
		t.Fatalf("search avatar status=%d items=%+v", res.StatusCode, search.Items)
	}

	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/users/me/phone/code", token, `{"phone":"13900000001"}`)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("phone code status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/users/me/phone", token, `{"phone":"13900000001","code":"654321"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("phone update status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/users/me/devices", token, `{"deviceId":"device-1","platform":"ios","provider":"apns","pushToken":"secret-token","notificationsEnabled":true,"previewEnabled":false,"soundEnabled":false,"vibrationEnabled":true}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("device register status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/users/me/devices", token, "")
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
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v2/users/me/devices/device-1", token, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("device delete status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/messages/favorites", token, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("favorites status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/feedback", token, `{"category":"product","content":"Please add dark mode","contact":"alice@example.com"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("feedback status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestWebPushConfigurationAndSubscriptionValidation(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{
		JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321",
		AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
		WebPushPublicKey: "public-vapid-key", WebPushPrivateKey: "private-vapid-key", WebPushSubject: "https://chat.example.com",
	}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	response, err := http.Get(ts.URL + "/v2/config/web-push")
	if err != nil {
		t.Fatal(err)
	}
	var publicConfig struct {
		Enabled   bool   `json:"enabled"`
		PublicKey string `json:"publicKey"`
	}
	if err = json.NewDecoder(response.Body).Decode(&publicConfig); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !publicConfig.Enabled || publicConfig.PublicKey != cfg.WebPushPublicKey {
		t.Fatalf("public Web Push config status=%d body=%+v", response.StatusCode, publicConfig)
	}

	token := loginToken(t, ts.URL, "13800000001")
	response = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/users/me/devices", token,
		`{"deviceId":"web-bad","platform":"web","provider":"webpush","pushToken":"not-json"}`)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed Web Push subscription status=%d", response.StatusCode)
	}
	_, x, y, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authKey := make([]byte, 16)
	_, _ = rand.Read(authKey)
	subscription, _ := json.Marshal(map[string]any{
		"endpoint": "https://push.example.com/subscription",
		"keys": map[string]string{
			"p256dh": base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), x, y)),
			"auth":   base64.RawURLEncoding.EncodeToString(authKey),
		},
	})
	requestBody, _ := json.Marshal(map[string]any{
		"deviceId": "web-valid", "platform": "web", "provider": "webpush", "pushToken": string(subscription),
	})
	response = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/users/me/devices", token, string(requestBody))
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("valid Web Push subscription status=%d", response.StatusCode)
	}
}

func TestForwardMessagesSeparateMergedAndIdempotent(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	installHTTPTestWukongRuntime(a)
	cid, err := a.DirectConversation("usr_alice", "usr_bob")
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := a.SendMessage("usr_alice", cid.ID, "source-1", "text", map[string]any{"text": "hello"}, "")
	second, _, _ := a.SendMessage("usr_bob", cid.ID, "source-2", "text", map[string]any{"text": "world"}, "")
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	token := loginToken(t, ts.URL, "13800000001")

	separate := fmt.Sprintf(`{"targetConversationId":%q,"sourceMessageIds":[%q,%q],"mode":"separate","clientBatchId":"batch-one"}`, cid.ID, first.ID, second.ID)
	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/messages/forward", token, separate)
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
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/messages/forward", token, separate)
	forwarded = struct {
		Messages  []model.Message `json:"messages"`
		Duplicate bool            `json:"duplicate"`
	}{}
	_ = json.NewDecoder(res.Body).Decode(&forwarded)
	_ = res.Body.Close()
	if !forwarded.Duplicate || len(forwarded.Messages) != 2 {
		t.Fatalf("separate retry=%+v", forwarded)
	}

	merged := fmt.Sprintf(`{"targetConversationId":%q,"sourceMessageIds":[%q,%q],"mode":"merged","clientBatchId":"batch-two"}`, cid.ID, first.ID, second.ID)
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/messages/forward", token, merged)
	_ = json.NewDecoder(res.Body).Decode(&forwarded)
	_ = res.Body.Close()
	if len(forwarded.Messages) != 1 || forwarded.Messages[0].Type != "chat_history" || forwarded.Messages[0].SenderID != "usr_alice" {
		t.Fatalf("merged forward=%+v", forwarded)
	}
	entries, ok := forwarded.Messages[0].Body["entries"].([]any)
	if !ok || len(entries) != 2 {
		t.Fatalf("merged entries=%T %+v", forwarded.Messages[0].Body["entries"], forwarded.Messages[0].Body["entries"])
	}
	firstEntry, ok := entries[0].(map[string]any)
	if !ok || firstEntry["sourceMessageId"] != first.ID || firstEntry["senderId"] != "usr_alice" || firstEntry["type"] != "text" || firstEntry["summary"] != "hello" {
		t.Fatalf("merged first entry=%T %+v", entries[0], entries[0])
	}
	if _, exists := firstEntry["body"]; exists {
		t.Fatal("merged entry contains source message body")
	}
	if _, exists := forwarded.Messages[0].Body["sdp"]; exists {
		t.Fatal("merged forward contains untrusted transport data")
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/messages/forward", token, fmt.Sprintf(`{"targetConversationId":%q,"sourceMessageIds":["missing"],"mode":"merged","clientBatchId":"batch-missing"}`, cid.ID))
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("inaccessible source status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestPasswordRegistrationLoginAndReset(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	cfg := config.Config{JWTSecret: "test-secret-password-auth-32-bytes", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	phone := "13912345678"

	registerBody := fmt.Sprintf(`{"phone":%q,"code":"654321","password":"StrongPass123!","name":"New User"}`, phone)
	res, err := publicHTTPPost(t, ts.URL+"/v2/auth/register", "application/json", strings.NewReader(registerBody))
	if err != nil {
		t.Fatal(err)
	}
	var registered struct{ AccessToken, RefreshToken string }
	_ = json.NewDecoder(res.Body).Decode(&registered)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || registered.AccessToken == "" || registered.RefreshToken == "" {
		t.Fatalf("register status=%d response=%+v", res.StatusCode, registered)
	}

	wrong, _ := publicHTTPPost(t, ts.URL+"/v2/auth/password-login", "application/json", strings.NewReader(fmt.Sprintf(`{"phone":%q,"password":"wrong-pass"}`, phone)))
	if wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d", wrong.StatusCode)
	}
	_ = wrong.Body.Close()
	unknown, _ := publicHTTPPost(t, ts.URL+"/v2/auth/password-login", "application/json", strings.NewReader(`{"phone":"13900009999","password":"wrong-pass"}`))
	if unknown.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown account enumeration status=%d", unknown.StatusCode)
	}
	_ = unknown.Body.Close()
	correct, _ := publicHTTPPost(t, ts.URL+"/v2/auth/password-login", "application/json", strings.NewReader(fmt.Sprintf(`{"phone":%q,"password":"StrongPass123!"}`, phone)))
	if correct.StatusCode != http.StatusOK {
		t.Fatalf("password login status=%d", correct.StatusCode)
	}
	_ = correct.Body.Close()

	resetCode, _ := publicHTTPPost(t, ts.URL+"/v2/auth/password/reset-code", "application/json", strings.NewReader(fmt.Sprintf(`{"phone":%q}`, phone)))
	if resetCode.StatusCode != http.StatusAccepted {
		t.Fatalf("reset code status=%d", resetCode.StatusCode)
	}
	_ = resetCode.Body.Close()
	reset, _ := publicHTTPPost(t, ts.URL+"/v2/auth/password/reset", "application/json", strings.NewReader(fmt.Sprintf(`{"phone":%q,"code":"654321","password":"ChangedPass456!"}`, phone)))
	if reset.StatusCode != http.StatusNoContent {
		t.Fatalf("reset status=%d", reset.StatusCode)
	}
	_ = reset.Body.Close()
	oldPassword, _ := publicHTTPPost(t, ts.URL+"/v2/auth/password-login", "application/json", strings.NewReader(fmt.Sprintf(`{"phone":%q,"password":"StrongPass123!"}`, phone)))
	if oldPassword.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password status=%d", oldPassword.StatusCode)
	}
	_ = oldPassword.Body.Close()
	newPassword, _ := publicHTTPPost(t, ts.URL+"/v2/auth/password-login", "application/json", strings.NewReader(fmt.Sprintf(`{"phone":%q,"password":"ChangedPass456!"}`, phone)))
	if newPassword.StatusCode != http.StatusOK {
		t.Fatalf("new password status=%d", newPassword.StatusCode)
	}
	_ = newPassword.Body.Close()
}

func TestAnnouncementLifecycleTargetingAndReadReceipt(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	userToken := loginToken(t, ts.URL, "13800000001")
	adminToken := adminTestToken(t, cfg.JWTSecret)

	create := `{"title":"Targeted update","content":"Only Alice should see this","status":"draft","pinned":true,"targetType":"users","targetUserIds":["usr_alice"],"pushOnPublish":false,"reason":"create targeted operations notice","confirmed":true}`
	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/admin/announcements", adminToken, create)
	var item model.Announcement
	_ = json.NewDecoder(res.Body).Decode(&item)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || item.ID == "" {
		t.Fatalf("create status=%d item=%+v", res.StatusCode, item)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/announcements", userToken, "")
	var list struct {
		Items []model.Announcement `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&list)
	_ = res.Body.Close()
	if len(list.Items) != 0 {
		t.Fatalf("draft leaked: %+v", list.Items)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/admin/announcements/"+item.ID+"/publish", adminToken, `{"enqueuePush":true,"reason":"publish approved notice","confirmed":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("publish status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/announcements", userToken, "")
	list.Items = nil
	_ = json.NewDecoder(res.Body).Decode(&list)
	_ = res.Body.Close()
	if len(list.Items) != 1 || !list.Items[0].Pinned {
		t.Fatalf("published list=%+v", list.Items)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/announcements/"+item.ID+"/read", userToken, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("read status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/announcements", userToken, "")
	list.Items = nil
	_ = json.NewDecoder(res.Body).Decode(&list)
	_ = res.Body.Close()
	if len(list.Items) != 1 || list.Items[0].ReadAt == nil {
		t.Fatalf("read receipt missing: %+v", list.Items)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/admin/announcements/"+item.ID+"/withdraw", adminToken, `{"reason":"notice is no longer active","confirmed":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("withdraw status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/announcements", userToken, "")
	list.Items = nil
	_ = json.NewDecoder(res.Body).Decode(&list)
	_ = res.Body.Close()
	if len(list.Items) != 0 {
		t.Fatalf("withdrawn leaked: %+v", list.Items)
	}
}

func TestAdminRuntimeSettingsValidateAuditAndNeverExposeSecrets(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: strings.Repeat("s", 32), DevMode: true, DevOTPCode: "654321", MediaMaxBytes: 25 << 20, AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour, CallInviteTTL: 45 * time.Second, DatabaseURL: "postgres://secret-database", RedisURL: "redis://secret-redis", S3Endpoint: "storage", S3AccessKey: "secret-access", S3SecretKey: "secret-storage", GetuiAppID: "app", GetuiAppKey: "key", GetuiMasterSecret: "secret-getui-master", PushProvider: "getui", LiveKitEnabled: true, LiveKitURL: "wss://livekit.example.test", LiveKitAPIURL: "https://livekit-api.example.test", LiveKitAPIKey: "livekit-key", LiveKitAPISecret: "secret-livekit-credential-at-least-32-bytes", LiveKitTokenTTL: 5 * time.Minute}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	adminToken := adminTestToken(t, cfg.JWTSecret)
	res := authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/admin/settings", adminToken, "")
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	for _, secret := range []string{"secret-database", "secret-redis", "secret-access", "secret-storage", "secret-getui-master", "secret-livekit-credential-at-least-32-bytes"} {
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
	if status["pushProvider"] != true || status["liveKit"] != true || infra["mediaMaxSizeMB"] != float64(25) || infra["callInviteTimeoutSeconds"] != float64(45) {
		t.Fatalf("status=%v infrastructure=%v", status, infra)
	}
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v2/admin/settings", adminToken, `{"passwordMinLength":12,"maxMessageTextLength":8000,"messageRecallMinutes":5,"maxGroupMembers":1000,"friendRequestExpiryDays":10,"allowFriendRequests":false,"allowSearchByHandle":true,"allowSearchByPhone":true,"announcementPushEnabled":false,"callsEnabled":true,"videoCallsEnabled":false,"sensitiveWordEnabled":true,"reportSlaHours":12,"reason":"publish runtime policy update","confirmed":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update settings status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	updated := a.Settings()
	if updated["passwordMinLength"] != float64(12) || updated["allowFriendRequests"] != false || updated["allowSearchByHandle"] != true || updated["allowSearchByPhone"] != true || updated["videoCallsEnabled"] != false {
		t.Fatalf("updated settings=%v", updated)
	}
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v2/admin/settings", adminToken, `{"messageRecallMinutes":0,"reason":"validate invalid policy rejection","confirmed":true}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid setting status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestAdminGroupMemberModerationRequiresReasonAndProtectsOwner(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	group, err := a.CreateGroup("usr_alice", "后台治理群", []string{"usr_bob"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: strings.Repeat("g", 32)}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	adminToken := adminTestToken(t, cfg.JWTSecret)

	path := ts.URL + "/v2/admin/groups/" + group.ID + "/members/usr_bob"
	res := authenticatedRequest(t, http.MethodPatch, path, adminToken, `{"action":"role","role":"admin"}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing confirmation status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodPatch, path, adminToken, `{"action":"role","role":"admin","reason":"运营工单 GROUP-2","confirmed":true}`)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("role status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	members, _ := a.GroupMembers("usr_alice", group.ID)
	bobRole := ""
	for _, member := range members {
		if member.UserID == "usr_bob" {
			bobRole = member.Role
			break
		}
	}
	if len(members) != 2 || bobRole != "admin" {
		t.Fatalf("members after role=%+v", members)
	}
	ownerPath := ts.URL + "/v2/admin/groups/" + group.ID + "/members/usr_alice"
	res = authenticatedRequest(t, http.MethodDelete, ownerPath, adminToken, `{"reason":"不得移除群主","confirmed":true}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("owner removal status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodDelete, path, adminToken, `{"reason":"确认移出违规成员","confirmed":true}`)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("remove status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	members, _ = a.GroupMembers("usr_alice", group.ID)
	if len(members) != 1 || members[0].UserID != "usr_alice" {
		t.Fatalf("members after removal=%+v", members)
	}
}

type conversationListItem struct {
	Conversation struct {
		ID string `json:"id"`
	} `json:"conversation"`
	Membership struct {
		Pinned             bool `json:"pinned"`
		Saved              bool `json:"saved"`
		NotificationsMuted bool `json:"notificationsMuted"`
		ManualUnread       bool `json:"manualUnread"`
	} `json:"membership"`
}

func listConversations(t *testing.T, base, token string) []conversationListItem {
	t.Helper()
	res := authenticatedRequest(t, http.MethodGet, base+"/v2/channels/conversations", token, "")
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

func adminTestToken(t *testing.T, secret string) string {
	t.Helper()
	token, err := (auth.Manager{Secret: []byte(secret)}).IssueAdmin("test-admin", "platform_admin", time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func postgresAdminTestToken(t *testing.T, repository *store.Postgres, secret string) string {
	t.Helper()
	ctx := context.Background()
	hash, err := bcrypt.GenerateFromPassword([]byte("PostgresAdminTest123!"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	account, err := repository.CreateAdminAccount(ctx, store.AdminAccountCreate{
		ID: "test-admin", Email: "test-admin@example.invalid", DisplayName: "Postgres Test Admin",
		PasswordHash: string(hash), RoleID: "platform_admin", CreatedBy: "test", At: time.Now().UTC(),
	})
	if errors.Is(err, store.ErrConflict) {
		account, err = repository.AdminAccountByID(ctx, "test-admin")
		if err == nil {
			role, status := "platform_admin", "active"
			account, err = repository.UpdateAdminAccount(ctx, store.AdminAccountUpdate{ID: account.ID, ActorID: account.ID, RoleID: &role, Status: &status, At: time.Now().UTC()})
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	token, err := (auth.Manager{Secret: []byte(secret)}).IssueAdmin(account.ID, account.RoleID, time.Hour, account.AuthVersion)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func adminKeyRequest(t *testing.T, method, url, token, body string) *http.Response {
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
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: strings.Repeat("s", 32), DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	body, _ := json.Marshal(map[string]string{"phone": "13800000001", "code": "654321"})
	res, _ := publicHTTPPost(t, ts.URL+"/v2/auth/login", "application/json", bytes.NewReader(body))
	var first struct{ AccessToken, RefreshToken string }
	_ = json.NewDecoder(res.Body).Decode(&first)
	_ = res.Body.Close()
	raw, _ := json.Marshal(map[string]string{"refreshToken": first.RefreshToken})
	res, _ = publicHTTPPost(t, ts.URL+"/v2/auth/refresh", "application/json", bytes.NewReader(raw))
	var second struct{ AccessToken, RefreshToken string }
	_ = json.NewDecoder(res.Body).Decode(&second)
	_ = res.Body.Close()
	if second.RefreshToken == "" {
		t.Fatal("refresh rotation failed")
	}
	res, _ = publicHTTPPost(t, ts.URL+"/v2/auth/refresh", "application/json", bytes.NewReader(raw))
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused refresh status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	logoutRaw, _ := json.Marshal(map[string]string{"refreshToken": second.RefreshToken})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v2/auth/logout", bytes.NewReader(logoutRaw))
	req.Header.Set("authorization", "Bearer "+second.AccessToken)
	req.Header.Set("content-type", "application/json")
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res, _ = publicHTTPPost(t, ts.URL+"/v2/auth/refresh", "application/json", bytes.NewReader(logoutRaw))
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("logged out refresh status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	adminRaw, _ := json.Marshal(map[string]string{"email": "admin@example.com", "password": "correct horse battery staple"})
	res, _ = http.Post(ts.URL+"/v2/admin/auth/login", "application/json", bytes.NewReader(adminRaw))
	var admin struct{ AccessToken string }
	_ = json.NewDecoder(res.Body).Decode(&admin)
	_ = res.Body.Close()
	if admin.AccessToken == "" {
		t.Fatal("admin login failed")
	}
	support, _ := New(cfg, a).auth.IssueAdmin("support-1", "support", time.Hour, 1)
	banReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/v2/admin/users/usr_bob/ban", strings.NewReader(`{"reason":"x"}`))
	banReq.Header.Set("authorization", "Bearer "+support)
	banReq.Header.Set("content-type", "application/json")
	res, _ = http.DefaultClient.Do(banReq)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("support mutation status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/admin/users/usr_bob/ban", admin.AccessToken, `{}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("ban without reason status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/admin/users/usr_bob/ban", admin.AccessToken, `{"reason":"abuse investigation","durationHours":24,"confirmed":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("timed ban status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestAdminUserManagementReturnsRealRelationsDevicesAndRejectsUnavailableSystemMessage(t *testing.T) {
	a, err := app.New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	request, err := a.RequestFriend("usr_alice", "usr_bob", "后台关系接口测试")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.AcceptFriend("usr_bob", request.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.Block("usr_alice", "usr_admin", true); err != nil {
		t.Fatal(err)
	}
	if _, err = a.RegisterDevice("usr_alice", store.Device{ID: "device_android_1", Platform: "android", Provider: "fcm", PushToken: "private-token", NotificationsEnabled: true, SoundEnabled: true, VibrationEnabled: true}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: strings.Repeat("s", 32), DevMode: true, AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	adminKey := adminTestToken(t, cfg.JWTSecret)
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()

	res := adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/users", adminKey, `{"phone":"13800009999","name":"后台新增用户","password":"StrongPass123!","gender":"female","reason":"运营工单 USER-1","confirmed":true}`)
	if res.StatusCode != http.StatusCreated {
		_ = res.Body.Close()
		t.Fatalf("create admin user status=%d", res.StatusCode)
	}
	var created struct {
		Item model.User `json:"item"`
	}
	if err = json.NewDecoder(res.Body).Decode(&created); err != nil {
		_ = res.Body.Close()
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if created.Item.Phone != "13800009999" || created.Item.Gender != "female" || created.Item.Handle == "" {
		t.Fatalf("created admin user=%+v", created.Item)
	}
	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/users", adminKey, `{"phone":"13800009999","name":"重复手机号","password":"StrongPass123!","gender":"female","reason":"重复校验","confirmed":true}`)
	if res.StatusCode != http.StatusConflict {
		_ = res.Body.Close()
		t.Fatalf("duplicate admin user status=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/users", adminKey, `{"phone":"12800009999","name":"错误手机号","password":"StrongPass123!","gender":"female","reason":"校验","confirmed":true}`)
	if res.StatusCode != http.StatusBadRequest {
		_ = res.Body.Close()
		t.Fatalf("invalid mainland phone status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	checks := []struct{ path, id string }{
		{path: "/v2/admin/users/usr_alice/friends", id: "usr_bob"},
		{path: "/v2/admin/users/usr_alice/blocks", id: "usr_admin"},
	}
	for _, check := range checks {
		res := adminKeyRequest(t, http.MethodGet, ts.URL+check.path, adminKey, "")
		if res.StatusCode != http.StatusOK {
			_ = res.Body.Close()
			t.Fatalf("%s status=%d", check.path, res.StatusCode)
		}
		var payload struct {
			Items []map[string]any `json:"items"`
		}
		if err = json.NewDecoder(res.Body).Decode(&payload); err != nil {
			_ = res.Body.Close()
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if len(payload.Items) != 1 {
			t.Fatalf("%s items=%v", check.path, payload.Items)
		}
		user, _ := payload.Items[0]["user"].(map[string]any)
		if user["id"] != check.id {
			t.Fatalf("%s nested user=%v", check.path, payload.Items[0]["user"])
		}
	}
	res = adminKeyRequest(t, http.MethodGet, ts.URL+"/v2/admin/users/usr_alice/devices", adminKey, "")
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		t.Fatalf("devices status=%d", res.StatusCode)
	}
	var devices struct {
		Items             []map[string]any `json:"items"`
		PushRegistrations []map[string]any `json:"pushRegistrations"`
	}
	if err = json.NewDecoder(res.Body).Decode(&devices); err != nil {
		_ = res.Body.Close()
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if len(devices.Items) != 0 || len(devices.PushRegistrations) != 1 || devices.PushRegistrations[0]["id"] != "device_android_1" {
		t.Fatalf("admin device response=%+v", devices)
	}
	if _, leaked := devices.PushRegistrations[0]["pushToken"]; leaked {
		t.Fatal("admin device response leaked push token")
	}

	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/users/usr_alice/system-message", adminKey, `{"senderUid":"usr_admin","content":"版本升级通知","reason":"运营工单 OPS-18","confirmed":true}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("system message without WuKongIM status=%d", res.StatusCode)
	}
}

func TestAuthenticationDoesNotFallbackAndBanBlocksRefreshAndAPI(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: strings.Repeat("s", 32), DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()

	bad, _ := json.Marshal(map[string]string{"phone": "13800000001", "code": "000000"})
	res, err := publicHTTPPost(t, ts.URL+"/v2/auth/login", "application/json", bytes.NewReader(bad))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad OTP status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	body, _ := json.Marshal(map[string]string{"phone": "13800000001", "code": "654321"})
	res, err = publicHTTPPost(t, ts.URL+"/v2/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var tokens struct{ AccessToken, RefreshToken string }
	if err = json.NewDecoder(res.Body).Decode(&tokens); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if err = a.AdminBan("admin", "usr_alice", true, 24, "test"); err != nil {
		t.Fatal(err)
	}

	refreshBody, _ := json.Marshal(map[string]string{"refreshToken": tokens.RefreshToken})
	res, err = publicHTTPPost(t, ts.URL+"/v2/auth/refresh", "application/json", bytes.NewReader(refreshBody))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("banned refresh status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/users/me", tokens.AccessToken, "")
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("banned API status=%d", res.StatusCode)
	}
}

func TestAccessTokensAreRejectedFromURLsAndLegacyWebSocketIsAbsent(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: strings.Repeat("s", 32), DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	token := loginToken(t, ts.URL, "13800000001")

	res, err := http.Get(ts.URL + "/v2/users/me?token=" + url.QueryEscape(token))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("REST query token status=%d", res.StatusCode)
	}

	request, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/ws", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy websocket status=%d", response.StatusCode)
	}
}
func publicHTTPPost(t *testing.T, target, contentType string, body io.Reader) (*http.Response, error) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, target, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-Client-Platform", "android")
	return http.DefaultClient.Do(request)
}

func publicPlatformPost(t *testing.T, target, platform, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Client-Platform", platform)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestWebQRLoginRequiresMobileConfirmationAndConsumesTicketOnce(t *testing.T) {
	a, err := app.New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	mobileToken := loginToken(t, ts.URL, "13800000001")

	create := publicPlatformPost(t, ts.URL+"/v2/auth/qr/create", "web", `{"clientName":"Edge · Windows"}`)
	defer create.Body.Close()
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d", create.StatusCode)
	}
	var ticket struct {
		ID, QRPayload, PollToken string
	}
	if err = json.NewDecoder(create.Body).Decode(&ticket); err != nil {
		t.Fatal(err)
	}
	if ticket.ID == "" || ticket.PollToken == "" || !strings.HasPrefix(ticket.QRPayload, "qingwaguagua://login/ql_") || strings.Contains(ticket.QRPayload, ticket.PollToken) {
		t.Fatalf("unsafe ticket response=%+v", ticket)
	}
	qrToken := strings.TrimPrefix(ticket.QRPayload, "qingwaguagua://login/")

	pending := publicPlatformPost(t, ts.URL+"/v2/auth/qr/poll", "web", fmt.Sprintf(`{"id":%q,"pollToken":%q}`, ticket.ID, ticket.PollToken))
	if pending.StatusCode != http.StatusAccepted {
		t.Fatalf("pending poll status=%d", pending.StatusCode)
	}
	pending.Body.Close()

	inspect := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/auth/qr/inspect", mobileToken, fmt.Sprintf(`{"token":%q}`, qrToken))
	if inspect.StatusCode != http.StatusOK {
		t.Fatalf("inspect status=%d", inspect.StatusCode)
	}
	var details struct{ ClientName, Status string }
	_ = json.NewDecoder(inspect.Body).Decode(&details)
	inspect.Body.Close()
	if details.ClientName != "Edge · Windows" || details.Status != "pending" {
		t.Fatalf("inspect details=%+v", details)
	}

	confirm := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/auth/qr/confirm", mobileToken, fmt.Sprintf(`{"token":%q}`, qrToken))
	if confirm.StatusCode != http.StatusOK {
		t.Fatalf("confirm status=%d", confirm.StatusCode)
	}
	confirm.Body.Close()

	claim := publicPlatformPost(t, ts.URL+"/v2/auth/qr/poll", "web", fmt.Sprintf(`{"id":%q,"pollToken":%q}`, ticket.ID, ticket.PollToken))
	if claim.StatusCode != http.StatusOK {
		t.Fatalf("claim status=%d", claim.StatusCode)
	}
	var session struct {
		AccessToken, RefreshToken string
		User                      model.User
	}
	if err = json.NewDecoder(claim.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	claim.Body.Close()
	if session.AccessToken == "" || session.RefreshToken == "" || session.User.ID == "" {
		t.Fatalf("incomplete QR login session=%+v", session)
	}

	replay := publicPlatformPost(t, ts.URL+"/v2/auth/qr/poll", "web", fmt.Sprintf(`{"id":%q,"pollToken":%q}`, ticket.ID, ticket.PollToken))
	defer replay.Body.Close()
	if replay.StatusCode != http.StatusConflict {
		t.Fatalf("replay status=%d", replay.StatusCode)
	}
}

func TestRobotMenusAreAdminConfiguredAndLimitedToConversationMembers(t *testing.T) {
	repository := &robotAPITestStore{}
	a, err := app.New(context.Background(), repository)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	conversation, err := a.DirectConversation("usr_alice", "usr_bob")
	if err != nil {
		t.Fatal(err)
	}
	repository.conversationID = conversation.ID
	cfg := config.Config{
		JWTSecret: "robot-test-secret", DevMode: true, DevOTPCode: "654321",
		AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
	}
	adminKey := adminTestToken(t, cfg.JWTSecret)
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()

	configure := adminKeyRequest(t, http.MethodPut, ts.URL+"/v2/admin/wukong/robots/usr_bob", adminKey, `{"enabled":true,"username":"qing_helper","placeholder":"请选择服务","menus":[{"cmd":"查询订单","remark":"查询订单","type":"command"},{"cmd":"联系客服","remark":"联系客服","type":"command"}],"reason":"发布客服菜单","confirmed":true}`)
	defer configure.Body.Close()
	if configure.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(configure.Body)
		t.Fatalf("configure status=%d body=%s", configure.StatusCode, raw)
	}

	aliceToken := loginToken(t, ts.URL, "13800000001")
	menus := authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/robots/conversations/"+conversation.ID, aliceToken, "")
	defer menus.Body.Close()
	if menus.StatusCode != http.StatusOK {
		t.Fatalf("menus status=%d", menus.StatusCode)
	}
	var payload struct {
		Items []struct {
			RobotID string `json:"robot_id"`
			Status  int    `json:"status"`
			Version int64  `json:"version"`
			Menus   []struct {
				Command string `json:"cmd"`
				Remark  string `json:"remark"`
				Type    string `json:"type"`
			} `json:"menus"`
		} `json:"items"`
	}
	if err = json.NewDecoder(menus.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || payload.Items[0].RobotID != "usr_bob" || payload.Items[0].Status != 1 || payload.Items[0].Version != 1 || len(payload.Items[0].Menus) != 2 || payload.Items[0].Menus[0].Command != "查询订单" {
		t.Fatalf("robot payload=%+v", payload.Items)
	}

	invalid := adminKeyRequest(t, http.MethodPut, ts.URL+"/v2/admin/wukong/robots/usr_bob", adminKey, `{"enabled":true,"menus":[{"cmd":"重复","remark":"一","type":"command"},{"cmd":"重复","remark":"二","type":"command"}],"reason":"拒绝重复命令","confirmed":true}`)
	defer invalid.Body.Close()
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate menu status=%d", invalid.StatusCode)
	}

	outsiderToken := loginToken(t, ts.URL, "13800000003")
	forbidden := authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/robots/conversations/"+conversation.ID, outsiderToken, "")
	defer forbidden.Body.Close()
	if forbidden.StatusCode != http.StatusNotFound {
		t.Fatalf("outsider status=%d", forbidden.StatusCode)
	}
}

func TestAuthAndProfilePhoneValidationIsConsistent(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	token := loginToken(t, ts.URL, "13800000001")

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		token  string
	}{
		{name: "request login code", method: http.MethodPost, path: "/v2/auth/code", body: `{"phone":"abc123"}`},
		{name: "code login", method: http.MethodPost, path: "/v2/auth/login", body: `{"phone":"abc123","code":"654321"}`},
		{name: "register", method: http.MethodPost, path: "/v2/auth/register", body: `{"phone":"abc123","code":"654321","password":"password123","name":"测试用户"}`},
		{name: "password login", method: http.MethodPost, path: "/v2/auth/password-login", body: `{"phone":"abc123","password":"password123"}`},
		{name: "request password reset code", method: http.MethodPost, path: "/v2/auth/password/reset-code", body: `{"phone":"abc123"}`},
		{name: "reset password", method: http.MethodPost, path: "/v2/auth/password/reset", body: `{"phone":"abc123","code":"654321","password":"password123"}`},
		{name: "request phone change code", method: http.MethodPost, path: "/v2/users/me/phone/code", body: `{"phone":"abc123"}`, token: token},
		{name: "change phone", method: http.MethodPatch, path: "/v2/users/me/phone", body: `{"phone":"abc123","code":"654321"}`, token: token},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var response *http.Response
			if test.token == "" {
				request, err := http.NewRequest(test.method, ts.URL+test.path, strings.NewReader(test.body))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-Client-Platform", "android")
				response, err = http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				response = authenticatedRequest(t, test.method, ts.URL+test.path, test.token, test.body)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d, want %d", response.StatusCode, http.StatusBadRequest)
			}
			var payload struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Error.Code != "INVALID_ARGUMENT" {
				t.Fatalf("code=%q, want INVALID_ARGUMENT", payload.Error.Code)
			}
		})
	}
}

func TestPublicAuthPolicyMatchesRuntimeSettings(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	if err := a.UpdateSettings("admin", map[string]any{
		"registrationEnabled": false,
		"passwordMinLength":   float64(14),
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: "test-secret", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()

	response, err := http.Get(ts.URL + "/v2/config/auth")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var policy app.PublicAuthPolicy
	if err = json.NewDecoder(response.Body).Decode(&policy); err != nil {
		t.Fatal(err)
	}
	if policy.RegistrationEnabled || policy.PasswordMinLength != 14 || policy.PasswordMaxBytes != 72 {
		t.Fatalf("policy=%+v", policy)
	}
}

func loginToken(t *testing.T, base, phone string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"phone": phone, "code": "654321"})
	res, err := publicHTTPPost(t, base+"/v2/auth/login", "application/json", bytes.NewReader(body))
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

func TestProfileHandleSearchCapabilitiesAndGroupMemberContract(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	bob := loginToken(t, ts.URL, "13800000002")
	res := authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/users/me", alice, `{"handle":"official_support"}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("reserved handle status=%d", res.StatusCode)
	}
	res.Body.Close()

	for i, handle := range []string{"alice_one", "alice_two"} {
		res := authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/users/me", alice, `{"handle":"`+handle+`"}`)
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
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/users/me", alice, `{"handle":"alice_three"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("third handle update status=%d", res.StatusCode)
	}
	res.Body.Close()

	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/contacts/search/capabilities", alice, "")
	var caps map[string]bool
	_ = json.NewDecoder(res.Body).Decode(&caps)
	res.Body.Close()
	if !caps["allowSearchByHandle"] || caps["allowSearchByPhone"] || !caps["canUpdatePrivacySettings"] {
		t.Fatalf("capabilities=%v", caps)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/contacts/search?q=13800000002&by=phone", alice, "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("disabled phone search status=%d", res.StatusCode)
	}
	res.Body.Close()

	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/users/me", bob, `{"handle":"bob_public"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bob handle update status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/contacts/search?q=bob_public&by=handle", alice, "")
	var search struct {
		Items []model.User `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&search)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(search.Items) != 1 || search.Items[0].Phone != "" || search.Items[0].Handle != "bob_public" {
		t.Fatalf("handle search status=%d items=%+v", res.StatusCode, search.Items)
	}
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/users/me", bob, `{"allowSearchByHandle":false}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("disable handle discovery status=%d", res.StatusCode)
	}
	var privacyProfile model.User
	_ = json.NewDecoder(res.Body).Decode(&privacyProfile)
	res.Body.Close()
	if privacyProfile.AllowSearchByHandle {
		t.Fatalf("handle discovery remained enabled: %+v", privacyProfile)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/contacts/search?q=bob_public&by=handle", alice, "")
	search.Items = nil
	_ = json.NewDecoder(res.Body).Decode(&search)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(search.Items) != 0 {
		t.Fatalf("private handle remained searchable status=%d items=%+v", res.StatusCode, search.Items)
	}

	group, err := a.CreateGroup("usr_alice", "Neighbors", []string{"usr_bob"})
	if err != nil {
		t.Fatal(err)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/channels/groups/"+group.ID+"/members", alice, "")
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
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/channels/conversations", alice, "")
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
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	installHTTPTestWukongRuntime(a)
	mediaID := "med_secure"
	if err := a.CreateMedia(store.Media{ID: mediaID, OwnerID: "usr_alice", ObjectKey: "users/alice/media.jpg", MIME: "image/jpeg", Size: 10, Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := a.CompleteMedia(mediaID, "usr_alice", 10, "checksum"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	api := New(cfg, a)
	api.media = signedMediaService{}
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()
	alice, bob, outsider := loginToken(t, ts.URL, "13800000001"), loginToken(t, ts.URL, "13800000002"), loginToken(t, ts.URL, "13800000000")
	cid := directConversation(t, ts.URL, alice, "usr_bob")
	sendURL := ts.URL + "/v2/messages/conversations/" + cid + "/send"
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
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/messages/conversations/"+cid+"/history", bob, "")
	var history struct {
		Items []model.Message `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&history)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(history.Items) != 1 || history.Items[0].Body["downloadUrl"] == nil {
		t.Fatalf("history status=%d items=%+v", res.StatusCode, history.Items)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for name, token := range map[string]string{"owner": alice, "member": bob} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v2/media/"+mediaID, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err = client.Do(req)
		if err != nil || res.StatusCode != http.StatusTemporaryRedirect || !strings.Contains(res.Header.Get("Location"), "X-Amz-Signature") {
			t.Fatalf("%s download status=%d location=%q err=%v", name, res.StatusCode, res.Header.Get("Location"), err)
		}
		res.Body.Close()
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v2/media/"+mediaID, nil)
	req.Header.Set("Authorization", "Bearer "+outsider)
	res, err = client.Do(req)
	if err != nil || res.StatusCode != http.StatusNotFound {
		t.Fatalf("outsider status=%d err=%v", res.StatusCode, err)
	}
	res.Body.Close()
}

func TestAccountDeletionRequiresOTPAndResolvedGroupOwnership(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	group, err := a.CreateGroup("usr_alice", "Owned Group", []string{"usr_bob"})
	if err != nil {
		t.Fatal(err)
	}
	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/users/me/deletion/code", alice, `{}`)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("deletion code status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v2/users/me", alice, `{"code":"000000"}`)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid deletion code status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v2/users/me", alice, `{"code":"654321"}`)
	var conflictBody map[string]any
	_ = json.NewDecoder(res.Body).Decode(&conflictBody)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict || !strings.Contains(fmt.Sprint(conflictBody), "GROUP_OWNERSHIP_REQUIRED") {
		t.Fatalf("ownership conflict status=%d body=%v", res.StatusCode, conflictBody)
	}
	if err = a.DisbandGroup("usr_alice", group.ID, "account deletion"); err != nil {
		t.Fatal(err)
	}
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v2/users/me", alice, `{"code":"654321"}`)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d", res.StatusCode)
	}
	res.Body.Close()
	deleted, err := a.User("usr_alice")
	if err != nil || !deleted.Banned || deleted.DeletedAt == nil || deleted.Phone == "13800000001" || deleted.Handle == "alice_two" || deleted.Name != "已注销用户" {
		t.Fatalf("deleted profile=%+v err=%v", deleted, err)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/users/me", alice, "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("deleted account still active status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v2/users/me", alice, `{}`)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("idempotent delete status=%d", res.StatusCode)
	}
	res.Body.Close()
}

func TestContactAndLocationMessageContracts(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	installHTTPTestWukongRuntime(a)
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	cid := directConversation(t, ts.URL, alice, "usr_bob")
	sendURL := ts.URL + "/v2/messages/conversations/" + cid + "/send"

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
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	ownerToken, memberToken, inviteeToken := loginToken(t, ts.URL, phones[0]), loginToken(t, ts.URL, phones[1]), loginToken(t, ts.URL, phones[2])

	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/channels/groups", ownerToken, fmt.Sprintf(`{"name":"API Group","memberIds":[%q]}`, users[1].ID))
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
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/channels/groups/"+group.ID, memberToken, `{"name":"Denied"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("member profile update status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/channels/groups/"+group.ID, ownerToken, fmt.Sprintf(`{"name":"Renamed API Group","avatarMediaId":%q,"joinPolicy":"qr","allowMemberAddFriend":false,"rotateQR":true}`, avatarID))
	var profile model.GroupProfile
	_ = json.NewDecoder(res.Body).Decode(&profile)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || profile.QRToken == "" || profile.AllowMemberAddFriend || strings.Contains(profile.QRToken, phones[0]) || !strings.HasPrefix(profile.AvatarURL, "/v2/avatars/"+avatarID+"?") {
		t.Fatalf("profile status=%d value=%+v", res.StatusCode, profile)
	}
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v2/channels/groups/"+group.ID+"/announcement", ownerToken, `{"content":"Welcome"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("announcement status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/channels/groups/"+group.ID+"/invites", ownerToken, fmt.Sprintf(`{"userId":%q}`, users[2].ID))
	var inviteResponse struct {
		Invite model.GroupInvite `json:"invite"`
	}
	_ = json.NewDecoder(res.Body).Decode(&inviteResponse)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || inviteResponse.Invite.ID == "" {
		t.Fatalf("invite status=%d value=%+v", res.StatusCode, inviteResponse)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/channels/group-invitations?status=pending", inviteeToken, "")
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
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/channels/group-invitations/"+inviteResponse.Invite.ID+"/accept", inviteeToken, `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("accept status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/channels/groups/"+group.ID+"/members", inviteeToken, "")
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
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/messages/conversations/"+group.ID+"/history", ownerToken, "")
	var history struct {
		Items []model.Message `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&history)
	res.Body.Close()
	foundSystem := false
	for _, message := range history.Items {
		foundSystem = foundSystem || message.Type == "system"
	}
	if foundSystem {
		t.Fatalf("group operation leaked a system body into the legacy history endpoint: %+v", history.Items)
	}
}

func TestSupportRESTWorkflowPostgres(t *testing.T) {
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
	phones := []string{"185" + suffix, "186" + suffix, "187" + suffix, "188" + suffix}
	users := make([]*model.User, len(phones))
	for index, phone := range phones {
		users[index], err = a.Login(phone, fmt.Sprintf("Support API %d", index))
		if err != nil {
			t.Fatal(err)
		}
	}
	skill, err := a.SaveSupportSkillGroup(ctx, "admin-test", store.SupportSkillGroupInput{
		Name: "订单咨询", RoutingStrategy: "least_active", MaxConcurrentPerAgent: 5, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.SaveSupportAgent(ctx, store.SupportAgentInput{
		UserID: users[1].ID, Status: "available", MaxConcurrent: 5, SkillGroupIDs: []string{skill.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.SaveSupportAgent(ctx, store.SupportAgentInput{
		UserID: users[2].ID, Status: "busy", MaxConcurrent: 5, SkillGroupIDs: []string{skill.ID},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	visitorToken := loginToken(t, ts.URL, phones[0])
	agentToken := loginToken(t, ts.URL, phones[1])
	targetToken := loginToken(t, ts.URL, phones[2])
	outsiderToken := loginToken(t, ts.URL, phones[3])

	res := authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/support/skills", visitorToken, "")
	var skillList struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&skillList)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(skillList.Items) == 0 {
		t.Fatalf("support skills status=%d items=%v", res.StatusCode, skillList.Items)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/support/sessions", visitorToken,
		fmt.Sprintf(`{"skillGroupId":%q,"subject":"订单退款","metadata":{"orderId":"api-order"}}`, skill.ID))
	var created struct {
		Item    map[string]any `json:"item"`
		Created bool           `json:"created"`
	}
	_ = json.NewDecoder(res.Body).Decode(&created)
	res.Body.Close()
	sessionID, _ := created.Item["id"].(string)
	if res.StatusCode != http.StatusCreated || !created.Created || sessionID == "" ||
		created.Item["channelType"] != float64(wukong.ChannelVisitor) || created.Item["assignedAgentId"] != users[1].ID {
		t.Fatalf("create support status=%d body=%v", res.StatusCode, created)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/support/sessions/"+sessionID, outsiderToken, "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("outsider support session status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/support/agents?skillGroupId="+url.QueryEscape(skill.ID), agentToken, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("support agents status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/support/sessions/"+sessionID+"/transfer", agentToken,
		fmt.Sprintf(`{"targetAgentId":%q}`, users[2].ID))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("support transfer status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/support/sessions/"+sessionID+"/end", targetToken, `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("support end status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/support/sessions/"+sessionID+"/rating", visitorToken, `{"rating":5,"comment":"满意"}`)
	var rated struct {
		Item map[string]any `json:"item"`
	}
	_ = json.NewDecoder(res.Body).Decode(&rated)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || rated.Item["rating"] != float64(5) {
		t.Fatalf("support rating status=%d body=%v", res.StatusCode, rated)
	}
}

func TestBusinessChannelUserTemporaryMembershipAndAccessREST(t *testing.T) {
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
	owner, err := a.Login("189"+suffix, "Channel Owner")
	if err != nil {
		t.Fatal(err)
	}
	member, err := a.Login("190"+suffix, "Channel Member")
	if err != nil {
		t.Fatal(err)
	}
	channel, err := a.CreateBusinessChannel(ctx, owner.ID, store.BusinessChannelCreate{
		ChannelType: int(wukong.ChannelInfo), Name: "Temporary News", Visibility: "public",
		JoinPolicy: "open", PostingPolicy: "operators",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	ownerToken, memberToken := loginToken(t, ts.URL, "189"+suffix), loginToken(t, ts.URL, "190"+suffix)
	expiresAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)

	res := authenticatedRequest(t, http.MethodPost,
		ts.URL+"/v2/channels/business/"+url.PathEscape(channel.ID)+"/subscribe?channelType=6", memberToken,
		fmt.Sprintf(`{"expiresAt":%q}`, expiresAt.Format(time.RFC3339)))
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("temporary subscribe status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodGet,
		ts.URL+"/v2/channels/business/"+url.PathEscape(channel.ID)+"/members?channelType=6", ownerToken, "")
	var members struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&members)
	res.Body.Close()
	foundTemporary := false
	for _, item := range members.Items {
		foundTemporary = foundTemporary || item["userId"] == member.ID && item["expiresAt"] != nil
	}
	if res.StatusCode != http.StatusOK || !foundTemporary {
		t.Fatalf("temporary members status=%d items=%v", res.StatusCode, members.Items)
	}
	res = authenticatedRequest(t, http.MethodPatch,
		ts.URL+"/v2/channels/business/"+url.PathEscape(channel.ID)+"/members/"+url.PathEscape(member.ID)+"?channelType=6", ownerToken,
		`{"clearExpiry":true}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("clear temporary expiry status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodGet,
		ts.URL+"/v2/channels/business/"+url.PathEscape(channel.ID)+"/members?channelType=6", ownerToken, "")
	members.Items = nil
	_ = json.NewDecoder(res.Body).Decode(&members)
	res.Body.Close()
	foundPermanent := false
	for _, item := range members.Items {
		foundPermanent = foundPermanent || item["userId"] == member.ID && item["expiresAt"] == nil
	}
	if res.StatusCode != http.StatusOK || !foundPermanent {
		t.Fatalf("permanent members status=%d items=%v", res.StatusCode, members.Items)
	}
	res = authenticatedRequest(t, http.MethodPut,
		ts.URL+"/v2/channels/business/"+url.PathEscape(channel.ID)+"/access/deny/"+url.PathEscape(member.ID)+"?channelType=6", ownerToken,
		`{"reason":"abuse"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("denylist status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodGet,
		ts.URL+"/v2/channels/business/"+url.PathEscape(channel.ID)+"/access?channelType=6", ownerToken, "")
	var access struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&access)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(access.Items) != 1 || access.Items[0]["accessType"] != "deny" {
		t.Fatalf("user access status=%d items=%v", res.StatusCode, access.Items)
	}
}

func TestAdminBusinessChannelsAndSupportRequireConfirmedAuditedWrites(t *testing.T) {
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
	owner, err := a.Login("191"+suffix, "Admin Channel Owner")
	if err != nil {
		t.Fatal(err)
	}
	member, err := a.Login("192"+suffix, "Admin Channel Member")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: strings.Repeat("j", 32),
		DevMode: true, DevOTPCode: "654321",
		AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	adminKey := postgresAdminTestToken(t, p, cfg.JWTSecret)
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()

	createBody := fmt.Sprintf(`{"ownerId":%q,"channelType":6,"name":"Admin News","visibility":"public","joinPolicy":"open","postingPolicy":"operators","reason":"create official news"}`, owner.ID)
	res := adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/channels", adminKey, createBody)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unconfirmed channel create status=%d", res.StatusCode)
	}
	res.Body.Close()
	createBody = strings.TrimSuffix(createBody, "}") + `,"confirmed":true}`
	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/channels", adminKey, createBody)
	var created struct {
		Item map[string]any `json:"item"`
	}
	_ = json.NewDecoder(res.Body).Decode(&created)
	res.Body.Close()
	channelID, _ := created.Item["id"].(string)
	if res.StatusCode != http.StatusCreated || channelID == "" {
		t.Fatalf("confirmed channel create status=%d body=%v", res.StatusCode, created)
	}
	temporaryExpiry := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	res = adminKeyRequest(t, http.MethodPut,
		ts.URL+"/v2/admin/channels/"+url.PathEscape(channelID)+"/members/"+url.PathEscape(member.ID)+"?channelType=6",
		adminKey, fmt.Sprintf(`{"expiresAt":%q,"reason":"temporary news subscription","confirmed":true}`, temporaryExpiry.Format(time.RFC3339)))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("add temporary channel member status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = adminKeyRequest(t, http.MethodGet, ts.URL+"/v2/admin/channels/"+url.PathEscape(channelID)+"/members?channelType=6", adminKey, "")
	var members struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&members)
	res.Body.Close()
	foundTemporary := false
	for _, item := range members.Items {
		foundTemporary = foundTemporary || item["userId"] == member.ID && item["expiresAt"] != nil
	}
	if res.StatusCode != http.StatusOK || !foundTemporary {
		t.Fatalf("admin members status=%d items=%v", res.StatusCode, members.Items)
	}
	res = adminKeyRequest(t, http.MethodPut,
		ts.URL+"/v2/admin/channels/"+url.PathEscape(channelID)+"/access/deny/"+url.PathEscape(member.ID)+"?channelType=6",
		adminKey, `{"reason":"abuse review","confirmed":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("set channel denylist status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = adminKeyRequest(t, http.MethodGet, ts.URL+"/v2/admin/channels/"+url.PathEscape(channelID)+"/access?channelType=6", adminKey, "")
	var access struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&access)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(access.Items) != 1 || access.Items[0]["accessType"] != "deny" {
		t.Fatalf("channel access status=%d items=%v", res.StatusCode, access.Items)
	}
	res = adminKeyRequest(t, http.MethodPatch, ts.URL+"/v2/admin/channels/"+url.PathEscape(channelID)+"?channelType=6", adminKey,
		`{"sendBan":true,"reason":"pause publishing","confirmed":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("channel update status=%d", res.StatusCode)
	}
	res.Body.Close()

	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/support/skills", adminKey,
		`{"name":"Billing","routingStrategy":"least_active","maxConcurrentPerAgent":5,"enabled":true,"reason":"create billing queue","confirmed":true}`)
	var skillResponse struct {
		Item map[string]any `json:"item"`
	}
	_ = json.NewDecoder(res.Body).Decode(&skillResponse)
	res.Body.Close()
	skillID, _ := skillResponse.Item["id"].(string)
	if res.StatusCode != http.StatusOK || skillID == "" {
		t.Fatalf("support skill status=%d body=%v", res.StatusCode, skillResponse)
	}
	for _, agent := range []struct{ id, status string }{{member.ID, "available"}, {owner.ID, "busy"}} {
		res = adminKeyRequest(t, http.MethodPut, ts.URL+"/v2/admin/support/agents/"+url.PathEscape(agent.id), adminKey,
			fmt.Sprintf(`{"status":%q,"maxConcurrent":5,"skillGroupIds":[%q],"reason":"configure support agent","confirmed":true}`, agent.status, skillID))
		if res.StatusCode != http.StatusOK {
			t.Fatalf("save support agent %s status=%d", agent.id, res.StatusCode)
		}
		res.Body.Close()
	}
	session, _, err := a.CreateSupportSession(ctx, owner.ID, skillID, "billing issue", int(wukong.ChannelVisitor), map[string]any{"source": "admin-test"})
	if err != nil || session.AssignedAgentID != member.ID {
		t.Fatalf("support session=%+v err=%v", session, err)
	}
	res = adminKeyRequest(t, http.MethodGet, ts.URL+"/v2/admin/support/sessions?q="+url.QueryEscape(session.ID), adminKey, "")
	var sessions struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&sessions)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(sessions.Items) != 1 {
		t.Fatalf("admin support sessions status=%d items=%v", res.StatusCode, sessions.Items)
	}
	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/support/sessions/"+url.PathEscape(session.ID)+"/transfer", adminKey,
		fmt.Sprintf(`{"targetAgentId":%q,"reason":"skill escalation","confirmed":true}`, owner.ID))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("admin support transfer status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/support/sessions/"+url.PathEscape(session.ID)+"/end", adminKey,
		`{"reason":"case resolved","confirmed":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("admin support end status=%d", res.StatusCode)
	}
	res.Body.Close()

	auditItems, _, _, err := a.AdminAuditsPage("", "", "", 200)
	if err != nil {
		t.Fatal(err)
	}
	expectedActions := map[string]bool{"channel.create": true, "channel.member.add": true, "channel.access.update": true,
		"channel.update": true, "support.skill.save": true, "support.agent.save": true,
		"support.session.transfer": true, "support.session.end": true}
	audits := 0
	requestReasonAudits := 0
	rejectedControlAudit := false
	for _, item := range auditItems {
		if item.ActorID == "test-admin" && expectedActions[item.Action] {
			audits++
		}
		if item.ActorID == "test-admin" && item.Action == "admin.request" {
			reason, _ := item.Metadata["reason"].(string)
			if item.Result == "success" && strings.TrimSpace(reason) != "" {
				requestReasonAudits++
			}
			if item.Result == "failed" && reason == "create official news" {
				rejectedControlAudit = true
			}
		}
	}
	if audits < 9 {
		t.Fatalf("admin business/support writes missing audits: %d", audits)
	}
	if requestReasonAudits < 9 || !rejectedControlAudit {
		t.Fatalf("admin request reason audits=%d rejectedControl=%v", requestReasonAudits, rejectedControlAudit)
	}
}

func TestMomentsRESTWorkflowPostgres(t *testing.T) {
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
	authorPhone, viewerPhone := "183"+suffix, "184"+suffix
	author, err := a.Login(authorPhone, "Moment Author")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := a.Login(viewerPhone, "Moment Viewer")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	adminKey := postgresAdminTestToken(t, p, cfg.JWTSecret)
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	authorToken := loginToken(t, ts.URL, authorPhone)
	viewerToken := loginToken(t, ts.URL, viewerPhone)

	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/moments", authorToken,
		`{"content":"第一条朋友圈","visibility":"public","mediaKind":"none","mediaIds":[],"visibleUserIds":[]}`)
	var created struct {
		Item map[string]any `json:"item"`
	}
	_ = json.NewDecoder(res.Body).Decode(&created)
	res.Body.Close()
	momentID, _ := created.Item["id"].(string)
	if res.StatusCode != http.StatusCreated || momentID == "" || created.Item["authorId"] != author.ID {
		t.Fatalf("create moment status=%d body=%v", res.StatusCode, created)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/moments?authorId="+url.QueryEscape(author.ID), viewerToken, "")
	var feed struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&feed)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(feed.Items) != 1 || feed.Items[0]["id"] != momentID {
		t.Fatalf("moment feed status=%d body=%v", res.StatusCode, feed)
	}
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v2/moments/"+momentID+"/like", viewerToken, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("moment like status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/moments/"+momentID+"/comments", viewerToken, `{"content":"评论内容"}`)
	var comment struct {
		Item map[string]any `json:"item"`
	}
	_ = json.NewDecoder(res.Body).Decode(&comment)
	res.Body.Close()
	commentID, _ := comment.Item["id"].(string)
	if res.StatusCode != http.StatusCreated || commentID == "" {
		t.Fatalf("moment comment status=%d body=%v", res.StatusCode, comment)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/moments/reminders", authorToken, "")
	var reminders struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&reminders)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(reminders.Items) != 2 {
		t.Fatalf("moment reminders status=%d body=%v", res.StatusCode, reminders)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/moments/reminders/read", authorToken, `{"reminderIds":[]}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("moment reminders read status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/reports", viewerToken,
		fmt.Sprintf(`{"targetType":"moment","targetId":%q,"reason":"spam","details":"test"}`, momentID))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("moment report status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = adminKeyRequest(t, http.MethodGet, ts.URL+"/v2/admin/moments?q="+url.QueryEscape(momentID), adminKey, "")
	var moderationPage struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&moderationPage)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(moderationPage.Items) != 1 || moderationPage.Items[0]["id"] != momentID {
		t.Fatalf("admin moments status=%d body=%v", res.StatusCode, moderationPage)
	}
	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/moments/"+momentID+"/moderate", adminKey, `{"status":"hidden","reason":"举报复核","confirmed":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("hide admin moment status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/moments?authorId="+url.QueryEscape(author.ID), viewerToken, "")
	feed = struct {
		Items []map[string]any `json:"items"`
	}{}
	_ = json.NewDecoder(res.Body).Decode(&feed)
	res.Body.Close()
	if len(feed.Items) != 0 {
		t.Fatalf("hidden admin moment remained visible: %v", feed)
	}
	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/moments/"+momentID+"/moderate", adminKey, `{"status":"published","reason":"复核恢复","confirmed":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("restore admin moment status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v2/moments/"+momentID+"/comments/"+commentID, authorToken, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete moment comment status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodDelete, ts.URL+"/v2/moments/"+momentID, authorToken, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete moment status=%d", res.StatusCode)
	}
	res.Body.Close()
	_ = viewer
}

func TestStickerStoreRESTWorkflowPostgres(t *testing.T) {
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
	creatorPhone, viewerPhone := "185"+suffix, "186"+suffix
	creator, err := a.Login(creatorPhone, "Sticker Creator")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := a.Login(viewerPhone, "Sticker Viewer")
	if err != nil {
		t.Fatal(err)
	}
	coverID, mediaID := "sticker_rest_cover_"+suffix, "sticker_rest_media_"+suffix
	for _, item := range []store.Media{
		{ID: coverID, OwnerID: creator.ID, ObjectKey: "stickers/" + coverID, MIME: "image/webp", Size: 128, Status: "ready"},
		{ID: mediaID, OwnerID: creator.ID, ObjectKey: "stickers/" + mediaID, MIME: "image/webp", Size: 128, Status: "ready"},
	} {
		if err = p.CreateMedia(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	adminKey := postgresAdminTestToken(t, p, cfg.JWTSecret)
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	categoryID, packID, stickerID := "sticker_rest_category_"+suffix, "sticker_rest_pack_"+suffix, "sticker_rest_item_"+suffix
	res := adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/sticker-categories", adminKey,
		fmt.Sprintf(`{"id":%q,"name":"Popular","enabled":true,"sortOrder":10,"reason":"create test category","confirmed":true}`, categoryID))
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("create sticker category status=%d body=%s", res.StatusCode, body)
	}
	res.Body.Close()
	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/sticker-packs", adminKey,
		fmt.Sprintf(`{"id":%q,"categoryId":%q,"name":"Animals","coverMediaId":%q,"status":"reviewing","sortOrder":10,"reason":"create test pack","confirmed":true}`, packID, categoryID, coverID))
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("create sticker pack status=%d body=%s", res.StatusCode, body)
	}
	res.Body.Close()
	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/sticker-packs/"+packID+"/items", adminKey,
		fmt.Sprintf(`{"id":%q,"name":"Cat","mediaId":%q,"emoji":"🐱","status":"published","metadata":{"animated":false},"reason":"create test item","confirmed":true}`, stickerID, mediaID))
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("create sticker item status=%d body=%s", res.StatusCode, body)
	}
	res.Body.Close()
	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/sticker-packs/"+packID+"/review", adminKey, `{"status":"published","reason":"publish test pack","confirmed":true}`)
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("publish sticker pack status=%d body=%s", res.StatusCode, body)
	}
	res.Body.Close()
	viewerToken := loginToken(t, ts.URL, viewerPhone)

	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/stickers/categories", viewerToken, "")
	var categories struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&categories)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(categories.Items) == 0 {
		t.Fatalf("sticker categories status=%d body=%v", res.StatusCode, categories)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/stickers/packs?categoryId="+url.QueryEscape(categoryID), viewerToken, "")
	var packs struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&packs)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(packs.Items) != 1 || packs.Items[0]["id"] != packID {
		t.Fatalf("sticker packs status=%d body=%v", res.StatusCode, packs)
	}
	items, _ := packs.Items[0]["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("sticker pack items=%v", packs.Items[0]["items"])
	}
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v2/stickers/packs/"+packID+"/favorite", viewerToken, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("favorite pack status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v2/stickers/"+stickerID+"/favorite", viewerToken, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("favorite sticker status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/stickers/"+stickerID+"/used", viewerToken, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("record sticker use status=%d", res.StatusCode)
	}
	res.Body.Close()
	for _, path := range []string{"/v2/stickers/favorites", "/v2/stickers/recent"} {
		res = authenticatedRequest(t, http.MethodGet, ts.URL+path, viewerToken, "")
		var result struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.NewDecoder(res.Body).Decode(&result)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || len(result.Items) != 1 || result.Items[0]["id"] != stickerID {
			t.Fatalf("sticker list path=%s status=%d body=%v", path, res.StatusCode, result)
		}
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/stickers/packs/"+packID, viewerToken, "")
	var detail struct {
		Item map[string]any `json:"item"`
	}
	_ = json.NewDecoder(res.Body).Decode(&detail)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || detail.Item["favorite"] != true || detail.Item["id"] != packID {
		t.Fatalf("sticker detail status=%d body=%v", res.StatusCode, detail)
	}
	res = adminKeyRequest(t, http.MethodGet, ts.URL+"/v2/admin/sticker-packs?q="+url.QueryEscape(packID), adminKey, "")
	var moderationPacks struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&moderationPacks)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(moderationPacks.Items) != 1 || moderationPacks.Items[0]["id"] != packID {
		t.Fatalf("admin sticker packs status=%d body=%v", res.StatusCode, moderationPacks)
	}
	res = adminKeyRequest(t, http.MethodPost, ts.URL+"/v2/admin/sticker-packs/"+packID+"/review", adminKey, `{"status":"disabled","reason":"运营下架","confirmed":true}`)
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("disable sticker pack status=%d body=%s", res.StatusCode, body)
	}
	res.Body.Close()
	_ = viewer
}

func directConversation(t *testing.T, base, token, other string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"userId": other})
	req, _ := http.NewRequest(http.MethodPost, base+"/v2/channels/direct", bytes.NewReader(body))
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

func TestMessageCollaborationRequiresCanonicalStore(t *testing.T) {
	a, _ := app.New(context.Background(), teststore.Memory{})
	_ = a.SeedDemo()
	installHTTPTestWukongRuntime(a)
	cfg := config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	alice, bob := loginToken(t, ts.URL, "13800000001"), loginToken(t, ts.URL, "13800000002")
	res := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/channels/groups", alice, `{"name":"Collaboration","memberIds":["usr_bob"]}`)
	var group model.Conversation
	_ = json.NewDecoder(res.Body).Decode(&group)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || group.ID == "" {
		t.Fatalf("create group status=%d group=%+v", res.StatusCode, group)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/channels/conversations/"+group.ID+"/typing", bob, `{"typing":true}`)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("member typing status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/messages/conversations/"+group.ID+"/send", alice, `{"clientMsgId":"collab-1","type":"text","body":{"text":"first","mentions":["usr_bob"]}}`)
	var sent struct {
		Message model.Message `json:"message"`
	}
	_ = json.NewDecoder(res.Body).Decode(&sent)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || sent.Message.ID == "" {
		t.Fatalf("send status=%d message=%+v", res.StatusCode, sent.Message)
	}
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/messages/"+sent.Message.ID, alice, `{"text":"final searchable","mentions":["usr_bob"]}`)
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("edit without canonical extension store status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/messages/"+sent.Message.ID, bob, `{"editId":"hijack","text":"bad"}`)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("non-author edit status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v2/messages/"+sent.Message.ID+"/reactions/%F0%9F%91%8D", bob, "")
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("reaction without canonical extension store status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v2/messages/pins/"+sent.Message.ID+"?conversationId="+url.QueryEscape(group.ID), bob, "")
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("member pin status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodPut, ts.URL+"/v2/messages/pins/"+sent.Message.ID+"?conversationId="+url.QueryEscape(group.ID), alice, "")
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("owner pin status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/messages/pins?conversationId="+url.QueryEscape(group.ID), bob, "")
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("pin list without canonical extension store status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/messages/search?conversationId="+url.QueryEscape(group.ID)+"&q=SEARCHABLE&limit=10", bob, "")
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("search without canonical WuKong loader status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/messages/"+sent.Message.ID+"/edits", bob, "")
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("edits without canonical extension store status=%d", res.StatusCode)
	}
	res = authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/messages/conversations/"+group.ID+"/send", alice, `{"clientMsgId":"collab-invalid","type":"text","body":{"text":"bad mention","mentions":[{"userId":"usr_bob","name":"Bob"}]}}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("object mention status=%d", res.StatusCode)
	}
	res.Body.Close()
}
