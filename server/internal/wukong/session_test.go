package wukong

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type credentialMemoryStore struct {
	mu    sync.Mutex
	items map[string]string
}

func credentialKey(uid string, deviceFlag, deviceLevel int) string {
	return uid + ":" + strconv.Itoa(deviceFlag) + ":" + strconv.Itoa(deviceLevel)
}

func (s *credentialMemoryStore) WukongCredentialProvisioned(_ context.Context, uid string, deviceFlag, deviceLevel int, digest string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.items[credentialKey(uid, deviceFlag, deviceLevel)] == digest, nil
}

func (s *credentialMemoryStore) MarkWukongCredentialProvisioned(_ context.Context, uid string, deviceFlag, deviceLevel int, digest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[credentialKey(uid, deviceFlag, deviceLevel)] = digest
	return nil
}

func (s *credentialMemoryStore) InvalidateWukongCredential(_ context.Context, uid string, deviceFlag int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := uid + ":"
	if deviceFlag != -1 {
		prefix += strconv.Itoa(deviceFlag) + ":"
	}
	for key := range s.items {
		if strings.HasPrefix(key, prefix) {
			delete(s.items, key)
		}
	}
	return nil
}

func TestSessionIssuerDoesNotRewriteUnchangedMasterToken(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/user/token" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		writes.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "manager-token"})
	if err != nil {
		t.Fatal(err)
	}
	state := &credentialMemoryStore{items: map[string]string{}}
	issuer, err := NewSessionIssuer(client, "01234567890123456789012345678901", "tcp://im:5100", "wss://im.example/ws", state)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = issuer.Issue(context.Background(), "usr_1", "web"); err != nil {
			t.Fatal(err)
		}
	}
	if got := writes.Load(); got != 1 {
		t.Fatalf("unchanged credential was provisioned %d times, want 1", got)
	}
	if _, err = issuer.Issue(context.Background(), "usr_1", "macos"); err != nil {
		t.Fatal(err)
	}
	if got := writes.Load(); got != 2 {
		t.Fatalf("new device flag writes=%d, want 2", got)
	}
	macSession, err := issuer.Issue(context.Background(), "usr_1", "macos")
	if err != nil {
		t.Fatal(err)
	}
	if macSession.SDK != "wukong_easy_sdk" || macSession.DeviceFlag != DeviceDesktop {
		t.Fatalf("macOS session=%+v", macSession)
	}
}
