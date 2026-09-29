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
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The real production binaries/configuration run in fresh local containers.
// Only TEST host port mappings are replaced with loopback; this deliberately
// does not attest independent physical hosts, public DNS, firewalls or suppliers.
func TestPlatformProductionLocalDocker(t *testing.T) {
	if os.Getenv("TENANCY_PLATFORM_BUNDLE_TEST") != "local" {
		t.Skip("platform production-profile Docker test not enabled")
	}
	c := platformBundleFixture(t)
	c.ToolsImage = os.Getenv("TENANCY_PLATFORM_BUNDLE_IMAGE")
	c.Peers = nil
	raw, _, err := BuildPlatformBundle(c, "platform-fixture")
	if err != nil {
		t.Fatal("preloaded pinned platform image required")
	}
	binary, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal("Docker unavailable")
	}
	var suffix [6]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	tenant := "platform-fixture-" + hex.EncodeToString(suffix[:])
	endpoint := "unix:///var/run/docker.sock"
	if runtime.GOOS == "windows" {
		endpoint = "npipe:////./pipe/dockerDesktopLinuxEngine"
	}
	r := &ComposeRunner{Binary: binary, Endpoint: endpoint, BundleRoot: t.TempDir(), Project: "frogim-deploy-" + tenant, Server: "server-" + tenant, Tenant: tenant}
	var b Bundle
	if json.Unmarshal(raw, &b) != nil {
		t.Fatal("fixture document")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	for name, s := range b.Services {
		if _, err = r.inspectImage(t.Context(), s.Image); err != nil {
			t.Fatal("preloaded image unavailable", name)
		}
		s.Ports = nil
		if name == "platform-gateway" {
			s.Ports = []Port{{HostIP: "127.0.0.1", Target: 8443, Published: strconv.Itoa(port), Protocol: "tcp"}}
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
	for _, s := range document["services"].(map[string]any) {
		s.(map[string]any)["labels"] = owner
	}
	raw, _ = json.Marshal(document)
	if r.resources(t.Context(), b) != nil {
		t.Fatal("fixture resource collision")
	}
	snapshot := func() string {
		t.Helper()
		out, e := r.command(t.Context(), nil, "ps", "--all", "--filter", "label=com.docker.compose.project=frogim-tenancy-local", "--format", "{{.ID}}")
		if e != nil {
			t.Fatal("default stack inspection")
		}
		ids := strings.Fields(string(out))
		sort.Strings(ids)
		return strings.Join(ids, ",")
	}
	before := snapshot()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		containers, e := r.containers(ctx)
		if e != nil || r.resources(ctx, b) != nil {
			t.Error("cleanup ownership not confirmed")
			return
		}
		for _, item := range containers {
			if !r.owns(item.Labels) {
				t.Error("foreign cleanup refused")
				return
			}
		}
		if _, e = r.compose(ctx, raw, "down", "--volumes", "--timeout", "2"); e != nil {
			t.Error("private test fixture cleanup failed")
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if _, err = r.compose(ctx, raw, "up", "--detach", "--no-build", "--pull", "never", "--wait", "--wait-timeout", "100"); err != nil {
		// Inspect states only; provider secrets and Compose contents stay private.
		items, _ := r.containers(t.Context())
		for _, item := range items {
			t.Log(item.Labels["com.docker.compose.service"], item.Status, item.Health, item.ExitCode)
		}
		t.Fatal("production runtime fixture did not become healthy")
	}
	items, err := r.containers(t.Context())
	if err != nil || len(items) != 4 {
		t.Fatal("platform fixture incomplete")
	}
	for _, item := range items {
		if !r.owns(item.Labels) || item.Status != "running" || item.Health != "healthy" {
			t.Fatal("unhealthy platform component")
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
	request := func(method, path, body string) (http.Header, []byte, int) {
		t.Helper()
		req, e := http.NewRequest(method, c.PublicURL+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res, e := client.Do(req)
		if e != nil {
			t.Fatal("gateway request failed", e)
		}
		defer res.Body.Close()
		data, e := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if e != nil {
			t.Fatal(e)
		}
		return res.Header, data, res.StatusCode
	}
	headers, body, status := request("GET", "/v2/config/auth", "")
	if status != 200 || !bytes.Contains(body, []byte(`"otpLoginEnabled":false`)) || !bytes.Contains(body, []byte(`"registrationEnabled":false`)) || headers.Get("Cache-Control") != "no-store" {
		t.Fatal("production auth did not remain closed without SMS")
	}
	for _, path := range []string{"/internal/platform/tickets/consume", "/metrics", "/twirp/livekit.RoomService/ListRooms"} {
		if _, _, code := request("GET", path, ""); code != 404 {
			t.Fatal("internal route exposed", path)
		}
	}
	_, body, status = request("GET", "/app/", "")
	if status != 200 || !bytes.Contains(body, []byte("flutter_bootstrap.js")) {
		t.Fatal("unified client not served")
	}
	if !strings.Contains(headers.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("missing production CSP")
	}
	_, _, status = request("POST", "/platform/admin/auth/login", `{"username":"operator","password":"synthetic-platform-operator-password"}`)
	if status != 200 {
		t.Fatal("platform operator bootstrap/login failed", status)
	}
	if snapshot() != before {
		t.Fatal("default runtime was changed")
	}
	t.Log("production platform+DB+Redis+gateway healthy; TLS verified; internal routes closed; OTP absent; admin login succeeds; default runtime unchanged")
}
