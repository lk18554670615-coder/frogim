package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
)

type peerLoginStore struct {
	teststore.Memory
	store.UserAccessStore
	mu       sync.Mutex
	profiles map[string]store.UserAccessProfile
	allowed  map[string]bool
	peers    map[string]map[string]string
	queries  [][]string
	updates  [][]string
	fail     bool
}

func (s *peerLoginStore) RecordUserAccess(context.Context, store.UserAccessLog) error { return nil }
func (s *peerLoginStore) UserAccessProfiles(_ context.Context, ids []string, _ string) (map[string]store.UserAccessProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries = append(s.queries, append([]string(nil), ids...))
	if s.fail {
		return nil, errors.New("database unavailable")
	}
	out := make(map[string]store.UserAccessProfile, len(ids))
	for _, id := range ids {
		out[id] = s.profiles[id]
	}
	return out, nil
}

func (s *peerLoginStore) InternalUser(_ context.Context, uid string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	allowed, found := s.allowed[uid]
	return !found || allowed, nil
}

func (s *peerLoginStore) SetInternalUser(_ context.Context, _ string, userID string, internal bool, _, _ string) (store.InternalUserUpdate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.allowed[userID]
	s.allowed[userID] = internal
	s.updates = append(s.updates, []string{userID})
	return store.InternalUserUpdate{UserID: userID, IsInternalUser: internal, Changed: previous != internal}, nil
}

func (s *peerLoginStore) ReadFriendLoginIP(_ context.Context, viewerID, conversationID, _ string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if allowed, found := s.allowed[viewerID]; found && !allowed {
		return "", "", store.ErrForbidden
	}
	peerID := s.peers[conversationID][viewerID]
	if peerID == "" {
		return "", "", store.ErrNotFound
	}
	s.queries = append(s.queries, []string{peerID})
	if s.fail {
		return "", "", errors.New("database unavailable")
	}
	return peerID, s.profiles[peerID].LastLoginIP, nil
}

