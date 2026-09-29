package deployment

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/tenancy"
)

type backupFixtureIO struct {
	fail             string
	calls            map[string]int
	paths            []string
	entered, proceed chan struct{}
}

func TestBackupHelperCapabilityNormalization(t *testing.T) {
	for _, values := range [][]string{{"DAC_OVERRIDE"}, {"CAP_DAC_OVERRIDE"}} {
		if !singleCapability(values, "DAC_OVERRIDE") {
			t.Fatal("Docker equivalent rejected")
		}
	}
	for _, values := range [][]string{nil, {}, {"CAP_SYS_ADMIN"}, {"CAP_DAC_OVERRIDE", "CAP_SYS_ADMIN"}, {"DAC_OVERRIDE", "CAP_DAC_OVERRIDE"}, {"cap_dac_override"}} {
		if singleCapability(values, "DAC_OVERRIDE") {
			t.Fatal("broader capability accepted")
		}
	}
}

func (f *backupFixtureIO) call(phase string) error {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[phase]++
	if f.fail == phase {
		return errors.New("private database/provider error")
	}
	return nil
}
func (f *backupFixtureIO) Prepare(ctx context.Context, _ backupPlan, _ []byte) (backupSource, error) {
	if f.entered != nil {
		close(f.entered)
		select {
		case <-f.proceed:
		case <-ctx.Done():
			return backupSource{}, ctx.Err()
		}
	}
	s := backupSource{Containers: map[string]string{}, Volumes: map[string]string{}}
	for i := range 9 {
		s.Containers[fmt.Sprintf("service-%d", i)] = strings.Repeat("a", 64)
	}
	for i := range 6 {
		s.Volumes[fmt.Sprintf("volume-%d", i)] = strings.Repeat("b", 64)
	}
	return s, f.call("queued")
}
func (f *backupFixtureIO) Quiesce(context.Context, backupPlan, backupSource) error {
	return f.call("quiescing")
}
func (f *backupFixtureIO) Capture(_ context.Context, _ backupPlan, _ backupSource, path string, _ []byte) (ArchiveProof, error) {
	f.paths = append(f.paths, path)
	return ArchiveProof{strings.Repeat("c", 64), 321, 8}, f.call("snapshotting")
}
func (f *backupFixtureIO) Restore(context.Context, backupPlan, backupSource) error {
	return f.call("restoring_services")
}
func (f *backupFixtureIO) Verify(context.Context, backupPlan, backupSource) error {
	return f.call("verifying")
}

type backupFixture struct {
	x         *Executor
	c         map[string]Release
	dir, root string
	key       []byte
	o         BackupOperation
	f         *backupFixtureIO
}

