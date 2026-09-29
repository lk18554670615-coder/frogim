package wukong

import (
	"github.com/linli/im/server/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTenantSessionGenerationCannotReviveOldToken(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/token" {
			t.Error("unexpected path")
		}
		writes.Add(1)
		w.WriteHeader(200)
	}))
	defer server.Close()
	client, e := NewClient(Config{APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	state := &credentialMemoryStore{items: map[string]string{}}
	issuer, e := NewSessionIssuer(client, strings.Repeat("a", 40), "tcp://im:5100", "wss://im.example/ws", state)
	if e != nil {
		t.Fatal(e)
	}
	i := tenancy.Identity{TenantID: "a", AccountID: "account", LocalUserID: "user", AssignmentVersion: 1}
	original, e := issuer.IssueTenant(t.Context(), i, 1, 1, "web")
	if e != nil {
		t.Fatal(e)
	}
	again, e := issuer.IssueTenant(t.Context(), i, 1, 1, "web")
	if e != nil || again.Token != original.Token || writes.Load() != 1 {
		t.Fatal("normal refresh rewrote IM", e)
	}
	seen := map[string]bool{original.Token: true}
	for _, change := range []string{"auth", "realm", "assignment", "account", "tenant", "device"} {
		copy := i
		auth, realm, device := int64(1), int64(1), "web"
		switch change {
		case "auth":
			auth = 2
		case "realm":
			realm = 3
		case "assignment":
			copy.AssignmentVersion = 2
		case "account":
			copy.AccountID = "other"
		case "tenant":
			copy.TenantID = "b"
		case "device":
			device = "android"
		}
		session, e := issuer.IssueTenant(t.Context(), copy, auth, realm, device)
		if e != nil {
			t.Fatal(e)
		}
		if seen[session.Token] {
			t.Fatal("generation collision", change)
		}
		seen[session.Token] = true
	}
	for _, v := range [][2]int64{{0, 1}, {1, 0}} {
		if _, e := issuer.IssueTenant(t.Context(), i, v[0], v[1], "web"); e == nil {
			t.Fatal("missing epoch accepted")
		}
	}
	legacy, e := issuer.Issue(t.Context(), i.LocalUserID, "web")
	if e != nil || seen[legacy.Token] {
		t.Fatal("legacy credential reused", e)
	}
}
