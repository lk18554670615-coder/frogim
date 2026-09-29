package deployment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

// A real, fresh enterprise stack; no access to existing data volumes or CA keys.
// This is Docker-local isolation, NOT a physical-server or production rehearsal.
func TestEnterpriseBundleLocalDocker(t *testing.T) {
	if os.Getenv("TENANCY_ENTERPRISE_BUNDLE_TEST") != "local" {
		t.Skip("full local enterprise bundle test not explicitly enabled")
	}
	enterpriseBundleDocker(t, false)
}

func enterpriseBundleDocker(t *testing.T, shared bool) {
	image := os.Getenv("TENANCY_ENTERPRISE_BUNDLE_IMAGE")
	if !imageReference.MatchString(image) {
		t.Fatal("requires preloaded immutable enterprise bundle image")
	}
	binary, e := exec.LookPath("docker")
	if e != nil {
		t.Fatal("Docker unavailable")
	}
	c := bundleTestConfig(t)
	var suffix [6]byte
	if _, e = rand.Read(suffix[:]); e != nil {
		t.Fatal(e)
	}
	c.TenantID = "stack-" + hex.EncodeToString(suffix[:])
	c.ServerID = "server-" + c.TenantID
	if shared {
		c.TenantID = "default"
		c.SharedDatastores = &SharedDatastores{Database: "enterprise", RedisDB: 1}
	}
	c.ToolsImage = image
	issue := bundleTestPKI(t)
	c.ControlTLS = issue(tenancy.EnterpriseIdentity(c.TenantID))
	c.PublicTLS = issue("")
	platformCert := issue(tenancy.PlatformIdentity)
	availablePort := func() int {
		t.Helper()
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	c.Ports = EnterprisePorts{HTTP: availablePort(), Media: availablePort(), IM: availablePort(), Control: availablePort(), RTCTCP: availablePort(), RTCUDPFrom: availablePort()}
	if c.Validate() != nil {
		t.Fatal("conflicting ephemeral ports; no deployment performed")
	}
	root := t.TempDir()
	raw, one, e := BuildEnterpriseBundle(c, Release{ID: "one", Sequence: 1, Runtime: "linux/amd64", SchemaVersion: 79})
	if e != nil {
		t.Fatal(e)
	}
	var b Bundle
	if e = json.Unmarshal(raw, &b); e != nil {
		t.Fatal(e)
	}
	one, e = WriteEnterpriseRelease(root, c, one)
	if e != nil {
		t.Fatal("private release write failed")
	}
	two, e := WriteEnterpriseRelease(root, c, Release{ID: "two", Sequence: 2, Runtime: "linux/amd64", SchemaVersion: 79, RollbackTo: []string{"one"}})
	if e != nil {
		t.Fatal("private upgrade release write failed")
	}
	catalog := map[string]Release{"one": one, "two": two}
	endpoint := "unix:///var/run/docker.sock"
	if runtime.GOOS == "windows" {
		endpoint = "npipe:////./pipe/dockerDesktopLinuxEngine"
	}
	r := &ComposeRunner{Binary: binary, Endpoint: endpoint, BundleRoot: root, Project: "frogim-deploy-" + c.TenantID, Server: c.ServerID, Tenant: c.TenantID, Catalog: catalog}
	if shared {
		startSharedFixture(t, r, c)
	}
	for _, s := range b.Services {
		if _, e = r.inspectImage(t.Context(), s.Image); e != nil {
			t.Fatal("missing pinned dependency; never pulls", s.Image)
		}
	}
	stateDirectory := t.TempDir()
	x, e := OpenExecutor(stateDirectory, r.Server, r.Tenant, strings.Repeat("c", 64), "linux/amd64", catalog, r)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		x.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		containers, e := r.containers(ctx)
		if e != nil {
			t.Error("cleanup state unavailable")
			return
		}
		for _, item := range containers {
			if !r.owns(item.Labels) {
				t.Error("refusing foreign container cleanup")
				return
			}
		}
		if r.resources(ctx, b) != nil {
			t.Error("refusing foreign resource cleanup")
			return
		}
		o := Operation{ID: "cleanup", ServerID: r.Server, TenantID: r.Tenant, ReleaseID: one.ID, ReleaseDigest: one.Digest()}
		raw, _, e := r.bundle(one, o)
		if e != nil {
			t.Error("cleanup artifact unavailable")
			return
		}
		if _, e = r.compose(ctx, raw, "down", "--volumes", "--timeout", "2"); e != nil {
			t.Error("isolated enterprise fixture cleanup failed")
		}
	})
	// Real mTLS HTTP agent protocol, using the host CLI against the explicitly
	// selected local Linux engine. This is not a dedicated Linux host attestation.
	agent := &Agent{report: Inspection{Protocol: Protocol, ServerID: r.Server, TenantID: r.Tenant, HTTPBaseURL: c.PublicURL(), HostFingerprint: strings.Repeat("c", 64), IsolationMode: "local_preview", Runtime: "linux/amd64", Capabilities: []string{"inspect", "deploy"}}, executor: x}
	agentServer := httptest.NewUnstartedServer(agent.Handler())
	agentServer.TLS = bundleClientTLS(t, issue(AgentIdentity(r.Server)))
	agentServer.StartTLS()
	t.Cleanup(agentServer.Close)
	agentRPC, e := tenancy.NewRPC(agentServer.URL, bundleClientTLS(t, platformCert), AgentIdentity(r.Server))
	if e != nil {
		t.Fatal(e)
	}
	command := func(action string, o Operation) ControlReport {
		t.Helper()
		nonce, _ := tenancy.Secret()
		var report ControlReport
		if e := agentRPC.Call(t.Context(), "/internal/agent/deployment/"+action, ControlRequest{Nonce: nonce, Operation: o}, &report); e != nil {
			t.Fatal("agent control request failed", e)
		}
		if report.Nonce != nonce || report.Status.TenantID != c.TenantID || report.Receipt.Operation != o {
			t.Fatal("agent response identity mismatch")
		}
		return report
	}
	defaultSnapshot := func() string {
		t.Helper()
		data, e := r.command(t.Context(), nil, "ps", "--all", "--filter", "label=com.docker.compose.project=frogim-tenancy-local", "--format", "{{.ID}}")
		if e != nil {
			t.Fatal(e)
		}
		lines := strings.Fields(string(data))
		sort.Strings(lines)
		return strings.Join(lines, ",")
	}
	before := defaultSnapshot()
	deploy := func(id, target, prior, action string, generation int64) {
		t.Helper()
		o := Operation{ID: id, ServerID: r.Server, TenantID: r.Tenant, HostFingerprint: strings.Repeat("c", 64), ReleaseID: target, ReleaseDigest: catalog[target].Digest(), ExpectedGeneration: generation, ExpectedReleaseID: prior, Action: action}
		command("submit", o)
		if _, e := x.Once(t.Context()); e != nil {
			items, _ := r.containers(t.Context())
			for _, item := range items {
				t.Log("runtime state", item.Labels["com.docker.compose.service"], item.Status, item.Health)
				engineError, _ := r.command(t.Context(), nil, "inspect", "--format", "{{.State.Error}}", item.ID)
				// Never print arbitrary daemon error text (it can include private
				// paths/arguments). Fixed categories still distinguish a host port
				// failure from application readiness on future fixture failures.
				for _, category := range []string{"port is already allocated", "ports are not available", "address already in use", "failed to bind", "permission denied", "no such file or directory", "executable file not found", "failed to create task", "error while mounting"} {
					if strings.Contains(strings.ToLower(string(engineError)), category) {
						t.Log("engine failure category", item.Labels["com.docker.compose.service"], category)
					}
				}
				if item.Health != "healthy" {
					logs, _ := exec.CommandContext(t.Context(), r.Binary, "--host", r.Endpoint, "logs", "--tail", "5", item.ID).CombinedOutput()
					// Child stderr is intentionally discarded by the production
					// runner. Inspect only bounded stdout with secret-like strings
					// masked; never dump container environment or key material.
					safe := regexp.MustCompile(`[A-Za-z0-9/_+.-]{32,}={0,2}`).ReplaceAllString(string(logs), "[redacted]")
					if !strings.Contains(safe, "PRIVATE KEY") {
						t.Log("bounded runtime diagnostic", safe)
					}
				}
			}
			t.Fatal("enterprise compose operation failed", e)
		}
		if report := command("status", o); report.Receipt.State != "completed" || report.Status.Generation != generation+1 {
			t.Fatal("agent completion was not durable")
		}
		command("submit", o)
		if worked, e := x.Once(t.Context()); e != nil || worked {
			t.Fatal("confirmed deployment replayed")
		}
	}
	deploy("first", "one", "", "deploy", 0)
	control, e := tenancy.NewRPC(fmt.Sprintf("https://127.0.0.1:%d", c.Ports.Control), bundleClientTLS(t, platformCert), tenancy.EnterpriseIdentity(c.TenantID))
	if e != nil {
		t.Fatal(e)
	}
	checkReady := func() {
		t.Helper()
		nonce, _ := tenancy.Secret()
		var ready tenancy.Readiness
		if e := control.Call(t.Context(), "/internal/tenancy/readiness", map[string]string{"nonce": nonce}, &ready); e != nil {
			t.Fatal("real control readiness unavailable", e)
		}
		if !ready.Valid(nonce, c.TenantID, c.PublicURL()) || ready.SchemaVersion != 79 || ready.Realm == nil {
			t.Fatal("real dependency checks failed", ready.Checks, ready.SchemaVersion)
		}
	}
	checkReady()
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: bundleClientTLS(t, c.PublicTLS)}}
	t.Cleanup(client.CloseIdleConnections)
	for path, status := range map[string]int{"/": 200, "/ready": 200, "/internal/tenancy/readiness": 404, "/platform/admin/tenants": 404, "/twirp/livekit.RoomService/ListRooms": 404} {
		response, e := client.Get(c.PublicURL() + path)
		if e != nil {
			t.Fatal("public gateway unavailable")
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != status {
			t.Fatal("gateway route", path, response.StatusCode)
		}
	}
	// A synthetic identity exercises a real DB migration/provision transaction;
	// it is not a production account and never logs in or reaches push providers.
	identity := tenancy.Identity{AccountID: "account-fixture", TenantID: c.TenantID, LocalUserID: "local-fixture", AssignmentVersion: 1}
	operation := map[string]any{"operationId": "provision-fixture", "identity": identity, "input": map[string]any{"phone": "19900000991", "name": "Deployment fixture", "gender": "unspecified", "method": "admin", "passwordRuneCount": 32}}
	var ack map[string]any
	if e = control.Call(t.Context(), "/internal/tenancy/identities/prepare", operation, &ack); e != nil {
		t.Fatal("real identity provisioning", e)
	}
	inspectIdentity := func() {
		t.Helper()
		if shared {
			pg, err := r.sharedStore(t.Context(), "shared-postgres")
			items, se := r.containers(t.Context())
			var current Release
			for _, item := range items {
				if item.Labels["com.docker.compose.service"] == "enterprise-api" {
					current = r.Catalog[item.Labels["io.frogim.release"]]
				}
			}
			_, currentBundle, be := readBundle(r.BundleRoot, current.ID, current.ComposeSHA256)
			if err != nil || se != nil || be != nil {
				t.Fatal("shared identity target unavailable")
			}
			out, err := r.command(t.Context(), nil, "exec", pg, "psql", "-U", "enterprise", "-d", sharedDatabase(currentBundle), "-At", "-c", `SELECT count(*) FROM im_users WHERE id='local-fixture'`)
			if err != nil || strings.TrimSpace(string(out)) != "1" {
				t.Fatal("shared identity missing")
			}
			return
		}
		items, e := r.containers(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range items {
			if item.Labels["com.docker.compose.service"] == "enterprise-db" {
				out, e := r.command(t.Context(), nil, "exec", item.ID, "psql", "-U", "enterprise", "-d", "enterprise", "-At", "-c", `SELECT count(*) FROM im_users WHERE id='local-fixture'`)
				if e != nil || strings.TrimSpace(string(out)) != "1" {
					t.Fatal("enterprise identity data missing after deployment")
				}
				return
			}
		}
		t.Fatal("enterprise database missing")
	}
	inspectIdentity()
	// Agent restart restores the original journal rather than adopting a new
	// generation or issuing a second deployment against already running data.
	agentServer.Close()
	x.Close()
	x, e = OpenExecutor(stateDirectory, r.Server, r.Tenant, strings.Repeat("c", 64), "linux/amd64", catalog, r)
	if e != nil {
		t.Fatal("agent journal recovery failed")
	}
	agent.executor = x
	agentServer = httptest.NewUnstartedServer(agent.Handler())
	agentServer.TLS = bundleClientTLS(t, issue(AgentIdentity(r.Server)))
	agentServer.StartTLS()
	t.Cleanup(agentServer.Close)
	agentRPC, e = tenancy.NewRPC(agentServer.URL, bundleClientTLS(t, platformCert), AgentIdentity(r.Server))
	if e != nil {
		t.Fatal(e)
	}
	deploy("second", "two", "one", "deploy", 1)
	checkReady()
	inspectIdentity()
	deploy("rollback", "one", "two", "rollback", 2)
	checkReady()
	inspectIdentity()
	if defaultSnapshot() != before {
		t.Fatal("deployment recreated pre-existing default enterprise")
	}
	// Wrong mTLS identity from the same CA is rejected, not trusted by hostname.
	wrong, e := tenancy.NewRPC(fmt.Sprintf("https://127.0.0.1:%d", c.Ports.Control), bundleClientTLS(t, issue(tenancy.EnterpriseIdentity("other"))), tenancy.EnterpriseIdentity(c.TenantID))
	if e != nil {
		t.Fatal(e)
	}
	nonce, _ := tenancy.Secret()
	if e = wrong.Call(t.Context(), "/internal/tenancy/readiness", map[string]string{"nonce": nonce}, new(tenancy.Readiness)); e == nil {
		t.Fatal("another enterprise became platform")
	}
	wrongAgent, e := tenancy.NewRPC(agentServer.URL, bundleClientTLS(t, issue(tenancy.EnterpriseIdentity("other"))), AgentIdentity(r.Server))
	if e != nil {
		t.Fatal(e)
	}
	if e = wrongAgent.Call(t.Context(), "/internal/agent/deployment/status", ControlRequest{Nonce: nonce}, new(ControlReport)); e == nil {
		t.Fatal("enterprise became deployment dispatcher")
	}
	if os.Getenv("TENANCY_COLD_BACKUP_TEST") == "local" {
		if !t.Run("cold-backup-restore", func(t *testing.T) { coldBackupDrill(t, r, x, c, control, one, inspectIdentity, checkReady) }) {
			return
		}
		if defaultSnapshot() != before {
			t.Fatal("restore affected the default project")
		}
	}
	t.Log("nine services healthy; real PostgreSQL/Redis/IM/MinIO/LiveKit checks, identity persistence, upgrade, rollback, replay, agent journal restart, mTLS agent/business identity rejection and default-project preservation passed")
}