func newBackupFixture(t *testing.T) backupFixture {
	t.Helper()
	c := fixtureCatalog()
	for id, r := range c {
		r.TenantID = "a"
		r.ServerID = "server-a"
		c[id] = r
	}
	dir, root := t.TempDir(), t.TempDir()
	x := fixtureExecutor(t, dir, c, &fixtureRunner{})
	op := fixtureOperation(c, "deploy-one", "release-one", "", 0)
	if _, e := x.Submit(op); e != nil {
		t.Fatal(e)
	}
	if _, e := x.Once(t.Context()); e != nil {
		t.Fatal(e)
	}
	key := bytes.Repeat([]byte{33}, 32)
	f := &backupFixtureIO{}
	if e := x.configureBackupIO(root, key, f); e != nil {
		t.Fatal(e)
	}
	o := BackupOperation{ID: "backup-one", HostFingerprint: op.HostFingerprint, Binding: backup.Binding{TenantID: "a", ServerID: "server-a", ReleaseID: op.ReleaseID, ReleaseDigest: op.ReleaseDigest, Generation: 1, AccessVersion: 2, SchemaVersion: 79}}
	return backupFixture{x, c, dir, root, key, o, f}
}
func (b *backupFixture) restart(t *testing.T) {
	t.Helper()
	b.x.Close()
	b.x = fixtureExecutor(t, b.dir, b.c, &fixtureRunner{})
	if e := b.x.configureBackupIO(b.root, b.key, b.f); e != nil {
		t.Fatal(e)
	}
}
func (b *backupFixture) receipt(t *testing.T) BackupReceipt {
	t.Helper()
	_, r, e := b.x.BackupStatus(b.o.ID)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestBackupDurablePhasesAndExclusion(t *testing.T) {
	b := newBackupFixture(t)
	if _, e := b.x.SubmitBackup(b.o); e != nil {
		t.Fatal(e)
	}
	for _, phase := range []string{"queued", "quiescing", "snapshotting", "restoring_services", "verifying"} {
		r := b.receipt(t)
		if r.Phase != phase {
			t.Fatal(r)
		}
		if _, e := b.x.Submit(fixtureOperation(b.c, "other", "release-two", "release-one", 1)); !errors.Is(e, ErrConflict) {
			t.Fatal("backup did not exclude deployment")
		}
		other := b.o
		other.ID = "backup-two"
		if _, e := b.x.SubmitBackup(other); !errors.Is(e, ErrConflict) {
			t.Fatal("concurrent backup accepted")
		}
		// Simulate a killed process AFTER its running intent reached disk. Each
		// phase is restartable; capture's IO implementation authenticates lost ACKs.
		record := b.x.state.Backups[b.o.ID]
		record.State = "running"
		b.x.state.Backups[b.o.ID] = record
		if e := b.x.saveLocked(); e != nil {
			t.Fatal(e)
		}
		b.restart(t)
		if worked, e := b.x.BackupOnce(t.Context()); e != nil || !worked {
			t.Fatal(phase, e)
		}
	}
	r := b.receipt(t)
	if r.State != "completed" || r.Phase != "finished" || r.Attempt != 1 || !r.Archive.valid() {
		t.Fatal(r)
	}
	status, _, _ := b.x.Status("")
	if status.ActiveBackupID != "" || status.Generation != 1 {
		t.Fatal(status)
	}
	for range 3 {
		if _, e := b.x.SubmitBackup(b.o); e != nil {
			t.Fatal(e)
		}
		if worked, e := b.x.BackupOnce(t.Context()); e != nil || worked {
			t.Fatal("duplicate executed")
		}
	}
	if b.f.calls["snapshotting"] != 1 || b.f.calls["restoring_services"] != 1 {
		t.Fatal(b.f.calls)
	}
	if _, e := b.x.RetryBackup(b.o.ID, r.Revision); !errors.Is(e, ErrConflict) {
		t.Fatal("completed retried")
	}
	if _, e := b.x.Submit(fixtureOperation(b.c, "next", "release-two", "release-one", 1)); e != nil {
		t.Fatal(e)
	}
	other := b.o
	other.ID = "after-deploy"
	if _, e := b.x.SubmitBackup(other); !errors.Is(e, ErrConflict) {
		t.Fatal("deployment did not exclude backup")
	}
}
func TestBackupFailureRestoresBeforeClearingLock(t *testing.T) {
	for _, phase := range []string{"quiescing", "snapshotting"} {
		t.Run(phase, func(t *testing.T) {
			b := newBackupFixture(t)
			b.f.fail = phase
			if _, e := b.x.SubmitBackup(b.o); e != nil {
				t.Fatal(e)
			}
			for b.receipt(t).Phase != phase {
				if _, e := b.x.BackupOnce(t.Context()); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := b.x.BackupOnce(t.Context()); !errors.Is(e, ErrUnconfirmed) {
				t.Fatal("missing failure")
			}
			r := b.receipt(t)
			if r.Phase != "restoring_services" || r.State != "pending" || r.Archive != (ArchiveProof{}) {
				t.Fatal(r)
			}
			// Restoration itself fails. Keep the original archive error, require
			// explicit retry, and never re-run capture on restoration retry.
			b.f.fail = "restoring_services"
			if _, e := b.x.BackupOnce(t.Context()); e == nil {
				t.Fatal("restore failure lost")
			}
			r = b.receipt(t)
			if r.State != "unconfirmed" || r.RecoveryErrorCode != "BACKUP_SERVICE_RECOVERY_UNCONFIRMED" {
				t.Fatal(r)
			}
			b.restart(t)
			if worked, _ := b.x.BackupOnce(t.Context()); worked {
				t.Fatal("silent retry")
			}
			if _, e := b.x.RetryBackup(b.o.ID, r.Revision-1); e == nil {
				t.Fatal("stale revision accepted")
			}
			if _, e := b.x.RetryBackup(b.o.ID, r.Revision); e != nil {
				t.Fatal(e)
			}
			b.f.fail = ""
			for range 2 {
				if _, e := b.x.BackupOnce(t.Context()); e != nil {
					t.Fatal(e)
				}
			}
			r = b.receipt(t)
			if r.State != "failed" || r.ErrorCode == "" || r.RecoveryErrorCode != "" {
				t.Fatal(r)
			}
			if _, e := b.x.RetryBackup(b.o.ID, r.Revision); e != nil {
				t.Fatal(e)
			}
			if _, e := b.x.RetryBackup(b.o.ID, r.Revision); e == nil {
				t.Fatal("duplicate retry changed attempt")
			}
			for range 5 {
				if _, e := b.x.BackupOnce(t.Context()); e != nil {
					t.Fatal(e)
				}
			}
			r = b.receipt(t)
			if r.State != "completed" || r.Attempt != 2 {
				t.Fatal(r)
			}
			if !strings.HasSuffix(b.f.paths[len(b.f.paths)-1], "backup-one-attempt-002") {
				t.Fatal("failed path reused")
			}
		})
	}
}
func TestBackupUnconfirmedPreflightAndVerification(t *testing.T) {
	for _, phase := range []string{"queued", "verifying"} {
		t.Run(phase, func(t *testing.T) {
			b := newBackupFixture(t)
			b.f.fail = phase
			_, _ = b.x.SubmitBackup(b.o)
			for b.receipt(t).Phase != phase {
				if _, e := b.x.BackupOnce(t.Context()); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := b.x.BackupOnce(t.Context()); e == nil {
				t.Fatal("missing failure")
			}
			r := b.receipt(t)
			if r.State != "unconfirmed" || r.RecoveryErrorCode == "" {
				t.Fatal(r)
			}
			b.restart(t)
			_, e := b.x.RetryBackup(b.o.ID, r.Revision)
			if e != nil {
				t.Fatal(e)
			}
			b.f.fail = ""
			for b.receipt(t).State != "completed" {
				if _, e := b.x.BackupOnce(t.Context()); e != nil {
					t.Fatal(e)
				}
			}
			if b.f.calls["snapshotting"] != 1 {
				t.Fatal("successful archive re-captured")
			}
		})
	}
}
func TestBackupStatusDoesNotBlockAndJournalWriteFencesIO(t *testing.T) {
	b := newBackupFixture(t)
	b.f.entered = make(chan struct{})
	b.f.proceed = make(chan struct{})
	_, _ = b.x.SubmitBackup(b.o)
	done := make(chan error, 1)
	go func() { _, e := b.x.BackupOnce(t.Context()); done <- e }()
	<-b.f.entered
	status := make(chan ExecutorStatus, 1)
	go func() { s, _, _ := b.x.BackupStatus(b.o.ID); status <- s }()
	select {
	case s := <-status:
		if s.ActiveBackupID != b.o.ID {
			t.Fatal(s)
		}
	case <-time.After(time.Second):
		close(b.f.proceed)
		t.Fatal("status blocked behind IO")
	}
	if worked, e := b.x.BackupOnce(t.Context()); e != nil || worked {
		t.Fatal("parallel execution")
	}
	close(b.f.proceed)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	// Simulated durable write failure must occur before the next IO side effect.
	b.x.root.Close()
	if _, e := b.x.BackupOnce(t.Context()); !errors.Is(e, ErrState) {
		t.Fatal("failed journal ignored")
	}
	if b.f.calls["quiescing"] != 0 {
		t.Fatal("stopped without durable intent")
	}
}
func TestBackupConfigurationAndInvalidOperation(t *testing.T) {
	b := newBackupFixture(t)
	b.x.Close()
	b.x = fixtureExecutor(t, b.dir, b.c, &fixtureRunner{})
	if _, e := b.x.SubmitBackup(b.o); !errors.Is(e, ErrState) {
		t.Fatal("unconfigured backup enabled")
	}
	if e := b.x.configureBackupIO(t.TempDir(), b.key, b.f); e == nil {
		t.Fatal("root changed silently")
	}
	if e := b.x.configureBackupIO(b.root, bytes.Repeat([]byte{44}, 32), b.f); e == nil {
		t.Fatal("key changed silently")
	}
	if e := b.x.configureBackupIO(b.root, b.key, b.f); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*BackupOperation){func(o *BackupOperation) { o.Binding.TenantID = "b" }, func(o *BackupOperation) { o.Binding.AccessVersion = 0 }, func(o *BackupOperation) { o.Binding.Generation = 2 }, func(o *BackupOperation) { o.HostFingerprint = strings.Repeat("b", 64) }, func(o *BackupOperation) { o.ID = "../x" }, func(o *BackupOperation) { o.Binding.ReleaseDigest = strings.Repeat("c", 64) }} {
		o := b.o
		change(&o)
		if _, e := b.x.SubmitBackup(o); e == nil {
			t.Fatal("bad operation accepted")
		}
	}
	_, _ = b.x.SubmitBackup(b.o)
	changed := b.o
	changed.Binding.AccessVersion++
	if _, e := b.x.SubmitBackup(changed); e == nil {
		t.Fatal("replay modified binding")
	}
}

func TestBackupCancelOnlyBeforeDurableStopIntent(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			b := newBackupFixture(t)
			_, _ = b.x.SubmitBackup(b.o)
			if failed {
				b.f.fail = "queued"
				_, _ = b.x.BackupOnce(t.Context())
			}
			r := b.receipt(t)
			if _, e := b.x.CancelBackup(b.o.ID, r.Revision-1); e == nil {
				t.Fatal("stale cancel accepted")
			}
			if _, e := b.x.CancelBackup(b.o.ID, r.Revision); e != nil {
				t.Fatal(e)
			}
			if _, e := b.x.CancelBackup(b.o.ID, r.Revision); e != nil {
				t.Fatal("lost cancel ACK not idempotent")
			}
			b.restart(t)
			r = b.receipt(t)
			if r.State != "cancelled" {
				t.Fatal(r)
			}
			if _, e := b.x.RetryBackup(b.o.ID, r.Revision); e == nil {
				t.Fatal("cancelled resurrected")
			}
			b.o.ID = "second"
			b.f.fail = ""
			_, _ = b.x.SubmitBackup(b.o)
			_, _ = b.x.BackupOnce(t.Context())
			r = b.receipt(t)
			if _, e := b.x.CancelBackup(b.o.ID, r.Revision); e == nil {
				t.Fatal("stop intent cancelled without restoration")
			}
		})
	}
}
func TestBackupJournalMigrationAndStrictness(t *testing.T) {
	for _, kind := range []string{"legacy", "missing-backups", "active-without-record", "wrong-version"} {
		t.Run(kind, func(t *testing.T) {
			c := fixtureCatalog()
			dir := t.TempDir()
			x := fixtureExecutor(t, dir, c, &fixtureRunner{})
			x.Close()
			path := filepath.Join(dir, "state.json")
			raw, _ := os.ReadFile(path)
			var state map[string]any
			_ = json.Unmarshal(raw, &state)
			switch kind {
			case "legacy":
				state["version"] = 1
				delete(state, "backups")
			case "missing-backups":
				delete(state, "backups")
			case "active-without-record":
				state["activeBackupId"] = "unknown"
			case "wrong-version":
				state["version"] = 999
			}
			raw, _ = json.Marshal(state)
			if os.WriteFile(path, raw, 0600) != nil {
				t.Fatal("fixture")
			}
			y, e := OpenExecutor(dir, "server-a", "a", strings.Repeat("a", 64), "linux/amd64", c, &fixtureRunner{})
			if kind == "legacy" {
				if e != nil {
					t.Fatal(e)
				}
				y.Close()
				raw, _ = os.ReadFile(path)
				_ = json.Unmarshal(raw, &state)
				if state["version"] != float64(2) {
					t.Fatal("not migrated")
				}
			} else if e == nil {
				y.Close()
				t.Fatal("invalid journal accepted")
			}
		})
	}
}
func TestBackupArchiveProofAndPartialAttempt(t *testing.T) {
	b := newBackupFixture(t)
	path := filepath.Join(t.TempDir(), "set")
	w, e := backup.Create(path, b.o.Binding, b.key)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	for _, name := range []string{"compose", "release", "database", "im", "im-logs", "media", "plugins", "redis"} {
		if e = w.Add(name, func(dst io.Writer) error { _, e := io.WriteString(dst, "fixture"); return e }); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = inspectArchive(path, b.o.Binding, b.key); e == nil {
		t.Fatal("partial accepted")
	}
	if w.Finalize() != nil {
		t.Fatal("finalize")
	}
	p, e := inspectArchive(path, b.o.Binding, b.key)
	if e != nil || !p.valid() {
		t.Fatal(p, e)
	}
	var total int64
	entries, _ := os.ReadDir(path)
	for _, v := range entries {
		info, _ := v.Info()
		total += info.Size()
	}
	if p.Bytes != total {
		t.Fatal("incorrect size")
	}
	manifest := filepath.Join(path, "manifest.sealed")
	raw, _ := os.ReadFile(manifest)
	raw[len(raw)-1] ^= 1
	_ = os.WriteFile(manifest, raw, 0600)
	if _, e = inspectArchive(path, b.o.Binding, b.key); e == nil {
		t.Fatal("corrupt completed capture adopted")
	}
}
func TestAgentBackupMTLSAndDefaultDisabled(t *testing.T) {
	b, delivery := offsiteFixture(t)
	pool, _, cert := testAgentPKI(t)
	serverCert, _, _ := cert(AgentIdentity("server-a"))
	a := &Agent{executor: b.x}
	s := httptest.NewUnstartedServer(a.Handler())
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}, MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	s.StartTLS()
	defer s.Close()
	nonce, _ := tenancy.Secret()
	for _, identity := range []string{tenancy.EnterpriseIdentity("a"), AgentIdentity("server-b"), tenancy.PlatformIdentity} {
		pair, _, _ := cert(identity)
		cfg := &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13}
		rpc, e := tenancy.NewRPC(s.URL, cfg, AgentIdentity("server-a"))
		if e != nil {
			t.Fatal(e)
		}
		var out BackupControlReport
		e = rpc.Call(t.Context(), "/internal/agent/backup/submit", BackupControlRequest{Nonce: nonce, Operation: b.o}, &out)
		if identity != tenancy.PlatformIdentity {
			if e == nil {
				t.Fatal("foreign principal accepted")
			}
			continue
		}
		if e != nil || out.Receipt.State != "pending" || out.Nonce != nonce {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(out)
		for _, secret := range []string{"source", "containers", b.root, b.dir} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("private source leaked")
			}
		}
		transport := &http.Transport{TLSClientConfig: cfg}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport}
		for _, payload := range []string{`{"nonce":"` + nonce + `","path":"/"}`, `{} {}`, strings.Repeat(" ", 9000)} {
			res, e := client.Post(s.URL+"/internal/agent/backup/submit", "application/json", strings.NewReader(payload))
			if e != nil {
				t.Fatal(e)
			}
			res.Body.Close()
			if res.StatusCode != 400 {
				t.Fatal("unsafe body accepted")
			}
		}
		b.f.fail = "queued"
		_, _ = b.x.BackupOnce(t.Context())
		r := b.receipt(t)
		if e = rpc.Call(t.Context(), "/internal/agent/backup/retry", BackupControlRequest{Nonce: nonce, Operation: b.o, ExpectedRevision: r.Revision}, &out); e != nil {
			t.Fatal(e)
		}
		b.f.fail = ""
		for range 5 {
			if _, e = b.x.BackupOnce(t.Context()); e != nil {
				t.Fatal(e)
			}
		}
		if _, e = b.x.OffsiteOnce(t.Context()); e != nil {
			t.Fatal(e)
		}
		if e = rpc.Call(t.Context(), "/internal/agent/backup/status", BackupControlRequest{Nonce: nonce, Operation: b.o}, &out); e != nil || out.Receipt.Offsite.Delivery != delivery.proof || !ValidBackupOffsite(out.Receipt) {
			t.Fatal("mTLS delivery receipt", e)
		}
		// Same authenticated caller still cannot use the route on an inspect-only agent.
		idle := httptest.NewUnstartedServer((&Agent{}).Handler())
		idle.TLS = s.TLS.Clone()
		idle.StartTLS()
		defer idle.Close()
		res, e := client.Post(idle.URL+"/internal/agent/backup/status", "application/json", strings.NewReader(`{}`))
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatal("default backup enabled")
		}
	}
}
