package deployment

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Production runtime smoke test, with a loopback-only TEST mapping. The agent's
// dedicated-host port/identity rules remain covered separately and are NOT
// disabled on the real executor to make this Windows fixture run.
func TestEnterpriseProductionLocalDocker(t *testing.T) {
	if os.Getenv("TENANCY_ENTERPRISE_PRODUCTION_TEST") != "local" {
		t.Skip("enterprise production-profile Docker test not enabled")
	}
	c := productionBundleFixture(t)
	c.ToolsImage = os.Getenv("TENANCY_ENTERPRISE_BUNDLE_IMAGE")
	raw, _, err := BuildEnterpriseBundle(c, Release{ID: "production-smoke", Sequence: 1, Runtime: "linux/amd64", SchemaVersion: 79})
	if err != nil {
		t.Fatal("production fixture/preloaded image invalid")
	}
	var b Bundle
	if json.Unmarshal(raw, &b) != nil {
		t.Fatal("fixture document")
	}
	binary, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	var random [6]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	tenant := "prod-fixture-" + hex.EncodeToString(random[:])
	endpoint := "unix:///var/run/docker.sock"
	if runtime.GOOS == "windows" {
		endpoint = "npipe:////./pipe/dockerDesktopLinuxEngine"
	}
	r := &ComposeRunner{Binary: binary, Endpoint: endpoint, BundleRoot: t.TempDir(), Project: "frogim-deploy-" + tenant, Server: "server-" + tenant, Tenant: tenant}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	for name, s := range b.Services {
		if _, err = r.inspectImage(t.Context(), s.Image); err != nil {
			t.Fatal("pinned image missing", name)
		}
		s.Ports = nil
		if name == "enterprise-gateway" {
			s.Ports = []Port{{HostIP: "127.0.0.1", Target: 8444, Published: strconv.Itoa(port), Protocol: "tcp"}}
		}
		b.Services[name] = s
	}
	encoded, _ := json.Marshal(b)
	var document map[string]any
	_ = json.Unmarshal(encoded, &document)
	owner := map[string]string{"io.frogim.server": r.Server, "io.frogim.tenant": r.Tenant}
	for _, section := range []string{"networks", "volumes"} {
		for _, v := range document[section].(map[string]any) {
			v.(map[string]any)["labels"] = owner
		}
	}
	for _, v := range document["services"].(map[string]any) {
		v.(map[string]any)["labels"] = owner
	}
	raw, _ = json.Marshal(document)
	raw = bytes.ReplaceAll(raw, []byte("$"), []byte("$$"))
	if r.resources(t.Context(), b) != nil {
		t.Fatal("fixture resource collision")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		items, e := r.containers(ctx)
		if e != nil || r.resources(ctx, b) != nil {
			t.Error("cleanup ownership unavailable")
			return
		}
		for _, item := range items {
			if !r.owns(item.Labels) {
				t.Error("foreign cleanup refused")
				return
			}
		}
		if _, e = r.compose(ctx, raw, "down", "--volumes", "--timeout", "2"); e != nil {
			t.Error("enterprise fixture cleanup failed")
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if _, err = r.compose(ctx, raw, "up", "--detach", "--no-build", "--pull", "never", "--wait", "--wait-timeout", "100"); err != nil {
		items, _ := r.containers(t.Context())
		for _, item := range items {
			t.Log(item.Labels["com.docker.compose.service"], item.Status, item.Health, item.ExitCode)
		}
		t.Fatal("enterprise production runtime did not become healthy")
	}
	items, err := r.containers(t.Context())
	if err != nil || len(items) != 9 {
		t.Fatal("incomplete enterprise")
	}
	for _, item := range items {
		if item.Status != "running" || item.Health != "healthy" || !r.owns(item.Labels) {
			t.Fatal("unhealthy enterprise component")
		}
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(c.PublicTLS.CA))
	dialer := net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	}}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	for path, status := range map[string]int{"/ready": 200, "/": 200, "/internal/tenant/readiness": 404, "/twirp/livekit.RoomService/ListRooms": 404, "/v2/users/me": 401} {
		res, e := client.Get(c.PublicURL() + path)
		if e != nil {
			t.Fatal("TLS gateway unavailable", e)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != status {
			t.Fatal("route status", path, res.StatusCode)
		}
		if !strings.Contains(res.Header.Get("Content-Security-Policy"), "blob:") {
			t.Fatal("admin blob CSP missing")
		}
	}
	t.Log("all nine production-profile enterprise services healthy; TLS, protected routes, anonymous denial and admin CSP verified; only disposable loopback fixtures used")
}
