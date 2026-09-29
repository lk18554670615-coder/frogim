package platform

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/privatefile"
)

// All credentials are ephemeral, exclusively written, and never logged. These
// helpers have no access to the persistent default enterprise's configuration.
func deploymentStackPKI(t *testing.T) func(string) deployment.EnterpriseTLS {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal("test CA generation failed")
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "disposable deployment CA"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour)}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, key.Public(), key)
	if e != nil {
		t.Fatal("test CA signing failed")
	}
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return func(identity string) deployment.EnterpriseTLS {
		t.Helper()
		leafKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal("test key generation failed")
		}
		serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
		if e != nil {
			t.Fatal("test serial generation failed")
		}
		leaf := &x509.Certificate{SerialNumber: serial, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"host.docker.internal", "enterprise-api"}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		if identity != "" {
			u, _ := url.Parse(identity)
			leaf.URIs = []*url.URL{u}
		}
		der, e := x509.CreateCertificate(rand.Reader, leaf, ca, leafKey.Public(), key)
		if e != nil {
			t.Fatal("test certificate signing failed")
		}
		private, e := x509.MarshalPKCS8PrivateKey(leafKey)
		if e != nil {
			t.Fatal("test key encoding failed")
		}
		return deployment.EnterpriseTLS{CA: caPEM, Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))}
	}
}

func deploymentStackTLS(t *testing.T, c deployment.EnterpriseTLS) *tls.Config {
	t.Helper()
	cert, e := tls.X509KeyPair([]byte(c.Certificate), []byte(c.PrivateKey))
	if e != nil {
		t.Fatal("invalid test key pair")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(c.CA)) {
		t.Fatal("invalid test CA")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, RootCAs: pool, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert}
}

func deploymentStackPort(t *testing.T) int {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal("test loopback port unavailable")
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func deploymentStackPrivate(t *testing.T, path string, data []byte) {
	t.Helper()
	f, e := privatefile.Create(path)
	if e != nil {
		t.Fatal("private test file creation failed")
	}
	_, e = f.Write(data)
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		t.Fatal("private test file write failed")
	}
}

type deploymentStackDocker struct{ binary, id, server, agent string }

func (d deploymentStackDocker) command(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	endpoint := "unix:///var/run/docker.sock"
	if runtime.GOOS == "windows" {
		endpoint = "npipe:////./pipe/dockerDesktopLinuxEngine"
	}
	cmd := exec.CommandContext(ctx, d.binary, append([]string{"--host", endpoint}, args...)...)
	// Never inherit a remote Docker context/host or loader overrides for this
	// destructive disposable-fixture test. The endpoint is always local.
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "WINDIR", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "ProgramFiles", "ProgramData", "ProgramW6432"} {
		if v, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stderr = io.Discard // no environment, keys, or provider URLs in diagnostics
	cmd.WaitDelay = 2 * time.Second
	return cmd.Output()
}

func (d deploymentStackDocker) defaultSnapshot(t *testing.T) string {
	t.Helper()
	b, e := d.command(t.Context(), nil, "ps", "--all", "--filter", "label=com.docker.compose.project=frogim-tenancy-local", "--format", "{{.ID}}")
	if e != nil {
		t.Fatal("default stack snapshot unavailable")
	}
	ids := strings.Fields(string(b))
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// Deletion is test-only, restricted to exact inspected identities and dual
// owner labels. Never compose-down an uninspected name or remove a host path.
func (d deploymentStackDocker) cleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	if d.agent != "" {
		b, e := d.command(ctx, nil, "inspect", "--format", `{{index .Config.Labels "io.frogim.fixture"}}`, d.agent)
		if e != nil || strings.TrimSpace(string(b)) != d.id {
			t.Error("agent ownership unavailable; preserving fixture")
			return
		}
		if _, e = d.command(ctx, nil, "rm", "--force", d.agent); e != nil {
			t.Error("agent cleanup failed; preserving resources")
			return
		}
	}
	project := "frogim-deploy-" + d.id
	for _, kind := range []string{"container", "volume", "network"} {
		list := []string{kind, "ls", "--filter", "label=com.docker.compose.project=" + project, "--format", "{{.ID}}"}
		if kind == "container" {
			list = append(list, "--all")
		}
		if kind == "volume" {
			list[len(list)-1] = "{{.Name}}"
		}
		out, e := d.command(ctx, nil, list...)
		if e != nil {
			t.Error("fixture inventory unavailable", kind)
			return
		}
		for _, id := range strings.Fields(string(out)) {
			field := ".Labels"
			if kind == "container" {
				field = ".Config.Labels"
			}
			format := fmt.Sprintf(`{{index %s "io.frogim.tenant"}}|{{index %s "io.frogim.server"}}|{{index %s "com.docker.compose.project"}}`, field, field, field)
			labels, e := d.command(ctx, nil, kind, "inspect", "--format", format, id)
			if e != nil || strings.TrimSpace(string(labels)) != d.id+"|"+d.server+"|"+project {
				t.Error("foreign/unconfirmed fixture resource; refusing cleanup", kind)
				return
			}
			remove := []string{kind, "rm"}
			if kind == "container" {
				remove = append(remove, "--force")
			}
			remove = append(remove, id)
			if _, e = d.command(ctx, nil, remove...); e != nil {
				t.Error("fixture removal failed", kind)
				return
			}
		}
	}
}

