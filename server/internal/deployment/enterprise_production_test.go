package deployment

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/tenancy"
)

func productionBundleFixture(t *testing.T) EnterpriseConfig {
	t.Helper()
	c := bundleTestConfig(t)
	c.PlatformControlURL, c.PlatformWebOrigin = "https://platform.control.example.test:8443", "https://app.example.test"
	c.Ports.HTTP, c.Ports.Media = 443, 9443
	c.Production = &EnterpriseProduction{APIOrigin: "https://tenant-a.example.test", MediaOrigin: "https://media-a.example.test:9443", ControlURL: "https://tenant-a.control.example.test:21446", PublicBindIP: "0.0.0.0", ControlBindIP: "10.40.0.12", IMHost: "tenant-a.example.test", RTCNodeIP: "203.0.113.12"}
	issue := bundleTestPKI(t, "tenant-a.example.test", "media-a.example.test", "tenant-a.control.example.test")
	c.PublicTLS, c.ControlTLS = issue(""), issue(tenancy.EnterpriseIdentity(c.TenantID))
	return c
}

func TestProductionEnterpriseProfileAndRuntime(t *testing.T) {
	c := productionBundleFixture(t)
	input := Release{ID: "production-one", Sequence: 1, Runtime: "linux/amd64", SchemaVersion: 79}
	raw, release, err := BuildEnterpriseBundle(c, input)
	if err != nil {
		t.Fatal(err)
	}
	again, repeated, err := BuildEnterpriseBundle(c, input)
	if err != nil || !bytes.Equal(raw, again) || release.Digest() != repeated.Digest() || release.IsolationMode != "dedicated_host" {
		t.Fatal("profile is not immutable and target-bound")
	}
	var b Bundle
	if json.Unmarshal(raw, &b) != nil || validateBundle(b, release) != nil {
		t.Fatal("invalid produced bundle")
	}
	api := b.Services["enterprise-api"].Environment
	if api["IM_ENV"] != "production" || api["IM_S3_PUBLIC_ENDPOINT"] != "media-a.example.test:9443" || api["IM_WUKONG_TCP_URL"] != "tcp://tenant-a.example.test:21180" || api["IM_LIVEKIT_URL"] != "wss://tenant-a.example.test/livekit" {
		t.Fatal("preview addresses retained")
	}
	if !strings.Contains(b.Services["enterprise-livekit"].Environment[RuntimeFilesEnvironment], "node_ip: 203.0.113.12") {
		t.Fatal("preview ICE candidate")
	}
	for name, service := range b.Services {
		for _, port := range service.Ports {
			if !productionPort(name, port) {
				t.Fatal("unsafe published role", name)
			}
		}
	}
	// The generated environment must pass the REAL business runtime validator,
	// not just a second rendering-specific boolean check.
	for key, value := range api {
		if key != RuntimeFilesEnvironment {
			t.Setenv(key, value)
		}
	}
	dir := t.TempDir()
	for name, value := range map[string]string{"ca.pem": c.ControlTLS.CA, "cert.pem": c.ControlTLS.Certificate, "key.pem": c.ControlTLS.PrivateKey} {
		if os.WriteFile(filepath.Join(dir, name), []byte(value), 0600) != nil {
			t.Fatal("fixture TLS")
		}
	}
	t.Setenv("IM_TENANT_CA_FILE", filepath.Join(dir, "ca.pem"))
	t.Setenv("IM_TENANT_CERT_FILE", filepath.Join(dir, "cert.pem"))
	t.Setenv("IM_TENANT_KEY_FILE", filepath.Join(dir, "key.pem"))
	runtime := config.Load()
	if err := runtime.Validate(); err != nil {
		t.Fatal("generated production runtime rejected", err)
	}
	for name, mutation := range map[string]func(*config.Config){
		"fixed OTP":             func(c *config.Config) { c.DevOTPCode = "123456" },
		"dev auth":              func(c *config.Config) { c.DevMode = true },
		"preview":               func(c *config.Config) { c.TenancyPreview = true },
		"wildcard CORS":         func(c *config.Config) { c.AllowedOrigins = []string{"*"} },
		"shared keys":           func(c *config.Config) { c.S3SecretKey = c.JWTSecret },
		"unverified forwarding": func(c *config.Config) { c.TrustProxy = true },
		"shared gateway proof":  func(c *config.Config) { c.GatewaySecret = c.JWTSecret },
		"shared DB": func(c *config.Config) {
			c.DatabaseURL = strings.ReplaceAll(c.DatabaseURL, "enterprise-db", "other-enterprise-db")
		},
		"direct push":            func(c *config.Config) { c.PushProvider = "getui" },
		"raw call endpoint":      func(c *config.Config) { c.LiveKitURL = "wss://tenant-a.example.test:7880" },
		"wrong certificate role": func(c *config.Config) { c.TenantID = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := runtime
			mutation(&changed)
			if changed.Validate() == nil {
				t.Fatal("unsafe runtime accepted")
			}
		})
	}
	local := release
	local.IsolationMode = ""
	if validateBundle(b, local) == nil {
		t.Fatal("production ports accepted as preview")
	}
	root := t.TempDir()
	if _, err := WriteEnterpriseRelease(root, c, input); err != nil {
		t.Fatal(err)
	}
	runner := ComposeRunner{Binary: filepath.Join(root, "docker.exe"), BundleRoot: root, Endpoint: "npipe:////./pipe/dockerDesktopLinuxEngine", Project: "frogim-deploy-" + c.TenantID, Server: c.ServerID, Tenant: c.TenantID, Catalog: map[string]Release{release.ID: release}}
	op := Operation{ServerID: c.ServerID, TenantID: c.TenantID, ReleaseID: release.ID, ReleaseDigest: release.Digest()}
	if _, _, err := runner.bundle(release, op); err == nil {
		t.Fatal("preview runner accepted a production release")
	}
	runner.IsolationMode, runner.Endpoint = "dedicated_host", "unix:///var/run/docker.sock"
	if _, _, err := runner.bundle(release, op); err != nil {
		t.Fatal("dedicated target rejected", err)
	}
}

