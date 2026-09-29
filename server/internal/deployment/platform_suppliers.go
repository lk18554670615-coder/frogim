package deployment

import (
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"

	"github.com/linli/im/server/internal/tenancy"
	"github.com/linli/im/server/internal/webpushpolicy"
)

// Provider-only allowlist: cannot override database, listeners, auth identity,
// paths, deployment catalog, environment mode or native loader variables.
func validatePlatformSuppliers(c PlatformConfig) error {
	keys := strings.Fields(`PLATFORM_OTP_WEBHOOK_URL PLATFORM_OTP_WEBHOOK_TOKEN
PLATFORM_PASSWORD_RESET_SMS_URL PLATFORM_PASSWORD_RESET_SMS_TOKEN
PLATFORM_PUSH_PROVIDER PLATFORM_PUSH_ENCRYPTION_KEY PLATFORM_GETUI_APP_ID
PLATFORM_GETUI_APP_KEY PLATFORM_GETUI_MASTER_SECRET PLATFORM_GETUI_VOIP_ENABLED
PLATFORM_WEB_PUSH_ENABLED PLATFORM_WEB_PUSH_PUBLIC_KEY PLATFORM_WEB_PUSH_PRIVATE_KEY
PLATFORM_WEB_PUSH_SUBJECT PLATFORM_WEB_PUSH_ALLOWED_HOSTS`)
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	for key, value := range c.Suppliers {
		if !allowed[key] || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
			return ErrBundle
		}
	}
	e := c.Suppliers
	for _, prefix := range []string{"PLATFORM_OTP_WEBHOOK", "PLATFORM_PASSWORD_RESET_SMS"} {
		endpoint, token := e[prefix+"_URL"], e[prefix+"_TOKEN"]
		if endpoint == "" && token == "" {
			continue
		}
		u, err := url.Parse(endpoint)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || tenancy.PublicOrigin(u.Scheme+"://"+u.Host) != nil || len(token) < 32 || token == c.DatabaseSecret || token == c.RedisSecret {
			return ErrBundle
		}
		if prefix == "PLATFORM_OTP_WEBHOOK" && tenancy.ValidateBaseURL(endpoint, false) != nil {
			return ErrBundle
		}
	}
	mode := e["PLATFORM_PUSH_PROVIDER"]
	if mode != "" && mode != "disabled" && mode != "getui" {
		return ErrBundle
	}
	getui := mode == "getui"
	voip := e["PLATFORM_GETUI_VOIP_ENABLED"]
	if (voip != "" && voip != "true" && voip != "false") || (voip == "true" && !getui) {
		return ErrBundle
	}
	if getui {
		// Getui issues 22-character master secrets; a 24-character minimum
		// rejects valid existing provider credentials during platform adoption.
		if !regexp.MustCompile(`^[A-Za-z0-9_-]{4,128}$`).MatchString(e["PLATFORM_GETUI_APP_ID"]) || len(e["PLATFORM_GETUI_APP_KEY"]) < 16 || len(e["PLATFORM_GETUI_MASTER_SECRET"]) < 22 {
			return ErrBundle
		}
	} else {
		for _, key := range []string{"PLATFORM_GETUI_APP_ID", "PLATFORM_GETUI_APP_KEY", "PLATFORM_GETUI_MASTER_SECRET"} {
			if e[key] != "" {
				return ErrBundle
			}
		}
	}
	if c.APNSPrivateKey != "" {
		return ErrBundle
	}
	web := e["PLATFORM_WEB_PUSH_ENABLED"]
	if web != "" && web != "false" && web != "true" {
		return ErrBundle
	}
	if web == "true" {
		if _, err := webpushpolicy.New(e["PLATFORM_WEB_PUSH_PUBLIC_KEY"], e["PLATFORM_WEB_PUSH_PRIVATE_KEY"], e["PLATFORM_WEB_PUSH_SUBJECT"], e["PLATFORM_WEB_PUSH_ALLOWED_HOSTS"]); err != nil {
			return ErrBundle
		}
	} else {
		for _, key := range []string{"PLATFORM_WEB_PUSH_PUBLIC_KEY", "PLATFORM_WEB_PUSH_PRIVATE_KEY", "PLATFORM_WEB_PUSH_SUBJECT", "PLATFORM_WEB_PUSH_ALLOWED_HOSTS"} {
			if e[key] != "" {
				return ErrBundle
			}
		}
	}
	key := e["PLATFORM_PUSH_ENCRYPTION_KEY"]
	if getui || web == "true" {
		raw, err := base64.RawURLEncoding.DecodeString(key)
		if err != nil || len(raw) != 32 || key == c.DatabaseSecret || key == c.RedisSecret {
			return ErrBundle
		}
	} else if key != "" {
		return ErrBundle
	}
	return nil
}
