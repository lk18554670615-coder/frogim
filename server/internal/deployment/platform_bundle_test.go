package deployment

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
)

func platformBundleFixture(t *testing.T) PlatformConfig {
	t.Helper()
	s, err := NewEnterpriseSecrets("synthetic-platform-operator-password")
	if err != nil {
		t.Fatal(err)
	}
	issue := bundleTestPKI(t, "app.example.test", "platform-control.example.test")
	return PlatformConfig{ToolsImage: "frogim/platform-bundle@sha256:" + strings.Repeat("a", 64), PublicURL: "https://app.example.test", ControlURL: "https://platform-control.example.test:8443", PublicBindIP: "0.0.0.0", ControlBindIP: "10.40.0.10", DatabaseSecret: s.Database, RedisSecret: s.Redis, GatewaySecret: s.JWT, AdminUsername: "operator", AdminPasswordHash: s.AdminPasswordHash, PublicTLS: issue(""), ControlTLS: issue(tenancy.PlatformIdentity), Peers: []PlatformPeer{{TenantID: "default", ServerID: "default-server", ControlURL: "https://enterprise-control.example.test:8444", AgentURL: "https://agent.example.test:8450"}}}
}
func TestPlatformProductionBundle(t *testing.T) {
	c := platformBundleFixture(t)
	raw, release, err := BuildPlatformBundle(c, "platform-one")
	if err != nil {
		t.Fatal(err)
	}
	again, repeated, err := BuildPlatformBundle(c, "platform-one")
	if err != nil || release != repeated || !bytes.Equal(raw, again) {
		t.Fatal("non-deterministic artifact")
	}
	var b Bundle
	if json.Unmarshal(bytes.ReplaceAll(raw, []byte("$$"), []byte("$")), &b) != nil || len(b.Services) != 4 || !b.Networks["platform"].Internal {
		t.Fatal("platform isolation graph")
	}
	for name, s := range b.Services {
		if !imageReference.MatchString(s.Image) || s.Healthcheck == nil {
			t.Fatal("mutable/unhealthy service", name)
		}
		for _, v := range s.Volumes {
			if v.Type != "volume" {
				t.Fatal("host mount")
			}
		}
		switch name {
		case "platform-db", "platform-redis":
			if len(s.Ports) != 0 || len(s.Networks) != 1 || s.Networks[0] != "platform" {
				t.Fatal("public datastore")
			}
		case "platform-api":
			if len(s.Ports) != 1 || s.Ports[0].HostIP != c.ControlBindIP || s.Ports[0].Target != 8443 || s.Environment["PLATFORM_ENV"] != "production" || s.Environment["PLATFORM_PUSH_PROVIDER"] != "disabled" || s.Environment["PLATFORM_DEPLOYMENT_CATALOG_FILE"] != "" {
				t.Fatal("unsafe API")
			}
			if s.Environment["PLATFORM_ADMIN_PASSWORD_HASH"] != c.AdminPasswordHash {
				t.Fatal("Compose interpolation damaged bcrypt")
			}
		case "platform-gateway":
			if len(s.Ports) != 1 || s.Ports[0].Target != 8443 || s.Ports[0].Published != "443" {
				t.Fatal("public API port")
			}
			encoded, _ := json.Marshal(s)
			if bytes.Contains(encoded, []byte(c.ControlTLS.PrivateKey)) || strings.Contains(s.Environment[RuntimeFilesEnvironment], c.DatabaseSecret) {
				t.Fatal("credential crossed role")
			}
			if s.Environment["FROGIM_GATEWAY_SECRET"] != c.GatewaySecret || s.Environment["FROGIM_PLATFORM_PUBLIC_URL"] != c.PublicURL {
				t.Fatal("gateway context missing")
			}
		default:
			t.Fatal("business service at platform", name)
		}
		if encoded := s.Environment[RuntimeFilesEnvironment]; encoded != "" {
			role := s.Entrypoint[1]
			if err := PrepareRuntime(role, t.TempDir(), encoded); err != nil {
				t.Fatal("runtime files rejected", role, err)
			}
		}
	}
	root := t.TempDir()
	if _, err = WritePlatformRelease(root, c, release.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = WritePlatformRelease(root, c, release.ID); err != nil {
		t.Fatal("idempotent render", err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, release.ID, "release.json"))
	if err != nil || bytes.Contains(manifest, []byte(c.DatabaseSecret)) || bytes.Contains(manifest, []byte("PRIVATE KEY")) {
		t.Fatal("manifest secrecy")
	}
	changed := c
	changed.AdminUsername = "changed-operator"
	if _, err = WritePlatformRelease(root, changed, release.ID); err == nil {
		t.Fatal("overwrote immutable release")
	}
	if _, err = WritePlatformRelease(root, c, "../escape"); err == nil {
		t.Fatal("unsafe release ID")
	}
	config, _ := json.Marshal(c)
	if _, err = ReadPlatformConfig(bytes.NewReader(config)); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadPlatformConfig(bytes.NewReader(append(config, config...))); err == nil {
		t.Fatal("trailing config accepted")
	}
}

func TestPlatformProductionRejectsUnsafeConfiguration(t *testing.T) {
	c := platformBundleFixture(t)
	for name, mutate := range map[string]func(*PlatformConfig){
		"mutable image":         func(c *PlatformConfig) { c.ToolsImage = "frogim/platform:latest" },
		"preview URL":           func(c *PlatformConfig) { c.PublicURL = "https://127.0.0.1:18443" },
		"public control":        func(c *PlatformConfig) { c.ControlURL = "https://203.0.113.2:8443" },
		"preview control":       func(c *PlatformConfig) { c.ControlURL = "https://host.docker.internal:8443" },
		"certificate hostname":  func(c *PlatformConfig) { c.PublicURL = "https://foreign.example.test" },
		"wrong identity":        func(c *PlatformConfig) { c.ControlTLS = c.PublicTLS },
		"public control bind":   func(c *PlatformConfig) { c.ControlBindIP = "0.0.0.0" },
		"overlapping ports":     func(c *PlatformConfig) { c.PublicURL = "https://app.example.test:8443" },
		"shared secrets":        func(c *PlatformConfig) { c.RedisSecret = c.DatabaseSecret },
		"shared gateway secret": func(c *PlatformConfig) { c.GatewaySecret = c.DatabaseSecret },
		"weak secret":           func(c *PlatformConfig) { c.DatabaseSecret = "example-password" },
		"plaintext admin":       func(c *PlatformConfig) { c.AdminPasswordHash = "operator-password" },
		"duplicate peers":       func(c *PlatformConfig) { c.Peers = append(c.Peers, c.Peers[0]) },
		"duplicate addresses": func(c *PlatformConfig) {
			c.Peers = []PlatformPeer{{TenantID: "default", ServerID: "server", ControlURL: c.ControlURL, AgentURL: "https://agent.example.test:8450"}}
		},
		"unbound catalog": func(c *PlatformConfig) {
			c.Catalog = []Release{{ID: "one", Sequence: 1, Runtime: "linux/amd64", SchemaVersion: 79, ComposeSHA256: strings.Repeat("a", 64)}}
		},
		"env override": func(c *PlatformConfig) { c.Suppliers = map[string]string{"PLATFORM_ENV": "development"} },
		"fixed OTP":    func(c *PlatformConfig) { c.Suppliers = map[string]string{"PLATFORM_DEV_OTP_CODE": "123456"} },
		"private OTP": func(c *PlatformConfig) {
			c.Suppliers = map[string]string{"PLATFORM_OTP_WEBHOOK_URL": "https://127.0.0.1", "PLATFORM_OTP_WEBHOOK_TOKEN": strings.Repeat("a", 32)}
		},
		"partial OTP": func(c *PlatformConfig) {
			c.Suppliers = map[string]string{"PLATFORM_OTP_WEBHOOK_URL": "https://sms.example.test"}
		},
		"push key without supplier": func(c *PlatformConfig) {
			c.Suppliers = map[string]string{"PLATFORM_PUSH_ENCRYPTION_KEY": strings.Repeat("a", 43)}
		},
		"partial push": func(c *PlatformConfig) { c.Suppliers = map[string]string{"PLATFORM_PUSH_PROVIDER": "getui"} },
		"key file escape": func(c *PlatformConfig) {
			c.Suppliers = map[string]string{"PLATFORM_APNS_VOIP_KEY_FILE": "/host/private.pem"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := c
			mutate(&changed)
			if _, _, err := BuildPlatformBundle(changed, "one"); err == nil {
				t.Fatal("unsafe production config accepted")
			}
		})
	}
	c.Suppliers = map[string]string{"PLATFORM_OTP_WEBHOOK_URL": "https://sms.example.test", "PLATFORM_OTP_WEBHOOK_TOKEN": strings.Repeat("o", 32), "PLATFORM_PASSWORD_RESET_SMS_URL": "https://sms.example.test/recovery", "PLATFORM_PASSWORD_RESET_SMS_TOKEN": strings.Repeat("r", 32)}
	if c.Validate() != nil {
		t.Fatal("explicit real-provider config rejected")
	}
}

func TestPlatformClientImageBinding(t *testing.T) {
	if strings.Contains(platformCaddyConfig, "script-src 'self' 'unsafe-inline'") || !strings.Contains(platformCaddyConfig, "header_up X-Frogim-Gateway") {
		t.Fatal("unsafe gateway script/forwarding policy")
	}
	raw := []byte(`{"platformUrl":"https://app.example.test","environment":"production","baseHref":"/app/"}`)
	if ValidatePlatformWebMetadata(raw, "https://app.example.test") != nil {
		t.Fatal("valid client binding rejected")
	}
	for _, origin := range []string{"https://foreign.example.test", "https://127.0.0.1:18443", ""} {
		if ValidatePlatformWebMetadata(raw, origin) == nil {
			t.Fatal("foreign client image accepted")
		}
	}
	for _, bad := range [][]byte{bytes.ReplaceAll(raw, []byte("production"), []byte("development")), bytes.ReplaceAll(raw, []byte("/app/"), []byte("/")), append(raw, raw...)} {
		if ValidatePlatformWebMetadata(bad, "https://app.example.test") == nil {
			t.Fatal("invalid client metadata accepted")
		}
	}
}