func TestAdminInternalUserEndpoint(t *testing.T) {
	s := &peerLoginStore{profiles: map[string]store.UserAccessProfile{}, allowed: map[string]bool{}, peers: map[string]map[string]string{}}
	a, err := app.New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: strings.Repeat("p", 32), AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	token := adminTestToken(t, cfg.JWTSecret)

	put := func(path, body string, want int) string {
		t.Helper()
		res := authenticatedRequest(t, http.MethodPut, ts.URL+path, token, body)
		defer res.Body.Close()
		raw, readErr := io.ReadAll(res.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if res.StatusCode != want {
			t.Fatalf("status=%d want=%d body=%s", res.StatusCode, want, raw)
		}
		return string(raw)
	}
	raw := put("/v2/admin/users/usr_alice/internal-user", `{"isInternalUser":true,"reason":"support ticket","confirmed":true}`, http.StatusOK)
	if !strings.Contains(raw, `"isInternalUser":true`) || !strings.Contains(raw, `"changed":true`) {
		t.Fatalf("internal-user update response: %s", raw)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.updates) != 1 || len(s.updates[0]) != 1 || s.updates[0][0] != "usr_alice" {
		t.Fatalf("only valid updates reach persistence: %+v", s.updates)
	}
}

func TestConversationPeerLoginInfo(t *testing.T) {
	s := &peerLoginStore{profiles: map[string]store.UserAccessProfile{
		"usr_alice": {RegistrationIP: "198.51.100.10", LastLoginIP: "::ffff:192.168.1.20"},
		"usr_bob":   {RegistrationIP: "198.51.100.11", LastLoginIP: "fd00:0:0:0:0:0:0:8888"},
	}, allowed: map[string]bool{"usr_alice": true, "usr_bob": true, "usr_stranger": true}, peers: map[string]map[string]string{}}
	a, err := app.New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	c, err := a.DirectConversation("usr_alice", "usr_bob")
	if err != nil {
		t.Fatal(err)
	}
	s.peers[c.ID] = map[string]string{"usr_alice": "usr_bob", "usr_bob": "usr_alice"}
	group, err := a.CreateGroup("usr_alice", "IP test", []string{"usr_bob"})
	if err != nil {
		t.Fatal(err)
	}
	x := New(config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}, a)
	ts := httptest.NewServer(x.Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	bob := loginToken(t, ts.URL, "13800000002")
	stranger := loginToken(t, ts.URL, "13912340001")
	me := authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/users/me", alice, "")
	meRaw, err := io.ReadAll(me.Body)
	me.Body.Close()
	if err != nil || me.StatusCode != http.StatusOK || !strings.Contains(string(meRaw), `"isInternalUser":true`) || strings.Contains(string(meRaw), `"canDeleteMessagesForEveryone"`) || strings.Contains(string(meRaw), `"canViewFriendLoginIp"`) {
		t.Fatalf("own profile must explicitly expose the granted capability: status=%d body=%s err=%v", me.StatusCode, meRaw, err)
	}
	path := func(cid string) string { return ts.URL + "/v2/channels/conversations/" + cid + "/peer-login-info" }
	get := func(token, cid string, status int) (peerLoginInfo, string) {
		t.Helper()
		res := authenticatedRequest(t, http.MethodGet, path(cid), token, "")
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != status {
			t.Fatalf("status=%d want=%d body=%s", res.StatusCode, status, raw)
		}
		var info peerLoginInfo
		if status == 200 {
			if res.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("IP response must not be cached by browser or proxy")
			}
			if err := json.Unmarshal(raw, &info); err != nil {
				t.Fatal(err)
			}
		}
		return info, string(raw)
	}
	info, raw := get(alice, c.ID, 200)
	if info.UserID != "usr_bob" || info.LastLoginIP != "fd00::8888" || info.Region.Status != "private" {
		t.Fatalf("full IPv6 must survive normalization: %+v", info)
	}
	for _, secret := range []string{"198.51.100", "registrationIp", "lastLoginAt", "logs", "matchedSources"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("unexpected extra access data %s: %s", secret, raw)
		}
	}
	info, _ = get(bob, c.ID, 200)
	if info.UserID != "usr_alice" || info.LastLoginIP != "192.168.1.20" || info.Region.Status != "private" {
		t.Fatalf("reverse direction/mapped IPv4: %+v", info)
	}
	s.mu.Lock()
	s.profiles["usr_bob"] = store.UserAccessProfile{LastLoginIP: "2001:4860:4860::8888"}
	s.mu.Unlock()
	_, raw = get(alice, c.ID, 503)
	if !strings.Contains(raw, "LOGIN_IP_UNAVAILABLE") || strings.Contains(raw, "2001:4860") {
		t.Fatalf("unavailable geolocation must fail closed without copying the IP: %s", raw)
	}
	s.mu.Lock()
	s.profiles["usr_bob"] = store.UserAccessProfile{LastLoginIP: "fd00::8888"}
	s.mu.Unlock()
	get("", c.ID, 401)
	s.mu.Lock()
	s.allowed["usr_alice"] = false
	s.mu.Unlock()
	_, raw = get(alice, c.ID, 403)
	if !strings.Contains(raw, "PEER_LOGIN_IP_PERMISSION_REQUIRED") {
		t.Fatalf("missing permission error code: %s", raw)
	}
	s.mu.Lock()
	s.allowed["usr_alice"] = true
	s.mu.Unlock()
	get(stranger, c.ID, 404)
	get(alice, "nonexistent", 404)
	get(alice, group.ID, 404)
	s.mu.Lock()
	if len(s.queries) != 3 || len(s.queries[0]) != 1 || s.queries[0][0] != "usr_bob" || s.queries[1][0] != "usr_alice" || s.queries[2][0] != "usr_bob" {
		t.Fatalf("only the authorized conversation partner may be read: %+v", s.queries)
	}
	delete(s.profiles, "usr_bob")
	s.mu.Unlock()
	info, _ = get(alice, c.ID, 200)
	if info.LastLoginIP != "" || info.Region.Status != "unknown" {
		t.Fatalf("missing records must not be invented: %+v", info)
	}
	s.mu.Lock()
	s.fail = true
	s.mu.Unlock()
	_, raw = get(alice, c.ID, 503)
	if !strings.Contains(raw, "LOGIN_IP_UNAVAILABLE") || strings.Contains(raw, "database unavailable") {
		t.Fatalf("failure must be explicit without internal details: %s", raw)
	}
}
