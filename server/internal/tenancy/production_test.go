package tenancy

import (
	"strings"
	"testing"
)

func TestProductionOriginsAndPrivateListeners(t *testing.T) {
	for _, origin := range []string{"https://tenant.example.test", "https://tenant.example.test:9443", "https://203.0.113.12"} {
		if PublicOrigin(origin) != nil {
			t.Fatal("valid syntax rejected")
		}
	}
	for _, origin := range []string{"https://localhost", "https://127.0.0.1:9443", "https://[::1]", "https://10.1.2.3", "https://host.docker.internal", "https://api.local", "http://tenant.example.test", "https://u:p@tenant.example.test", "https://tenant.example.test/other", "https://tenant.example.test/", "https://TENANT.example.test", "https://tenant.example.test.", "https://tenant.example.test:0", "https://tenant.example.test:65536", "*"} {
		if PublicOrigin(origin) == nil {
			t.Fatal("nonproduction origin accepted", origin)
		}
	}
	for _, addr := range []string{"10.40.0.1:8443", "127.0.0.1:8450", "[fd12::1]:8443"} {
		if PrivateListen(addr) != nil {
			t.Fatal("private listener rejected", addr)
		}
	}
	if PublicOrigin("https://tenant.example.test:443") == nil {
		t.Fatal("noncanonical default HTTPS port accepted")
	}
	for _, addr := range []string{":8443", "0.0.0.0:8443", "203.0.113.1:8443", "[::]:8443", "private.example.test:8443", "10.40.0.1:80"} {
		if PrivateListen(addr) == nil {
			t.Fatal("public/ambiguous control listener", addr)
		}
	}
}

func TestProductionDatastoreProfileRejectsOverridesAndSharedCredentials(t *testing.T) {
	db := "postgres://platform:" + strings.Repeat("d", 43) + "@platform-db:5432/platform?sslmode=disable"
	cache := "redis://:" + strings.Repeat("r", 43) + "@platform-redis:6379/0"
	if ProductionDatastores(db, cache, "platform-db", "platform-redis") != nil {
		t.Fatal("valid profile")
	}
	for _, bad := range []string{strings.ReplaceAll(db, "platform-db", "enterprise-db"), db + "&host=external.example.test", db + "&sslmode=disable", strings.ReplaceAll(db, "/platform?", "/enterprise?"), strings.ReplaceAll(db, strings.Repeat("d", 43), "short"), strings.ReplaceAll(db, "sslmode=disable", "sslmode=prefer")} {
		if ProductionDatastores(bad, cache, "platform-db", "platform-redis") == nil {
			t.Fatal("unsafe database URL accepted")
		}
	}
	for _, bad := range []string{strings.ReplaceAll(cache, "platform-redis", "enterprise-redis"), cache + "?addr=another", strings.ReplaceAll(cache, strings.Repeat("r", 43), strings.Repeat("d", 43)), strings.ReplaceAll(cache, "/0", "/1")} {
		if ProductionDatastores(db, bad, "platform-db", "platform-redis") == nil {
			t.Fatal("unsafe Redis URL accepted")
		}
	}
}