func TestProductionBundleRejectsUnsafeEndpointsAndPorts(t *testing.T) {
	c := productionBundleFixture(t)
	r := Release{ID: "production-two", Sequence: 2, Runtime: "linux/amd64", SchemaVersion: 79}
	for name, mutate := range map[string]func(*EnterpriseConfig){
		"TLS wrong name":        func(c *EnterpriseConfig) { c.Production.APIOrigin = "https://other.example.test" },
		"media wrong name":      func(c *EnterpriseConfig) { c.Production.MediaOrigin = "https://other.example.test:9443" },
		"public control bind":   func(c *EnterpriseConfig) { c.Production.ControlBindIP = "0.0.0.0" },
		"loopback api":          func(c *EnterpriseConfig) { c.Production.APIOrigin = "https://127.0.0.1" },
		"loopback ICE":          func(c *EnterpriseConfig) { c.Production.RTCNodeIP = "127.0.0.1" },
		"wildcard ICE":          func(c *EnterpriseConfig) { c.Production.RTCNodeIP = "0.0.0.0" },
		"raw LiveKit":           func(c *EnterpriseConfig) { c.Ports.RTCTCP = 7880 },
		"wrong advertised port": func(c *EnterpriseConfig) { c.Ports.HTTP = 9444 },
		"wildcard CORS":         func(c *EnterpriseConfig) { c.PlatformWebOrigin = "*" },
		"same TLS key":          func(c *EnterpriseConfig) { c.PublicTLS = c.ControlTLS },
		"preview origin":        func(c *EnterpriseConfig) { c.PlatformWebOrigin = "https://127.0.0.1:18443" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := c
			profile := *c.Production
			copy.Production = &profile
			mutate(&copy)
			if _, _, err := BuildEnterpriseBundle(copy, r); err == nil {
				t.Fatal("unsafe production profile")
			}
		})
	}
	raw, release, err := BuildEnterpriseBundle(c, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		name string
		port Port
	}{
		{"enterprise-db", Port{Target: 5432, Published: "5432", HostIP: "0.0.0.0", Protocol: "tcp"}},
		{"enterprise-api", Port{Target: 8444, Published: "8444", HostIP: "0.0.0.0", Protocol: "tcp"}},
		{"enterprise-api", Port{Target: 8080, Published: "8080", HostIP: "10.40.0.12", Protocol: "tcp"}},
		{"enterprise-im", Port{Target: 5300, Published: "5300", HostIP: "0.0.0.0", Protocol: "tcp"}},
		{"enterprise-livekit", Port{Target: 7880, Published: "7880", HostIP: "0.0.0.0", Protocol: "tcp"}},
	} {
		var bundle Bundle
		_ = json.Unmarshal(raw, &bundle)
		s := bundle.Services[target.name]
		s.Ports = append(s.Ports, target.port)
		bundle.Services[target.name] = s
		if validateBundle(bundle, release) == nil {
			t.Fatal("private endpoint published", target.name)
		}
	}
}
