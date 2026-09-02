package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/teststore"
)

func TestGroupAdministratorRoleRequiresOwner(t *testing.T) {
	a, err := app.New(t.Context(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	group, err := a.CreateGroup("usr_alice", "Administrator role test", []string{"usr_bob", "usr_admin"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ts := httptest.NewServer(New(cfg, a).Handler())
	defer ts.Close()
	owner := loginToken(t, ts.URL, "13800000001")
	member := loginToken(t, ts.URL, "13800000002")
	for _, tc := range []struct {
		name, token, target, role string
		status                    int
	}{
		{"member cannot promote", member, "usr_admin", "admin", http.StatusForbidden},
		{"owner promotes", owner, "usr_bob", "admin", http.StatusOK},
		{"administrator cannot promote", member, "usr_admin", "admin", http.StatusForbidden},
		{"administrator cannot demote self", member, "usr_bob", "member", http.StatusForbidden},
		{"owner cannot demote self", owner, "usr_alice", "member", http.StatusForbidden},
		{"role cannot transfer ownership", owner, "usr_bob", "owner", http.StatusBadRequest},
		{"owner cancels administrator", owner, "usr_bob", "member", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := ts.URL + "/v2/channels/groups/" + group.ID + "/members/" + tc.target + "/role"
			res := authenticatedRequest(t, http.MethodPut, path, tc.token, `{"role":"`+tc.role+`"}`)
			defer res.Body.Close()
			if res.StatusCode != tc.status {
				t.Fatalf("status=%d want=%d", res.StatusCode, tc.status)
			}
		})
	}
	members, err := a.GroupMembers("usr_alice", group.ID)
	if err != nil || len(members) != 3 {
		t.Fatalf("members=%+v err=%v", members, err)
	}
	for _, m := range members {
		want := "member"
		if m.UserID == "usr_alice" {
			want = "owner"
		}
		if m.Role != want {
			t.Fatalf("user=%s role=%s want=%s", m.UserID, m.Role, want)
		}
	}
}
