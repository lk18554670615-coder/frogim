package deployment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Explicit local Docker opt-in. Creates only a random frogim-deploy-fixture-*
// project with a preloaded, immutable Alpine image, no host mounts or ports.
// It does not deploy the real enterprise stack or modify existing services.
func TestComposeRunnerLocalDockerDeployUpgradeRollbackAndIsolation(t *testing.T) {
	if os.Getenv("TENANCY_DEPLOYMENT_DOCKER_TEST") != "local" {
		t.Skip("local Docker deployment test not explicitly enabled")
	}
	binary, e := exec.LookPath("docker")
	if e != nil {
		t.Fatal("local Docker CLI unavailable")
	}
	endpoint := "unix:///var/run/docker.sock"
	if runtime.GOOS == "windows" {
		endpoint = "npipe:////./pipe/dockerDesktopLinuxEngine"
	}
	newFixture := func() (*Executor, *ComposeRunner, map[string]Release) {
		t.Helper()
		var suffix [6]byte
		if _, e := rand.Read(suffix[:]); e != nil {
			t.Fatal(e)
		}
		tenant := "fixture-" + hex.EncodeToString(suffix[:])
		root := t.TempDir()
		b := fixtureBundle()
		s := b.Services["probe"]
		s.Image = "alpine@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce"
		s.Command = []string{"sh", "-c", "test -f /data/marker || printf retained >/data/marker; exec sleep 600"}
		s.Volumes = []Volume{{Type: "volume", Source: "data", Target: "/data"}}
		s.Healthcheck = &Healthcheck{Test: []string{"CMD", "test", "-f", "/data/marker"}, Interval: "1s", Timeout: "1s", Retries: 10}
		b.Services["probe"] = s
		b.Volumes = map[string]LocalVolume{"data": {}}
		one := saveFixtureBundle(t, root, Release{ID: "one", Sequence: 1, Runtime: "linux/amd64", SchemaVersion: 79}, b)
		s.Command = []string{"sh", "-c", "test -f /data/marker && exec sleep 600"}
		b.Services["probe"] = s
		two := saveFixtureBundle(t, root, Release{ID: "two", Sequence: 2, Runtime: "linux/amd64", SchemaVersion: 79, RollbackTo: []string{"one"}}, b)
		catalog := map[string]Release{one.ID: one, two.ID: two}
		r := &ComposeRunner{Binary: binary, Endpoint: endpoint, BundleRoot: root, Project: "frogim-deploy-" + tenant, Server: "server-" + tenant, Tenant: tenant, Catalog: catalog}
		info, e := r.command(t.Context(), nil, "info", "--format", "{{.OSType}}")
		if e != nil || strings.TrimSpace(string(info)) != "linux" {
			t.Fatal("requires local Linux Docker engine")
		}
		if _, e = r.inspectImage(t.Context(), s.Image); e != nil {
			t.Fatal("pinned fixture image must already be loaded; test never pulls")
		}
		x, e := OpenExecutor(t.TempDir(), r.Server, r.Tenant, strings.Repeat("a", 64), "linux/amd64", catalog, r)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			x.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			// Remove only this function's random, verified test project/resources.
			// This cleanup is test code, not an exposed executor capability.
			o := Operation{ID: "cleanup", ServerID: r.Server, TenantID: r.Tenant, ReleaseID: two.ID, ReleaseDigest: two.Digest()}
			raw, b, e := r.bundle(two, o)
			if e != nil {
				t.Error("cleanup bundle unavailable")
				return
			}
			containers, e := r.containers(ctx)
			if e != nil {
				t.Error("cleanup ownership unavailable")
				return
			}
			for _, c := range containers {
				if !r.owns(c.Labels) {
					t.Error("refusing foreign cleanup")
					return
				}
			}
			if r.resources(ctx, b) != nil {
				t.Error("refusing foreign resource cleanup")
				return
			}
			if _, e = r.compose(ctx, raw, "down", "--volumes", "--timeout", "2"); e != nil {
				t.Error("isolated fixture cleanup failed")
			}
		})
		return x, r, catalog
	}
	makeOp := func(r *ComposeRunner, c map[string]Release, id, release, prior string, g int64) Operation {
		return Operation{ID: id, ServerID: r.Server, TenantID: r.Tenant, HostFingerprint: strings.Repeat("a", 64), ReleaseID: release, ReleaseDigest: c[release].Digest(), ExpectedReleaseID: prior, ExpectedGeneration: g, Action: "deploy"}
	}
	deploy := func(x *Executor, o Operation) {
		t.Helper()
		if _, e := x.Submit(o); e != nil {
			t.Fatal(e)
		}
		if _, e := x.Once(t.Context()); e != nil {
			t.Fatal("local Docker deployment failed", e)
		}
	}
	other, otherRunner, otherCatalog := newFixture()
	deploy(other, makeOp(otherRunner, otherCatalog, "other", "one", "", 0))
	before, e := otherRunner.containers(t.Context())
	if e != nil || len(before) != 1 {
		t.Fatal("other fixture missing")
	}
	x, r, c := newFixture()
	// A crash after Compose create but before start leaves no State.Health.
	// Such owned partial state must stay inspectable for retry and cleanup.
	initial := makeOp(r, c, "first", "one", "", 0)
	raw, _, err := r.bundle(c["one"], initial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.compose(t.Context(), raw, "create", "--no-build", "--pull", "never"); err != nil {
		t.Fatal("partial fixture creation", err)
	}
	partial, err := r.containers(t.Context())
	if err != nil || len(partial) != 1 || partial[0].Status != "created" || partial[0].Health != "" {
		t.Fatal("unstarted container cannot be inspected", err)
	}
	deploy(x, makeOp(r, c, "first", "one", "", 0))
	deploy(x, makeOp(r, c, "second", "two", "one", 1))
	o := makeOp(r, c, "rollback", "one", "two", 2)
	o.Action = "rollback"
	deploy(x, o)
	containers, e := r.containers(t.Context())
	if e != nil || len(containers) != 1 {
		t.Fatal("target fixture missing")
	}
	marker, e := r.command(t.Context(), nil, "exec", containers[0].ID, "cat", "/data/marker")
	if e != nil || string(marker) != "retained" {
		t.Fatal("upgrade/rollback lost named-volume data")
	}
	after, e := otherRunner.containers(t.Context())
	if e != nil || len(after) != 1 || after[0].ID != before[0].ID || after[0].Status != "running" || after[0].Health != "healthy" {
		t.Fatal("deployment affected another project")
	}
	// Engine replay after confirmed deployment does not recreate containers.
	if _, e = x.Submit(o); e != nil {
		t.Fatal(e)
	}
	if worked, e := x.Once(t.Context()); e != nil || worked {
		t.Fatal("confirmed operation replayed")
	}
	final, e := r.containers(t.Context())
	if e != nil || final[0].ID != containers[0].ID {
		t.Fatal("replay recreated container")
	}
}
