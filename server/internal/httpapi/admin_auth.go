package httpapi

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func verifyAdminPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
func verifyTOTP(secret, code string, now time.Time) bool {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return false
	}
	for drift := -1; drift <= 1; drift++ {
		counter := uint64(now.Unix()/30 + int64(drift))
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], counter)
		m := hmac.New(sha1.New, key)
		_, _ = m.Write(b[:])
		sum := m.Sum(nil)
		o := sum[len(sum)-1] & 15
		v := (uint32(sum[o])&127)<<24 | (uint32(sum[o+1]) << 16) | (uint32(sum[o+2]) << 8) | uint32(sum[o+3])
		if fmt.Sprintf("%06d", v%1000000) == code {
			return true
		}
	}
	return false
}

func roleAllowed(role, permission string) bool {
	if role == "platform_admin" {
		return true
	}
	if role == "system_operator" {
		return permission == "read" || permission == "operations.write" || permission == "settings.write" || permission == "versions.write"
	}
	if role == "moderator" {
		return permission == "read" || permission == "users.write" || permission == "reports.write" || permission == "rules.write" || permission == "content.write"
	}
	if role == "content_operator" {
		return permission == "read" || permission == "content.write" || permission == "announcements.write" || permission == "channels.write"
	}
	if role == "support_agent" {
		return permission == "read" || permission == "support.write"
	}
	if role == "support" {
		return permission == "read"
	}
	return false
}
func adminPermission(method, path string) string {
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
