package config

import (
	"strings"
	"testing"
)

func TestTenantPreviewCannotAccidentallyEnableProduction(t *testing.T) {
	c := Config{TenantID: "a", TenancyPreview: true, Environment: "production"}
	if err := c.validateTenant(); err == nil {
		t.Fatal("unfinished tenant runtime enabled in production")
	}
	c.Environment = "development"
	c.PlatformControlURL = "https://platform.example"
	c.TenantCAFile = "ca"
	c.TenantCertFile = "cert"
	c.TenantKeyFile = "key"
	c.TenantControlAddr = ":8444"
	c.JWTSecret = strings.Repeat("a", 32)
	c.MediaSigningSecret = strings.Repeat("b", 32)
	c.AllowedOrigins = []string{"https://app.example"}
	if err := c.validateTenant(); err != nil {
		t.Fatal(err)
	}
	c.MediaSigningSecret = c.JWTSecret
	if err := c.validateTenant(); err == nil {
		t.Fatal("shared auth/media key")
	}
	c.MediaSigningSecret = strings.Repeat("b", 32)
	c.AllowedOrigins = []string{"*"}
	if err := c.validateTenant(); err == nil {
		t.Fatal("wildcard CORS")
	}
	c.AllowedOrigins = []string{"https://app.example"}
	c.DevMode = true
	if err := c.validateTenant(); err == nil {
		t.Fatal("development authentication in enterprise")
	}
	c.DevMode = false
	c.LiveKitEnabled = true
	c.TenantPublicURL = "https://a.example"
	c.LiveKitURL = "wss://unguarded.example"
	if err := c.validateTenant(); err == nil {
		t.Fatal("unguarded signaling URL accepted")
	}
	c.LiveKitURL = "wss://a.example/livekit"
	if err := c.validateTenant(); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"noop", "platform"} {
		c.PushProvider = provider
		if err := c.validateTenant(); err != nil {
			t.Fatal(err)
		}
	}
	for _, provider := range []string{"getui", "webhook", "getui_apns_voip", "log"} {
		c.PushProvider = provider
		if err := c.validateTenant(); err == nil {
			t.Fatal("enterprise direct provider accepted", provider)
		}
	}
	c.PushProvider = "platform"
	c.GetuiMasterSecret = "must-stay-on-platform"
	if err := c.validateTenant(); err == nil {
		t.Fatal("shared provider secret on enterprise accepted")
	}
	c.GetuiMasterSecret = ""
	c.WebPushPrivateKey = "must-stay-on-platform"
	if err := c.validateTenant(); err == nil {
		t.Fatal("shared Web Push key on enterprise accepted")
	}
	if err := (Config{PushProvider: "platform"}).validateTenant(); err == nil {
		t.Fatal("standalone platform provider accepted")
	}
}
