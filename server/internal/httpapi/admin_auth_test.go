package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/teststore"
)

func TestAdminRolePermissionsAreServerEnforced(t *testing.T) {
	cases := []struct {
		role, method, path string
		allowed            bool
	}{
		{"platform_admin", "DELETE", "/v2/admin/announcements/a", true},
		{"moderator", "POST", "/v2/admin/users/u/ban", true},
		{"moderator", "POST", "/v2/admin/reports/r/resolve", true},
		{"moderator", "DELETE", "/v2/admin/sensitive-words/w", true},
		{"moderator", "POST", "/v2/admin/moments/m/moderate", true},
		{"content_operator", "POST", "/v2/admin/sticker-packs/p/review", true},
		{"content_operator", "POST", "/v2/admin/sticker-categories", true},
		{"content_operator", "POST", "/v2/admin/sticker-packs/p/items", true},
		{"content_operator", "POST", "/v2/admin/announcements/a/publish", true},
		{"system_operator", "PUT", "/v2/admin/client-versions/android", true},
		{"system_operator", "POST", "/v2/admin/wukong/plugins/p/install", true},
		{"support_agent", "POST", "/v2/admin/support/sessions/s/transfer", true},
		{"support", "POST", "/v2/admin/support/sessions/s/transfer", false},
		{"moderator", "POST", "/v2/admin/groups/g/disband", false},
		{"moderator", "POST", "/v2/admin/announcements/a/publish", false},
		{"moderator", "PUT", "/v2/admin/settings", false},
		{"support", "GET", "/v2/admin/users", true},
		{"support", "POST", "/v2/admin/users/u/ban", false},
	}
	for _, test := range cases {
		if got := roleAllowed(test.role, adminPermission(test.method, test.path)); got != test.allowed {
			t.Errorf("role=%s method=%s path=%s allowed=%v want=%v", test.role, test.method, test.path, got, test.allowed)
		}
	}
}

func TestEveryRegisteredAdminJSONWriteRequiresConfirmationAndReason(t *testing.T) {
	a, err := app.New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	adminKey := strings.Repeat("k", 32)
	cfg := config.Config{
		JWTSecret:             strings.Repeat("j", 32),
		AdminKey:              adminKey,
		AdminSharedKeyEnabled: true,
		AccessTTL:             time.Hour,
		RefreshTTL:            24 * time.Hour,
	}
	server := httptest.NewServer(New(cfg, a).Handler())
	defer server.Close()

	routes := []struct{ method, path string }{
		{http.MethodPost, "/v2/admin/users/u/ban"},
		{http.MethodPost, "/v2/admin/users/u/unban"},
		{http.MethodPost, "/v2/admin/reports/r/resolve"},
		{http.MethodPost, "/v2/admin/announcements"},
		{http.MethodPut, "/v2/admin/announcements/a"},
		{http.MethodPost, "/v2/admin/announcements/a/publish"},
		{http.MethodPost, "/v2/admin/announcements/a/withdraw"},
		{http.MethodDelete, "/v2/admin/announcements/a"},
		{http.MethodPost, "/v2/admin/groups/g/disband"},
		{http.MethodPost, "/v2/admin/sensitive-words"},
		{http.MethodDelete, "/v2/admin/sensitive-words/w"},
		{http.MethodPut, "/v2/admin/settings"},
		{http.MethodPut, "/v2/admin/client-versions/android"},
		{http.MethodPost, "/v2/admin/moments/m/moderate"},
		{http.MethodPost, "/v2/admin/sticker-packs/p/review"},
		{http.MethodPost, "/v2/admin/sticker-categories"},
		{http.MethodPut, "/v2/admin/sticker-categories/c"},
		{http.MethodPost, "/v2/admin/sticker-packs"},
		{http.MethodPut, "/v2/admin/sticker-packs/p"},
		{http.MethodPost, "/v2/admin/sticker-packs/p/items"},
		{http.MethodPut, "/v2/admin/sticker-packs/p/items/i"},
		{http.MethodPost, "/v2/admin/wukong/devices/u/quit"},
		{http.MethodPost, "/v2/admin/wukong/plugins/install"},
		{http.MethodPut, "/v2/admin/wukong/plugins/p/upgrade"},
		{http.MethodPost, "/v2/admin/wukong/plugins/p/enable"},
		{http.MethodPost, "/v2/admin/wukong/plugins/p/disable"},
		{http.MethodPut, "/v2/admin/wukong/plugins/p/config"},
		{http.MethodDelete, "/v2/admin/wukong/plugins/p"},
		{http.MethodDelete, "/v2/admin/livekit/rooms/room"},
		{http.MethodDelete, "/v2/admin/livekit/rooms/room/participants/u"},
		{http.MethodPost, "/v2/admin/channels"},
		{http.MethodPatch, "/v2/admin/channels/c"},
		{http.MethodPut, "/v2/admin/channels/c/members/u"},
		{http.MethodPatch, "/v2/admin/channels/c/members/u"},
		{http.MethodDelete, "/v2/admin/channels/c/members/u"},
		{http.MethodPut, "/v2/admin/channels/c/access/deny/u"},
		{http.MethodDelete, "/v2/admin/channels/c/access/deny/u"},
		{http.MethodPost, "/v2/admin/support/skills"},
		{http.MethodPut, "/v2/admin/support/skills/s"},
		{http.MethodPut, "/v2/admin/support/agents/u"},
		{http.MethodPost, "/v2/admin/support/sessions/s/claim"},
		{http.MethodPost, "/v2/admin/support/sessions/s/transfer"},
		{http.MethodPost, "/v2/admin/support/sessions/s/end"},
	}

	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			response := adminKeyRequest(t, route.method, server.URL+route.path, adminKey, `{}`)
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), `"code":"CONFIRMATION_REQUIRED"`) {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
		})
	}
}

func TestAdminWriteControlPreservesStrictJSONBody(t *testing.T) {
	body := `{"confirmed":true,"reason":"approved change","value":7}`
	request := httptest.NewRequest(http.MethodPost, "/v2/admin/example", strings.NewReader(body))
	reason, err := adminWriteControl(request)
	if err != nil || reason != "approved change" {
		t.Fatalf("reason=%q err=%v", reason, err)
	}
	restored, err := io.ReadAll(request.Body)
	if err != nil || string(restored) != body {
		t.Fatalf("restored=%q err=%v", restored, err)
	}
}
