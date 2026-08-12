package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	c := Config{
		Addr: "127.0.0.1:8080", DatabaseURL: "postgres://db",
		JWTSecret: strings.Repeat("j", 32), AdminKey: strings.Repeat("a", 24),
		AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour, MediaMaxBytes: 100 << 20,
		DevMode: true, DevOTPCode: "654321",
	}
	configureWukong(&c)
	return c
}

func configureLiveKit(c *Config) {
	c.LiveKitEnabled = true
	c.LiveKitURL = "wss://chat.example.com/rtc"
	c.LiveKitAPIURL = "http://livekit:7880"
	c.LiveKitAPIKey = "livekit-key"
	c.LiveKitAPISecret = strings.Repeat("l", 32)
	c.LiveKitTokenTTL = 5 * time.Minute
}

func configureWukong(c *Config) {
	c.WukongEnabled = true
	c.WukongAPIURL = "http://wukongim:5001"
	c.WukongManagerURL = "http://wukongim:5300"
	c.WukongManagerToken = strings.Repeat("m", 24)
	c.WukongTokenSecret = strings.Repeat("t", 32)
	c.WukongPolicySecret = strings.Repeat("p", 32)
	c.WukongGRPCAddr = ":6970"
	c.WukongTCPURL = "tcp://im.example.com:5100"
	c.WukongWSURL = "wss://im.example.com/ws"
	c.WukongPluginDir = "/var/lib/nexachat/wukong-plugins"
	c.WukongPluginTrustedKeys = "release-key:" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	c.WukongPluginAllowlist = "wk.plugin.im-policy"
	c.WukongPluginMaxBytes = 64 << 20
}

func configureProductionRealtime(c *Config) {
	configureWukong(c)
	configureLiveKit(c)
}

func TestValidateFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"missing jwt", func(c *Config) { c.JWTSecret = "" }},
		{"bootstrap missing admin key", func(c *Config) { c.AdminSharedKeyEnabled = true; c.AdminKey = "" }},
		{"shared admin key outside development", func(c *Config) { c.AdminSharedKeyEnabled = true; c.DevMode = false }},
		{"demo seed outside development", func(c *Config) { c.SeedDemo = true; c.DevMode = false }},
		{"full without database", func(c *Config) { c.DatabaseURL = "" }},
		{"dev without otp", func(c *Config) { c.DevOTPCode = "" }},
		{"dev public bind", func(c *Config) { c.Addr = ":8080" }},
		{"production rejects dev", func(c *Config) { c.Environment = "production" }},
		{"media limit too small", func(c *Config) { c.MediaMaxBytes = 1024 }},
		{"android media endpoint outside development", func(c *Config) { c.S3AndroidPublicEndpoint = "10.0.2.2:9000"; c.DevMode = false }},
		{"wukong internal rate limit too small", func(c *Config) { c.WukongInternalRateLimitPerMinute = 59999 }},
		{"wukong internal rate limit too large", func(c *Config) { c.WukongInternalRateLimitPerMinute = 600001 }},
		{"container flag needs development env", func(c *Config) {
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
	developmentMedia := validConfig()
	developmentMedia.DevMode = true
	developmentMedia.DevOTPCode = "654321"
	developmentMedia.S3AndroidPublicEndpoint = "10.0.2.2:9000"
	developmentMedia.SeedDemo = true
	if err := developmentMedia.Validate(); err != nil {
		t.Fatalf("development Android media endpoint: %v", err)
	}
	container := validConfig()
	container.DatabaseURL = "postgres://db"
	container.DevMode = true
	container.DevOTPCode = "654321"
	container.Addr = ":8080"
	container.Environment = "development"
	container.DevAllowContainerBind = true
	container.DevIPTestOnly = true
	configureWukong(&container)
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
	c.DevMode = false
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
	configureProductionRealtime(&c)
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

func TestFullModeRequiresWukongAndProductionRequiresLiveKit(t *testing.T) {
	c := validConfig()
	c.DevMode = false
	c.DatabaseURL = "postgres://db"
	c.AdminEmail = "admin@example.com"
	c.AdminPasswordHash = "$2a$12$example"
	c.AdminTOTPSecret = "JBSWY3DPEHPK3PXP"
	c.OTPWebhookURL = "https://otp.example.com"
	c.OTPWebhookToken = strings.Repeat("o", 24)
	c.PushProvider = "webhook"
	c.PushWebhookURL = "https://push.example.com"
	c.PushWebhookToken = strings.Repeat("p", 24)
	c.WukongEnabled = false
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "WuKongIM is required") {
		t.Fatalf("missing WuKongIM config err=%v", err)
	}
	configureWukong(&c)
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "LiveKit") {
		t.Fatalf("missing LiveKit config err=%v", err)
	}
	configureLiveKit(&c)
	c.CallInviteTTL = 30 * time.Second
	if err := c.Validate(); err != nil {
		t.Fatalf("valid production LiveKit config: %v", err)
	}
}

