package httpapi

import (
	"strings"

	"github.com/linli/im/server/internal/store"
)

func adminAccountAllowed(account *store.AdminAccount, permission string) bool {
	if account == nil || account.Status != "active" {
		return false
	}
	if permission == "read" || account.RoleID == "platform_admin" {
		return true
	}
	for _, candidate := range account.Permissions {
		if candidate == permission {
			return true
		}
	}
	return false
}
func adminPermission(method, path string) string {
	if strings.Contains(path, "/administrators") || strings.Contains(path, "/roles") {
		return "platform.write"
	}
	if method == "GET" {
		return "read"
	}
	switch {
	case strings.Contains(path, "/client-versions"):
		return "versions.write"
	case strings.Contains(path, "/moments") || strings.Contains(path, "/sticker-"):
		return "content.write"
	case strings.Contains(path, "/wukong") || strings.Contains(path, "/livekit") || strings.Contains(path, "/plugins"):
		return "operations.write"
	case strings.Contains(path, "/support"):
		return "support.write"
	case strings.Contains(path, "/channels"):
		return "channels.write"
	case strings.Contains(path, "/settings"):
		return "settings.write"
	case strings.Contains(path, "/users/"):
		return "users.write"
	case strings.Contains(path, "/reports/"):
		return "reports.write"
	case strings.Contains(path, "/sensitive-words"):
		return "rules.write"
	case strings.Contains(path, "/groups/"):
		return "groups.write"
	case strings.Contains(path, "/announcements"):
		return "announcements.write"
	default:
		return "platform.write"
	}
}
