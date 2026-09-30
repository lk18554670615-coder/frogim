package deployment

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real edge -> verified HTTPS gateway -> upstream requests. Only fixture
// listeners/paths change; production route and proof generation is unchanged.
func TestSharedIngressHTTP(t *testing.T) {
	if os.Getenv("FROGIM_TEST_CADDY") != "1" {
		t.Skip("requires pinned local Caddy image")
	}
	c := edgeFixture(t)
	raw, err := BuildEdgeBundle(c)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct{ Environment map[string]string }
	}
	_ = json.Unmarshal(bytes.ReplaceAll(raw, []byte("$$"), []byte("$")), &doc)
	edge := doc.Services["gateway"].Environment["EDGE_CADDYFILE"]
	edge = strings.ReplaceAll(edge, "https://10.40.0.10:18443", "https://127.0.0.1:8443")
	edge = strings.ReplaceAll(edge, "https://10.40.0.12:18444", "https://127.0.0.1:8444")
	edge = strings.ReplaceAll(edge, "https://10.40.0.12:18445", "https://127.0.0.1:8445")
	edge = strings.ReplaceAll(edge, "/etc/letsencrypt/live/app.example.test/fullchain.pem", "/config/platform.pem")
	edge = strings.ReplaceAll(edge, "/etc/letsencrypt/live/app.example.test/privkey.pem", "/config/platform.key")
	enterprise := c.Enterprise.gatewayConfiguration()
	enterprise = enterprise[strings.Index(enterprise, "https://:8444"):]
	enterprise = strings.ReplaceAll(enterprise, "http://:8080 {\n  respond /health \"gateway-ready\"\n}\n", "")
	enterprise = strings.ReplaceAll(enterprise, "/config/public.", "/config/enterprise.")
	enterprise = strings.ReplaceAll(enterprise, "{$FROGIM_EDGE_SECRET}", c.Enterprise.Production.SharedIngress.Secret)
	enterprise = strings.ReplaceAll(enterprise, "{$FROGIM_GATEWAY_SECRET}", "enterprise-proof")
	platform := c.Platform.gatewayConfiguration()
	platform = platform[strings.Index(platform, "https://:8443"):]
	platform = strings.ReplaceAll(platform, "/config/public.", "/config/platform.")
	platform = strings.ReplaceAll(platform, "{$FROGIM_EDGE_SECRET}", c.Platform.SharedIngress.Secret)
	platform = strings.ReplaceAll(platform, "{$FROGIM_GATEWAY_SECRET}", "platform-proof")
	config := edge + enterprise + platform + `
http://:19000 {
  respond "{http.request.method}|{http.request.uri}|{http.request.header.X-Frogim-Gateway}|{http.request.header.X-Frogim-Edge}|{http.request.host}|{http.request.header.X-Real-IP}"
}
`
	for _, upstream := range []string{"enterprise-api:8080", "platform-api:8090", "enterprise-im:5200", "enterprise-minio:9000"} {
		config = strings.ReplaceAll(config, upstream, "127.0.0.1:19000")
	}
	dir := t.TempDir()
	for _, role := range []string{"admin", "platform", "web"} {
		if os.Mkdir(filepath.Join(dir, role), 0700) != nil {
			t.Fatal("fixture mkdir")
		}
		name := "index.html"
		if role == "platform" {
			name = "platform.html"
		}
		if os.WriteFile(filepath.Join(dir, role, name), []byte("fixture-"+role), 0600) != nil {
			t.Fatal("fixture static")
		}
		config = strings.ReplaceAll(config, "/srv/"+role, "/config/"+role)
	}
	for name, value := range map[string]string{"Caddyfile": config, "platform.pem": c.Platform.PublicTLS.Certificate, "platform.key": c.Platform.PublicTLS.PrivateKey, "platform-ca.pem": c.Platform.PublicTLS.CA, "enterprise.pem": c.Enterprise.PublicTLS.Certificate, "enterprise.key": c.Enterprise.PublicTLS.PrivateKey, "enterprise-ca.pem": c.Enterprise.PublicTLS.CA} {
		if os.WriteFile(filepath.Join(dir, name), []byte(value), 0600) != nil {
			t.Fatal("fixture write")
		}
	}
	cmd := exec.Command("docker", "run", "-d", "--rm", "-p", "127.0.0.1::443", "-p", "127.0.0.1::8444", "--mount", "type=bind,source="+dir+",target=/config,readonly", "-e", "PLATFORM_EDGE_SECRET="+c.Platform.SharedIngress.Secret, "-e", "ENTERPRISE_EDGE_SECRET="+c.Enterprise.Production.SharedIngress.Secret, "caddy@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d", "caddy", "run", "--config", "/config/Caddyfile", "--adapter", "caddyfile")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker fixture: %s", out)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { _ = exec.Command("docker", "stop", "-t", "1", id).Run() })
	port := func(target string) string {
		out, err := exec.Command("docker", "port", id, target).Output()
		if err != nil {
			t.Fatal(err)
		}
		return "https://" + strings.TrimSpace(string(out))
	}
	edgeURL, backendURL := port("443/tcp"), port("8444/tcp")
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(c.Platform.PublicTLS.CA))
	roots.AppendCertsFromPEM([]byte(c.Enterprise.PublicTLS.CA))
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "app.example.test", MinVersion: tls.VersionTLS12}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	request := func(base, route, method string) (int, string, error) {
		req, _ := http.NewRequest(method, base+route, nil)
		req.Host = "app.example.test"
		req.Header.Set("X-Frogim-Gateway", "forged")
		req.Header.Set("X-Frogim-Edge", "forged")
		req.Header.Set("X-Frogim-Client-IP", "1.2.3.4")
		req.Header.Set("X-Real-IP", "1.2.3.4")
		res, e := client.Do(req)
		if e != nil {
			return 0, "", e
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			return res.StatusCode, res.Header.Get("Location"), nil
		}
		return res.StatusCode, string(b), nil
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, _, e := request(edgeURL, "/rtc", "GET"); e == nil {
			break
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", id).CombinedOutput()
			t.Fatalf("Caddy startup: %s", logs)
		}
		time.Sleep(100 * time.Millisecond)
	}
	for route, want := range map[string]string{"/admin/": "fixture-admin", "/admin/users": "fixture-admin", "/platform/": "fixture-platform", "/app/": "fixture-web"} {
		status, body, e := request(edgeURL, route, "GET")
		if e != nil || status != 200 || body != want {
			t.Fatalf("static %s: %d %q %v", route, status, body, e)
		}
	}
	for route, want := range map[string]string{"/": "/app/", "/admin": "/admin/"} {
		status, body, e := request(edgeURL, route, "GET")
		if e != nil || status != 308 || body != want {
			t.Fatalf("redirect %s: %d %q %v", route, status, body, e)
		}
	}
	for _, route := range []string{"/overview", "/assets/old.js"} {
		status, _, e := request(edgeURL, route, "GET")
		if e != nil || status != 404 {
			t.Fatalf("unprefixed admin %s: %d %v", route, status, e)
		}
	}
	status, _, e := request(edgeURL, "/admin/v2/admin/auth/me", "GET")
	if e != nil || status != 404 {
		t.Fatalf("admin prefix must not expose API: %d %v", status, e)
	}
	for _, route := range []string{"/rtc", "/rtc/rtc", "/internal/test", "/metrics", "/twirp/foo", "/v1/old"} {
		status, _, e := request(edgeURL, route, "GET")
		if e != nil || status != 404 {
			t.Fatalf("private route %s: %d %v", route, status, e)
		}
	}
	for route, want := range map[string]string{"/platform/v2/config/auth": "GET|/v2/config/auth|platform-proof||app.example.test|", "/platform/admin/auth/me": "GET|/platform/admin/auth/me|platform-proof||app.example.test|", "/v2/users/me": "GET|/v2/users/me|enterprise-proof||app.example.test|", "/livekit/rtc": "GET|/livekit/rtc|enterprise-proof||app.example.test|"} {
		status, body, e := request(edgeURL, route, "GET")
		if e != nil || status != 200 || !strings.HasPrefix(body, want) || strings.Contains(body, "1.2.3.4") {
			t.Fatalf("route %s: %d %q %v", route, status, body, e)
		}
	}
	status, body, e := request(edgeURL, "/nexachat-media/users/a%20b?X-Amz-Signature=test", "PUT")
	if e != nil || status != 200 || !strings.Contains(body, "PUT|/nexachat-media/users/a%20b?X-Amz-Signature=test|||") {
		t.Fatalf("S3 request rewritten: %d %q %v", status, body, e)
	}
	for _, route := range []string{"/v2/users/me", "/im", "/"} {
		status, _, e := request(backendURL, route, "GET")
		if e != nil || status != 403 {
			t.Fatalf("direct gateway bypass %s: %d %v", route, status, e)
		}
	}
}
