package deployment

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
)

func edgeFixture(t *testing.T) EdgeConfig {
	p := platformBundleFixture(t)
	p.SharedDatastores = true
	a, _ := tenancy.Secret()
	b, _ := tenancy.Secret()
	p.SharedIngress = &SharedIngress{Secret: a, HTTPSPort: 18443}
	e := productionBundleFixture(t)
	e.TenantID = "default"
	e.PlatformControlURL = p.ControlURL
	e.Secrets.Redis = p.RedisSecret
	p.Peers[0].ServerID = e.ServerID
	p.Peers[0].ControlURL = e.Production.ControlURL
	e.PlatformWebOrigin = p.PublicURL
	e.Ports.HTTP, e.Ports.Media = 18444, 18445
	e.Production.APIOrigin, e.Production.MediaOrigin = p.PublicURL, p.PublicURL
	e.Production.SharedIngress = &SharedIngress{Secret: b}
	e.SharedDatastores = &SharedDatastores{Database: "enterprise", RedisDB: 1}
	e.MediaBucket = "nexachat-media"
	issue := bundleTestPKI(t, "app.example.test", "tenant-a.control.example.test")
	e.PublicTLS, e.ControlTLS = issue(""), issue(tenancy.EnterpriseIdentity("default"))
	return EdgeConfig{Platform: p, Enterprise: e, CertificateDirectory: "/data/linli-im/shared/letsencrypt", DownloadsDirectory: "/data/linli-im/shared/downloads", LegalDirectory: "/opt/frogim/legal"}
}

func TestSharedIngressBundles(t *testing.T) {
	c := edgeFixture(t)
	raw, r, err := BuildEnterpriseBundle(c.Enterprise, Release{ID: "single-edge", Sequence: 1, Runtime: "linux/amd64", SchemaVersion: 79})
	if err != nil {
		t.Fatal(err)
	}
	var b Bundle
	_ = json.Unmarshal(raw, &b)
	if r.IngressMode != SharedIngressMode || validateBundle(b, r) != nil || b.Services["enterprise-api"].Environment["IM_S3_BUCKET"] != "nexachat-media" {
		t.Fatal("missing ingress/bucket binding")
	}
	g := b.Services["enterprise-gateway"]
	g.Ports[0].HostIP = "0.0.0.0"
	b.Services["enterprise-gateway"] = g
	if validateBundle(b, r) == nil {
		t.Fatal("public backend accepted")
	}
	if _, _, err := BuildPlatformBundle(c.Platform, "platform-edge"); err != nil {
		t.Fatal(err)
	}
	edge, err := BuildEdgeBundle(c)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(c.Platform.PublicURL)
	if !bytes.Contains(edge, []byte("default_sni "+u.Hostname())) {
		t.Fatal("edge must select its configured certificate for clients without SNI")
	}
	c.Enterprise.Production.SharedIngress = nil
	if c.Enterprise.Validate() == nil {
		t.Fatal("same origin accepted without explicit edge profile")
	}
}

func TestSharedIngressCaddyAdapter(t *testing.T) {
	if os.Getenv("FROGIM_TEST_CADDY") != "1" {
		t.Skip("set FROGIM_TEST_CADDY=1 for pinned real Caddy adapter")
	}
	c := edgeFixture(t)
	raw, err := BuildEdgeBundle(c)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct{ Environment map[string]string }
	}
	if json.Unmarshal(bytes.ReplaceAll(raw, []byte("$$"), []byte("$")), &doc) != nil {
		t.Fatal("compose decode")
	}
	for name, config := range map[string]string{"edge": doc.Services["gateway"].Environment["EDGE_CADDYFILE"], "enterprise": c.Enterprise.gatewayConfiguration(), "platform": c.Platform.gatewayConfiguration()} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("docker", "run", "--rm", "-i", "-e", "FROGIM_EDGE_SECRET=fixture-proof", "--entrypoint", "caddy", "caddy@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d", "adapt", "--adapter", "caddyfile", "--config", "/dev/stdin")
			cmd.Stdin = strings.NewReader(config)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("adapter failed: %s", out)
			}
			if name == "edge" {
				var adapted any
				if err := json.Unmarshal(out, &adapted); err != nil {
					t.Fatal(err)
				}
				found := false
				var visit func(any)
				visit = func(value any) {
					switch node := value.(type) {
					case map[string]any:
						match, _ := json.Marshal(node["match"])
						if bytes.Contains(match, []byte(`"/web/*"`)) {
							route, _ := json.Marshal(node)
							found = bytes.Contains(route, []byte(`"Location":["/app/"]`)) && bytes.Contains(route, []byte(`"status_code":308`))
						}
						for _, child := range node {
							visit(child)
						}
					case []any:
						for _, child := range node {
							visit(child)
						}
					}
				}
				visit(adapted)
				if !found {
					t.Fatal("legacy Web path must redirect to /app/ with HTTP 308")
				}
			}
		})
	}
}
