package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/teststore"
	"github.com/linli/im/server/internal/wukong"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUserPresenceAuthorization(t *testing.T) {
	a, _ := app.New(t.Context(), teststore.Memory{})
	if err := a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	presenceTestFriend(t, a)
	group, err := a.CreateGroup("usr_alice", "presence", []string{"usr_bob", "usr_admin"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	api := New(cfg, a)
	fail := false
	api.presence = wukong.NewPresenceCache(func(_ context.Context, ids []string) (map[string]bool, error) {
		if fail {
			return nil, errors.New("unavailable")
		}
		out := map[string]bool{}
		for _, id := range ids {
			out[id] = id != "usr_bob"
		}
		return out, nil
	})
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()
	owner := loginToken(t, ts.URL, "13800000001")
	member := loginToken(t, ts.URL, "13800000002")
	query := func(token, ctx string) map[string]string {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"userIds": []string{"usr_alice", "usr_bob", "usr_admin", "missing"}, "groupId": ctx})
		res := authenticatedRequest(t, "POST", ts.URL+"/v2/users/presence", token, string(data))
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("status=%d", res.StatusCode)
		}
		var body struct {
			Items []wukong.Presence `json:"items"`
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, v := range body.Items {
			out[v.UserID] = v.Status
			if v.CheckedAt.IsZero() {
				t.Fatal("missing query time")
			}
		}
		return out
	}
	if got := query(owner, ""); got["usr_alice"] != "online" || got["usr_bob"] != "offline" || got["missing"] != "hidden" {
		t.Fatal(got)
	}
	// Remove friendship after caching: permission must not be cached with status.
	if err := a.DeleteFriend("usr_alice", "usr_bob"); err != nil {
		t.Fatal(err)
	}
	if got := query(owner, "")["usr_bob"]; got != "hidden" {
		t.Fatal(got)
	}
	if got := query(owner, group.ID)["usr_bob"]; got != "offline" {
		t.Fatal(got)
	}
	if got := query(member, group.ID)["usr_alice"]; got != "hidden" {
		t.Fatal(got)
	}
	if err := a.SetGroupRole("usr_alice", group.ID, "usr_bob", "admin"); err != nil {
		t.Fatal(err)
	}
	if got := query(member, group.ID)["usr_alice"]; got != "online" {
		t.Fatal(got)
	}
	if got := query(member, "another_group")["usr_alice"]; got != "hidden" {
		t.Fatal(got)
	}
	if err := a.SetGroupRole("usr_alice", group.ID, "usr_bob", "member"); err != nil {
		t.Fatal(err)
	}
	if got := query(member, group.ID)["usr_alice"]; got != "hidden" {
		t.Fatal(got)
	}
	if err := a.RemoveGroupMember("usr_alice", group.ID, "usr_bob"); err != nil {
		t.Fatal(err)
	}
	if got := query(owner, group.ID)["usr_bob"]; got != "hidden" {
		t.Fatal(got)
	}
	fail = true
	api.presence = wukong.NewPresenceCache(func(context.Context, []string) (map[string]bool, error) { return nil, errors.New("down") })
	if got := query(owner, ""); got["usr_alice"] != "unknown" || got["usr_bob"] != "hidden" {
		t.Fatal(got)
	}
	for _, body := range []string{`{"userIds":[]}`, `{"userIds":[""]}`, `{"userIds":["usr_alice"],"role":"owner"}`, `{"userIds":` + strings.Repeat(`"a",`, 200) + `"a"]}`} {
		res := authenticatedRequest(t, "POST", ts.URL+"/v2/users/presence", owner, body)
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatalf("invalid body status=%d", res.StatusCode)
		}
	}
	res, err := http.Post(ts.URL+"/v2/users/presence", "application/json", strings.NewReader(`{"userIds":["usr_alice"]}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal(res.StatusCode)
	}
}

func TestUserPresenceRechecksAfterUpstreamWait(t *testing.T) {
	a, _ := app.New(t.Context(), teststore.Memory{})
	_ = a.SeedDemo()
	presenceTestFriend(t, a)
	api := New(config.Config{JWTSecret: strings.Repeat("a", 32), DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}, a)
	started, release := make(chan struct{}), make(chan struct{})
	api.presence = wukong.NewPresenceCache(func(context.Context, []string) (map[string]bool, error) {
		close(started)
		<-release
		return map[string]bool{"usr_bob": true}, nil
	})
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()
	token := loginToken(t, ts.URL, "13800000001")
	done := make(chan string, 1)
	go func() {
		res := authenticatedRequest(t, "POST", ts.URL+"/v2/users/presence", token, `{"userIds":["usr_bob"]}`)
		defer res.Body.Close()
		var body struct {
			Items []wukong.Presence `json:"items"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		done <- body.Items[0].Status
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("upstream was not queried")
	}
	if err := a.DeleteFriend("usr_alice", "usr_bob"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if got := <-done; got != "hidden" {
		t.Fatal(got)
	}
}

func presenceTestFriend(t *testing.T, a *app.App) {
	t.Helper()
	r, err := a.RequestFriend("usr_alice", "usr_bob", "presence test")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.AcceptFriend("usr_bob", r.ID); err != nil {
		t.Fatal(err)
	}
}
