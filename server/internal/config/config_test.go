package config

import (
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{Addr: "127.0.0.1:8080", Mode: "memory", JWTSecret: strings.Repeat("j", 32), AdminKey: strings.Repeat("a", 24), AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour, WSMaxPerUser: 5, WSMaxPerIP: 20, MediaMaxBytes: 100 << 20}
}

func TestValidateFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"missing jwt", func(c *Config) { c.JWTSecret = "" }},
		{"bootstrap missing admin key", func(c *Config) { c.AdminSharedKeyEnabled = true; c.AdminKey = "" }},
		{"shared admin key outside development", func(c *Config) { c.AdminSharedKeyEnabled = true }},
		{"full without database", func(c *Config) { c.Mode = "full" }},
		{"dev without otp", func(c *Config) { c.DevMode = true }},
		{"dev public bind", func(c *Config) { c.DevMode = true; c.DevOTPCode = "654321"; c.Addr = ":8080" }},
		{"production rejects dev", func(c *Config) { c.DevMode = true; c.DevOTPCode = "654321"; c.Environment = "production" }},
		{"media limit too small", func(c *Config) { c.MediaMaxBytes = 1024 }},
		{"container flag needs development env", func(c *Config) {
			c.Mode = "full"
			c.DatabaseURL = "postgres://db"
			c.DevMode = true
			c.DevOTPCode = "654321"
			c.Addr = ":8080"
			c.DevAllowContainerBind = true
			c.DevIPTestOnly = true
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.edit(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	container := validConfig()
	container.Mode = "full"
	container.DatabaseURL = "postgres://db"
	container.DevMode = true
	container.DevOTPCode = "654321"
	container.Addr = ":8080"
	container.Environment = "development"
	container.DevAllowContainerBind = true
	container.DevIPTestOnly = true
	if err := container.Validate(); err != nil {
		t.Fatalf("explicit development container config: %v", err)
	}
	container.Environment = "production"
	if err := container.Validate(); err == nil {
		t.Fatal("production must reject development container bind")
	}
}

func TestEnvironmentProductionWinsAcrossAliases(t *testing.T) {
	t.Setenv("IM_ENV", "development")
	t.Setenv("APP_ENV", "production")
	if got := environment(); got != "production" {
		t.Fatalf("environment=%q", got)
	}
	t.Setenv("IM_ENV", "production")
	t.Setenv("APP_ENV", "development")
	if got := environment(); got != "production" {
		t.Fatalf("environment=%q", got)
	}
}

func TestProductionAllowsCompleteGetuiConfiguration(t *testing.T) {
	c := validConfig()
	c.Mode = "full"
	c.Environment = "production"
	c.DatabaseURL = "postgres://db"
	c.AdminEmail = "ops@example.com"
	c.AdminPasswordHash = "$2a$12$example"
	c.AdminTOTPSecret = "JBSWY3DPEHPK3PXP"
	c.OTPWebhookURL = "https://otp.example.com"
	c.OTPWebhookToken = strings.Repeat("o", 24)
	c.PushProvider = "getui"
	c.GetuiAppID = "app-id"
	c.GetuiAppKey = "app-key"
	c.GetuiMasterSecret = strings.Repeat("m", 16)
	c.RTCSTUNURLs = []string{"stun:stun.example.com:3478"}
	c.RTCTURNURLs = []string{"turns:turn.example.com:5349"}
	c.RTCTURNUsername = "turn-user"
	c.RTCTURNCredential = strings.Repeat("t", 16)
	if err := c.Validate(); err != nil {
		t.Fatalf("complete Getui config: %v", err)
	}
	c.DevIPTestOnly = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "IP-test") {
		t.Fatalf("production must reject IP-test marker: %v", err)
	}
	c.DevIPTestOnly = false
	c.GetuiMasterSecret = "short"
	if err := c.Validate(); err == nil {
		t.Fatal("short Getui master secret must fail")
	}
}

func TestProductionRequiresTURNAndSTUN(t *testing.T) {
	c := validConfig()
	c.Mode = "full"
	c.DatabaseURL = "postgres://db"
	c.AdminEmail = "admin@example.com"
	c.AdminPasswordHash = "$2a$12$example"
	c.AdminTOTPSecret = "JBSWY3DPEHPK3PXP"
	c.OTPWebhookURL = "https://otp.example.com"
	c.OTPWebhookToken = strings.Repeat("o", 24)
	c.PushProvider = "webhook"
	c.PushWebhookURL = "https://push.example.com"
	c.PushWebhookToken = strings.Repeat("p", 24)
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "STUN and TURN") {
		t.Fatalf("missing ICE config err=%v", err)
	}
	c.RTCSTUNURLs = []string{"stun:stun.example.com:3478"}
	c.RTCTURNURLs = []string{"turns:turn.example.com:5349?transport=tcp"}
	c.RTCTURNUsername = "nexachat"
	c.RTCTURNCredential = strings.Repeat("t", 24)
	c.CallInviteTTL = 30 * time.Second
	if err := c.Validate(); err != nil {
		t.Fatalf("valid production RTC config: %v", err)
	}
}

func TestProductionCombinedPushRequiresCompleteAPNSVoIPConfiguration(t *testing.T) {
	c := validConfig()
	c.Mode = "full"
	c.Environment = "production"
	c.DatabaseURL = "postgres://db"
	c.AdminEmail = "ops@example.com"
	c.AdminPasswordHash = "$2a$12$example"
	c.AdminTOTPSecret = "JBSWY3DPEHPK3PXP"
	c.OTPWebhookURL = "https://otp.example.com"
	c.OTPWebhookToken = strings.Repeat("o", 24)
	c.PushProvider = "getui_apns_voip"
	c.GetuiAppID = "app-id"
	c.GetuiAppKey = "app-key"
	c.GetuiMasterSecret = strings.Repeat("m", 16)
	c.RTCSTUNURLs = []string{"stun:stun.example.com:3478"}
	c.RTCTURNURLs = []string{"turns:turn.example.com:5349"}
	c.RTCTURNUsername = "turn-user"
	c.RTCTURNCredential = strings.Repeat("t", 16)
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "APNs VoIP") {
		t.Fatalf("missing APNs config err=%v", err)
	}
	c.APNSVoIPKeyID = "KEYID12345"
	c.APNSVoIPTeamID = "TEAMID1234"
	c.APNSVoIPBundleID = "com.linlitong.imapp"
	c.APNSVoIPKeyFile = "/run/secrets/apns_auth_key.p8"
	if err := c.Validate(); err != nil {
		t.Fatalf("complete combined push config: %v", err)
	}
	c.APNSVoIPBundleID = "com.linlitong.imapp.voip"
	if err := c.Validate(); err == nil {
		t.Fatal("bundle id must not include .voip suffix")
	}
}
