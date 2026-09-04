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
	queries  [][]string
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

func TestConversationPeerLoginInfo(t *testing.T) {
	s := &peerLoginStore{profiles: map[string]store.UserAccessProfile{
		"usr_alice": {RegistrationIP: "198.51.100.10", LastLoginIP: "::ffff:192.168.1.20"},
		"usr_bob":   {RegistrationIP: "198.51.100.11", LastLoginIP: "2001:4860:4860:0:0:0:0:8888"},
	}}
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
	if info.UserID != "usr_bob" || info.LastLoginIP != "2001:4860:4860::8888" || info.Region.Status != "unavailable" {
		t.Fatalf("full IPv6 must survive missing region DB: %+v", info)
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
	get("", c.ID, 401)
	get(stranger, c.ID, 404)
	get(alice, "nonexistent", 404)
	get(alice, group.ID, 404)
	s.mu.Lock()
	if len(s.queries) != 2 || len(s.queries[0]) != 1 || s.queries[0][0] != "usr_bob" || s.queries[1][0] != "usr_alice" {
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
