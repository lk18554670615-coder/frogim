package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fixtureRunner struct {
	applies, verifies     int
	failApply, failVerify bool
}

func (f *fixtureRunner) Apply(context.Context, Release, Operation) error {
	f.applies++
	if f.failApply {
		return errors.New("private provider secret")
	}
	return nil
}
func (f *fixtureRunner) Verify(context.Context, Release, Operation) error {
	f.verifies++
	if f.failVerify {
		return errors.New("private health diagnostic")
	}
	return nil
}
func fixtureCatalog() map[string]Release {
	a := Release{ID: "release-one", Sequence: 1, Runtime: "linux/amd64", ComposeSHA256: strings.Repeat("a", 64), SchemaVersion: 79}
	b := Release{ID: "release-two", Sequence: 2, Runtime: "linux/amd64", ComposeSHA256: strings.Repeat("b", 64), SchemaVersion: 79, RollbackTo: []string{a.ID}}
	c := Release{ID: "release-three", Sequence: 3, Runtime: "linux/amd64", ComposeSHA256: strings.Repeat("c", 64), SchemaVersion: 80}
	return map[string]Release{a.ID: a, b.ID: b, c.ID: c}
}
func fixtureOperation(c map[string]Release, id, release, previous string, generation int64) Operation {
	return Operation{ID: id, ServerID: "server-a", TenantID: "a", HostFingerprint: strings.Repeat("a", 64), ReleaseID: release, ReleaseDigest: c[release].Digest(), ExpectedGeneration: generation, ExpectedReleaseID: previous, Action: "deploy"}
}
func fixtureExecutor(t *testing.T, dir string, c map[string]Release, runner ReleaseRunner) *Executor {
	t.Helper()
	x, e := OpenExecutor(dir, "server-a", "a", strings.Repeat("a", 64), "linux/amd64", c, runner)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(x.Close)
	return x
}
func TestExecutorDurabilityReplayAndSingleOwner(t *testing.T) {
	c := fixtureCatalog()
	dir := t.TempDir()
	runner := &fixtureRunner{}
	x := fixtureExecutor(t, dir, c, runner)
	if other, e := OpenExecutor(dir, "server-a", "a", strings.Repeat("a", 64), "linux/amd64", c, runner); e == nil {
		other.Close()
		t.Fatal("second process owner accepted")
	}
	o := fixtureOperation(c, "job-one", "release-one", "", 0)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := x.Submit(o); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, e := x.Once(t.Context()); e != nil {
		t.Fatal(e)
	}
	if runner.applies != 1 || runner.verifies != 1 {
		t.Fatal("duplicate execution")
	}
	if _, e := x.Submit(o); e != nil {
		t.Fatal(e)
	}
	if worked, e := x.Once(t.Context()); e != nil || worked {
		t.Fatal("completed replay executed")
	}
	x.Close()
	x = fixtureExecutor(t, dir, c, runner)
	s, r, e := x.Status(o.ID)
	if e != nil || s.Generation != 1 || r.State != "completed" {
		t.Fatal("journal not recovered")
	}
	changed := o
	changed.ReleaseID = "release-two"
	changed.ReleaseDigest = c[changed.ReleaseID].Digest()
	if _, e = x.Submit(changed); !errors.Is(e, ErrConflict) {
		t.Fatal("immutable operation changed")
	}
}
func TestExecutorFailureRetryRecoveryAndRollback(t *testing.T) {
	c := fixtureCatalog()
	dir := t.TempDir()
	runner := &fixtureRunner{failApply: true}
	x := fixtureExecutor(t, dir, c, runner)
	o := fixtureOperation(c, "job-one", "release-one", "", 0)
	if _, e := x.Submit(o); e != nil {
		t.Fatal(e)
	}
	if _, e := x.Once(t.Context()); !errors.Is(e, ErrUnconfirmed) {
		t.Fatal("apply failed open")
	}
	s, r, e := x.Status(o.ID)
	if e != nil || s.Generation != 0 || r.ErrorCode != "DEPLOYMENT_UNCONFIRMED" || r.State != "unconfirmed" {
		t.Fatal("unsafe failure result")
	}
	if _, e = x.Submit(fixtureOperation(c, "other", "release-two", "", 0)); !errors.Is(e, ErrConflict) {
		t.Fatal("unknown task replaced")
	}
	if _, e = x.Retry(o.ID, r.Attempts+1); !errors.Is(e, ErrConflict) {
		t.Fatal("stale retry accepted")
	}
	if _, e = x.Retry(o.ID, r.Attempts); e != nil {
		t.Fatal(e)
	}
	runner.failApply = false
	if _, e = x.Once(t.Context()); e != nil {
		t.Fatal(e)
	}
	second := fixtureOperation(c, "job-two", "release-two", "release-one", 1)
	if _, e = x.Submit(second); e != nil {
		t.Fatal(e)
	}
	if _, e = x.Once(t.Context()); e != nil {
		t.Fatal(e)
	}
	rollback := fixtureOperation(c, "rollback-one", "release-one", "release-two", 2)
	if _, e = x.Submit(rollback); !errors.Is(e, ErrConflict) {
		t.Fatal("deploy bypassed rollback permission")
	}
	rollback.Action = "rollback"
	if _, e = x.Submit(rollback); e != nil {
		t.Fatal(e)
	}
	if _, e = x.Once(t.Context()); e != nil {
		t.Fatal(e)
	}
	third := fixtureOperation(c, "upgrade-schema", "release-three", "release-one", 3)
	if _, e = x.Submit(third); e != nil {
		t.Fatal(e)
	}
	if _, e = x.Once(t.Context()); e != nil {
		t.Fatal(e)
	}
	rollback = fixtureOperation(c, "unsafe-downgrade", "release-one", "release-three", 4)
	rollback.Action = "rollback"
	if _, e = x.Submit(rollback); !errors.Is(e, ErrConflict) {
		t.Fatal("database downgrade accepted")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if strings.Contains(string(data), "private") {
		t.Fatal("remote diagnostic persisted")
	}
}
func TestExecutorInterruptedApplyOnlyVerifiesAndCorruptionFailsClosed(t *testing.T) {
	for _, healthy := range []bool{true, false} {
		t.Run(map[bool]string{true: "healthy", false: "unconfirmed"}[healthy], func(t *testing.T) {
			c := fixtureCatalog()
			dir := t.TempDir()
			runner := &fixtureRunner{failVerify: !healthy}
			x := fixtureExecutor(t, dir, c, runner)
			o := fixtureOperation(c, "interrupted", "release-one", "", 0)
			if _, e := x.Submit(o); e != nil {
				t.Fatal(e)
			}
			// Durable checkpoint at process death, before any completion write.
			x.mu.Lock()
			r := x.state.Receipts[o.ID]
			r.State = "applying"
			r.Attempts = 1
			x.state.Receipts[o.ID] = r
			e := x.saveLocked()
			x.mu.Unlock()
			if e != nil {
				t.Fatal(e)
			}
			x.Close()
			x = fixtureExecutor(t, dir, c, runner)
			_, e = x.Once(t.Context())
			if healthy && e != nil || !healthy && !errors.Is(e, ErrUnconfirmed) || runner.applies != 0 || runner.verifies != 1 {
				t.Fatal("restart blindly re-applied")
			}
			x.Close()
			if os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"version":1,"receipts":null}`), 0600) != nil {
				t.Fatal("fixture write")
			}
			if other, e := OpenExecutor(dir, "server-a", "a", strings.Repeat("a", 64), "linux/amd64", c, runner); e == nil {
				other.Close()
				t.Fatal("corrupt journal reset")
			}
		})
	}
}
func TestExecutorJournalWriteFailurePreventsExecution(t *testing.T) {
	c := fixtureCatalog()
	dir := t.TempDir()
	runner := &fixtureRunner{}
	x := fixtureExecutor(t, dir, c, runner)
	// Only remove this test's newly created empty journal, then replace it with
	// a directory to inject atomic rename failure. No user/runtime file touched.
	if os.Remove(filepath.Join(dir, "state.json")) != nil || os.Mkdir(filepath.Join(dir, "state.json"), 0700) != nil {
		t.Fatal("fault fixture failed")
	}
	if _, e := x.Submit(fixtureOperation(c, "job", "release-one", "", 0)); !errors.Is(e, ErrState) {
		t.Fatal("write failure ignored")
	}
	if _, e := x.Once(t.Context()); !errors.Is(e, ErrState) || runner.applies != 0 {
		t.Fatal("poisoned process executed")
	}
}
func fixtureBundle() Bundle {
	return Bundle{Services: map[string]Service{"probe": {Image: "alpine@sha256:" + strings.Repeat("a", 64), Platform: "linux/amd64", Command: []string{"sleep", "300"}, Networks: []string{"private"}, Healthcheck: &Healthcheck{Test: []string{"CMD", "true"}, Interval: "1s", Timeout: "1s", Retries: 3}}}, Networks: map[string]LocalNetwork{"private": {Internal: true}}}
}
func saveFixtureBundle(t *testing.T, root string, r Release, b Bundle) Release {
	t.Helper()
	dir := filepath.Join(root, r.ID)
	if os.MkdirAll(dir, 0700) != nil {
		t.Fatal("fixture directory failed")
	}
	data, _ := json.Marshal(b)
	sum := sha256.Sum256(data)
	r.ComposeSHA256 = hex.EncodeToString(sum[:])
	if os.WriteFile(filepath.Join(dir, "compose.json"), data, 0600) != nil {
		t.Fatal("fixture bundle failed")
	}
	return r
}
func TestReleaseCatalogAndBundleRejectMutableOrEscapingInputs(t *testing.T) {
	c := fixtureCatalog()
	data, _ := json.Marshal(catalogList(c))
	if _, e := ReadCatalog(strings.NewReader(string(data))); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{`[]`, `[null]`, string(data) + ` {}`, `[{"id":"release","command":"sh"}]`} {
		if _, e := ReadCatalog(strings.NewReader(raw)); e == nil {
			t.Fatal("invalid catalog accepted")
		}
	}
	dir := t.TempDir()
	r := saveFixtureBundle(t, dir, c["release-one"], fixtureBundle())
	if _, b, e := readBundle(dir, r.ID, r.ComposeSHA256); e != nil || validateBundle(b, r) != nil {
		t.Fatal("valid bundle rejected")
	}
	for _, id := range []string{"../release-one", "..", "/absolute", "release/other"} {
		if _, _, e := readBundle(dir, id, r.ComposeSHA256); e == nil {
			t.Fatal("escaping path accepted")
		}
	}
	if _, _, e := readBundle(dir, r.ID, strings.Repeat("0", 64)); e == nil {
		t.Fatal("wrong digest accepted")
	}
	for _, change := range []func(*Service){func(s *Service) { s.Image = "alpine:latest" }, func(s *Service) { s.Healthcheck = nil }, func(s *Service) { s.Ports = []Port{{Target: 80, Published: "80", HostIP: "0.0.0.0", Protocol: "tcp"}} }, func(s *Service) {
		s.Ports = []Port{{Target: 80, Published: "99999", HostIP: "127.0.0.1", Protocol: "tcp"}}
	}, func(s *Service) {
		s.Volumes = []Volume{{Type: "bind", Source: "/var/run/docker.sock", Target: "/socket"}}
	}} {
		b := fixtureBundle()
		s := b.Services["probe"]
		change(&s)
		b.Services["probe"] = s
		if validateBundle(b, r) == nil {
			t.Fatal("unsafe bundle accepted")
		}
	}
	// Unknown top-level/service fields, external resources and host namespace
	// escapes must be rejected even when the operator supplied a matching digest.
	for _, raw := range []string{`{"services":{},"include":"other.yml"}`, `{"services":{"probe":{"image":"x","privileged":true}}}`, `{"services":{},"volumes":{"db":{"external":true}}}`} {
		sum := sha256.Sum256([]byte(raw))
		if os.WriteFile(filepath.Join(dir, r.ID, "compose.json"), []byte(raw), 0600) != nil {
			t.Fatal("fixture write")
		}
		if _, _, e := readBundle(dir, r.ID, hex.EncodeToString(sum[:])); e == nil {
			t.Fatal("unknown compose field accepted")
		}
	}
}
