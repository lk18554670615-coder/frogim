package httpapi

import "testing"

func TestAdminRolePermissionsAreServerEnforced(t *testing.T) {
	cases := []struct {
		role, method, path string
		allowed            bool
	}{
		{"platform_admin", "DELETE", "/v1/admin/announcements/a", true},
		{"moderator", "POST", "/v1/admin/users/u/ban", true},
		{"moderator", "POST", "/v1/admin/reports/r/resolve", true},
		{"moderator", "DELETE", "/v1/admin/sensitive-words/w", true},
		{"moderator", "POST", "/v1/admin/groups/g/disband", false},
		{"moderator", "POST", "/v1/admin/announcements/a/publish", false},
		{"moderator", "PUT", "/v1/admin/settings", false},
		{"support", "GET", "/v1/admin/users", true},
		{"support", "POST", "/v1/admin/users/u/ban", false},
	}
	for _, test := range cases {
		if got := roleAllowed(test.role, adminPermission(test.method, test.path)); got != test.allowed {
			t.Errorf("role=%s method=%s path=%s allowed=%v want=%v", test.role, test.method, test.path, got, test.allowed)
		}
	}
}