func TestProductionLiveKitValidatesTokenTTL(t *testing.T) {
	c := validConfig()
	c.DevMode = false
	c.DatabaseURL = "postgres://db"
	c.AdminEmail = "admin@example.com"
	c.AdminPasswordHash = "$2a$12$example"
	c.AdminTOTPSecret = "JBSWY3DPEHPK3PXP"
	c.OTPWebhookURL = "https://otp.example.com"
	c.OTPWebhookToken = strings.Repeat("o", 24)
	c.PushProvider = "webhook"
	c.PushWebhookURL = "https://push.example.com"
	c.PushWebhookToken = strings.Repeat("p", 24)
	configureProductionRealtime(&c)
	if err := c.Validate(); err != nil {
		t.Fatalf("valid LiveKit production config: %v", err)
	}
	c.LiveKitTokenTTL = 30 * time.Second
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "TOKEN_TTL") {
		t.Fatalf("unsafe token TTL err=%v", err)
	}
}

func TestProductionCombinedPushRequiresCompleteAPNSVoIPConfiguration(t *testing.T) {
	c := validConfig()
	c.DevMode = false
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
	configureProductionRealtime(&c)
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

func TestWukongRequiresIndependentPolicySecret(t *testing.T) {
	c := validConfig()
	c.WukongEnabled = true
	c.WukongAPIURL = "http://wukongim:5001"
	c.WukongManagerURL = "http://wukongim:5300"
	c.WukongManagerToken = strings.Repeat("m", 24)
	c.WukongTokenSecret = strings.Repeat("t", 32)
	c.WukongPolicySecret = ""
	c.WukongGRPCAddr = ":6970"
	c.WukongTCPURL = "tcp://im.example.com:5100"
	c.WukongWSURL = "wss://im.example.com/ws"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "policy secrets") {
		t.Fatalf("missing policy secret err=%v", err)
	}
	c.WukongPolicySecret = strings.Repeat("p", 32)
	if err := c.Validate(); err != nil {
		t.Fatalf("complete WuKong config: %v", err)
	}
}

func TestWukongSignedPluginLifecycleRejectsPartialOrInvalidTrust(t *testing.T) {
	c := validConfig()
	c.WukongPluginDir = ""
	c.WukongPluginTrustedKeys = ""
	c.WukongPluginAllowlist = ""
	c.WukongEnabled = true
	c.WukongAPIURL = "http://wukongim:5001"
	c.WukongManagerURL = "http://wukongim:5300"
	c.WukongManagerToken = strings.Repeat("m", 24)
	c.WukongTokenSecret = strings.Repeat("t", 32)
	c.WukongPolicySecret = strings.Repeat("p", 32)
	c.WukongGRPCAddr = ":6970"
	c.WukongTCPURL = "tcp://im.example.com:5100"
	c.WukongWSURL = "wss://im.example.com/ws"
	if err := c.Validate(); err != nil {
		t.Fatalf("optional lifecycle omitted in development: %v", err)
	}
	c.WukongPluginDir = "/var/lib/nexachat/wukong-plugins"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "directory, trusted keys") {
		t.Fatalf("partial plugin lifecycle err=%v", err)
	}
	c.WukongPluginTrustedKeys = "release-key:not-base64"
	c.WukongPluginAllowlist = "wk.plugin.safe"
	c.WukongPluginMaxBytes = 64 << 20
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "trusted keys") {
		t.Fatalf("invalid trusted key err=%v", err)
	}
	c.WukongPluginTrustedKeys = "release-key:" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := c.Validate(); err != nil {
		t.Fatalf("valid signed plugin lifecycle: %v", err)
	}
}
