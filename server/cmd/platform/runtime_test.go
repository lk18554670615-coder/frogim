package main

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestProductionPlatformRequiresExplicitCompleteProfile(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("synthetic-operator-password"), 12)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	values := map[string]string{
		"PLATFORM_ENV": "production", "PLATFORM_DEPLOYMENT_MODE": "dedicated_host", "PLATFORM_PUBLIC_URL": "https://app.example.test",
		"PLATFORM_WEB_ORIGIN": "https://app.example.test", "PLATFORM_ADDR": ":8090", "PLATFORM_CONTROL_ADDR": ":8443",
		"PLATFORM_DATABASE_URL":   "postgres://platform:" + strings.Repeat("d", 43) + "@platform-db:5432/platform?sslmode=disable",
		"PLATFORM_REDIS_URL":      "redis://:" + strings.Repeat("r", 43) + "@platform-redis:6379/0",
		"PLATFORM_ADMIN_USERNAME": "operator", "PLATFORM_ADMIN_PASSWORD_HASH": string(hash),
		"PLATFORM_GATEWAY_SECRET": strings.Repeat("g", 43),
	}
	for _, key := range []string{"PLATFORM_CA_FILE", "PLATFORM_CERT_FILE", "PLATFORM_KEY_FILE", "PLATFORM_PEERS_FILE"} {
		values[key] = filepath.Join(dir, key)
	}
	get := func(k string) string { return values[k] }
	if validatePlatformRuntime(get) != nil {
		t.Fatal("valid production profile rejected")
	}
	for key, bad := range map[string]string{"PLATFORM_DEPLOYMENT_MODE": "", "PLATFORM_PUBLIC_URL": "https://127.0.0.1:18443", "PLATFORM_WEB_ORIGIN": "*", "PLATFORM_ADMIN_PASSWORD_HASH": "plaintext", "PLATFORM_CA_FILE": "relative", "PLATFORM_DEV_OTP_CODE": "123456", "PLATFORM_DEV_MODE": "true", "PLATFORM_ADDR": ":80", "PLATFORM_CONTROL_ADDR": ":8090", "PLATFORM_DATABASE_URL": "postgres://shared.example.test/db"} {
		t.Run(key, func(t *testing.T) {
			before := values[key]
			values[key] = bad
			defer func() { values[key] = before }()
			if validatePlatformRuntime(get) == nil {
				t.Fatal("unsafe runtime accepted")
			}
		})
	}
	values = map[string]string{"PLATFORM_ENV": "development"}
	if validatePlatformRuntime(get) != nil {
		t.Fatal("local preview broken")
	}
	values["PLATFORM_ENV"] = "production"
	if validatePlatformRuntime(get) == nil {
		t.Fatal("flipping only environment enabled production")
	}
}