func deploymentStackHTTP(t *testing.T, client *http.Client, method, address, token string, body any, expected int) map[string]any {
	t.Helper()
	raw, e := json.Marshal(body)
	if e != nil {
		t.Fatal("test request encoding failed")
	}
	r, e := http.NewRequestWithContext(t.Context(), method, address, bytes.NewReader(raw))
	if e != nil {
		t.Fatal("test request creation failed")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Client-Platform", "web")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	response, e := client.Do(r)
	if e != nil {
		t.Fatal("local fixture HTTP request unavailable")
	}
	defer response.Body.Close()
	var out map[string]any
	if e = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&out); e != nil {
		t.Fatal("non-JSON fixture response", response.StatusCode)
	}
	if response.StatusCode != expected {
		// Return a fixed code only; never print a login/registration body.
		code := ""
		if v, ok := out["error"].(map[string]any); ok {
			code, _ = v["code"].(string)
		}
		t.Fatal("fixture response", response.StatusCode, "expected", expected, code)
	}
	return out
}

func deploymentStackDecode(t *testing.T, input any, output any) {
	t.Helper()
	b, e := json.Marshal(input)
	if e != nil || json.Unmarshal(b, output) != nil {
		t.Fatal("fixture response decode failed")
	}
}

func deploymentStackStartAgent(t *testing.T, c deployment.EnterpriseConfig, cert deployment.EnterpriseTLS, catalog []deployment.Release, root, image string, port int) deploymentStackDocker {
	t.Helper()
	binary, e := exec.LookPath("docker")
	if e != nil {
		t.Fatal("Docker unavailable")
	}
	d := deploymentStackDocker{binary: binary, id: c.TenantID, server: c.ServerID}
	state := t.TempDir()
	archives := t.TempDir()
	var backupKey [32]byte
	if _, e = rand.Read(backupKey[:]); e != nil {
		t.Fatal("fixture backup key unavailable")
	}
	deploymentStackPrivate(t, filepath.Join(root, "backup.key"), backupKey[:])
	clear(backupKey[:])
	config := map[string]any{"stateDirectory": "/state", "bundleDirectory": "/input", "dockerBinary": "/usr/local/bin/docker", "dockerEndpoint": "unix:///var/run/docker.sock", "catalog": catalog, "backup": map[string]string{"directory": "/archives", "keyFile": "/input/backup.key"}}
	raw, _ := json.Marshal(config)
	deploymentStackPrivate(t, filepath.Join(root, "executor.json"), raw)
	for name, value := range map[string]string{"ca.pem": cert.CA, "agent.pem": cert.Certificate, "agent.key": cert.PrivateKey} {
		deploymentStackPrivate(t, filepath.Join(root, name), []byte(value))
	}
	var hostID [16]byte
	if _, e = rand.Read(hostID[:]); e != nil {
		t.Fatal("fixture host identity unavailable")
	}
	deploymentStackPrivate(t, filepath.Join(root, "host-id"), []byte(hex.EncodeToString(hostID[:])))
	args := []string{"create", "--pull", "never", "--name", "frogim-agent-" + c.TenantID, "--label", "io.frogim.fixture=" + c.TenantID, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/tmp", "--publish", fmt.Sprintf("127.0.0.1:%d:8450", port), "--mount", "type=bind,source=" + root + ",target=/input,readonly", "--mount", "type=bind,source=" + state + ",target=/state", "--mount", "type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock"}
	args = append(args, "--mount", "type=bind,source="+archives+",target=/archives")
	for _, env := range []string{"AGENT_ENV=development", "AGENT_SERVER_ID=" + c.ServerID, "AGENT_TENANT_ID=" + c.TenantID, "AGENT_TENANT_HTTP_URL=" + c.PublicURL(), "AGENT_ISOLATION_MODE=local_preview", "AGENT_HOST_ID_FILE=/input/host-id", "AGENT_CA_FILE=/input/ca.pem", "AGENT_CERT_FILE=/input/agent.pem", "AGENT_KEY_FILE=/input/agent.key", "AGENT_EXECUTOR_FILE=/input/executor.json"} {
		args = append(args, "--env", env)
	}
	args = append(args, image)
	result, e := d.command(t.Context(), nil, args...)
	d.agent = strings.TrimSpace(string(result))
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(d.agent) {
		t.Fatal("agent container creation failed; inspect fixture name manually")
	}
	t.Cleanup(func() { d.cleanup(t) })
	if e != nil {
		t.Fatal("agent container creation unconfirmed")
	}
	// Register ownership cleanup before starting: a failed mount/port/start must
	// not leave an untracked agent with host-control capability behind.
	if _, e = d.command(t.Context(), nil, "start", d.agent); e != nil {
		t.Fatal("agent container start failed")
	}
	return d
}
