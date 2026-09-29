package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/backup"
)

type sealedFixtureIO struct{ *backupFixtureIO }

func (f sealedFixtureIO) Capture(_ context.Context, p backupPlan, _ backupSource, path string, key []byte) (ArchiveProof, error) {
	if e := f.call("snapshotting"); e != nil {
		return ArchiveProof{}, e
	}
	w, e := backup.Create(path, p.Request.Binding, key)
	if e != nil {
		return ArchiveProof{}, e
	}
	defer w.Close()
	for _, name := range append(slices.Clone(coldVolumes), "compose", "release", "database") {
		if e = w.Add(name, func(dst io.Writer) error { _, err := io.WriteString(dst, "fixture-"+name); return err }); e != nil {
			return ArchiveProof{}, e
		}
	}
	if e = w.Finalize(); e != nil {
		return ArchiveProof{}, e
	}
	return inspectArchive(path, p.Request.Binding, key)
}

type deliveryFixture struct {
	calls            int
	fail             bool
	alter            func(*backup.Delivery)
	entered, proceed chan struct{}
	proof            backup.Delivery
}

func (d *deliveryFixture) Close() {}
func (d *deliveryFixture) Deliver(ctx context.Context, b backup.Binding, path string, key []byte) (backup.Delivery, error) {
	d.calls++
	if d.entered != nil {
		close(d.entered)
		select {
		case <-d.proceed:
		case <-ctx.Done():
			return backup.Delivery{}, ctx.Err()
		}
	}
	r, e := backup.Open(path, b, key)
	if e != nil {
		return backup.Delivery{}, e
	}
	defer r.Close()
	d.proof = backup.Delivery{Version: 1, TargetID: "remote-a", Binding: b, Manifest: r.ManifestProof()}
	if d.fail {
		return backup.Delivery{}, errors.New("secret provider error")
	}
	if d.alter != nil {
		d.alter(&d.proof)
	}
	return d.proof, nil
}

func offsiteFixture(t *testing.T) (backupFixture, *deliveryFixture) {
	b := newBackupFixture(t)
	b.x.backupIO = sealedFixtureIO{b.f}
	d := &deliveryFixture{}
	if e := b.x.configureOffsite("remote-a", strings.Repeat("e", 64), d); e != nil {
		t.Fatal(e)
	}
	b.o.OffsiteTargetID = "remote-a"
	return b, d
}
func finishCapture(t *testing.T, b *backupFixture) {
	t.Helper()
	if _, e := b.x.SubmitBackup(b.o); e != nil {
		t.Fatal(e)
	}
	for range 5 {
		if ok, e := b.x.BackupOnce(t.Context()); !ok || e != nil {
			t.Fatal(ok, e)
		}
	}
	if r := b.receipt(t); r.State != "completed" || r.Offsite.State != "pending" {
		t.Fatal(r)
	}
}
func makeDeliveryDue(t *testing.T, b *backupFixture) {
	t.Helper()
	r := b.x.state.Backups[b.o.ID]
	r.Offsite.RetryAt = time.Now().Add(-time.Second).UTC()
	b.x.state.Backups[b.o.ID] = r
	if e := b.x.saveLocked(); e != nil {
		t.Fatal(e)
	}
}
func restartOffsite(t *testing.T, b *backupFixture, d *deliveryFixture) {
	b.restart(t)
	b.x.backupIO = sealedFixtureIO{b.f}
	if e := b.x.configureOffsite("remote-a", strings.Repeat("e", 64), d); e != nil {
		t.Fatal(e)
	}
}

func TestBackupOffsiteRetryRestartAndLostAcknowledgement(t *testing.T) {
	b, d := offsiteFixture(t)
	if ok, e := b.x.OffsiteOnce(t.Context()); ok || e != nil {
		t.Fatal("delivery before capture", ok, e)
	}
	finishCapture(t, &b)
	d.fail = true
	if ok, e := b.x.OffsiteOnce(t.Context()); !ok || !errors.Is(e, ErrUnconfirmed) {
		t.Fatal(ok, e)
	}
	r := b.receipt(t)
	if r.State != "completed" || r.Offsite.State != "unconfirmed" || !ValidBackupOffsite(r) || r.Offsite.ErrorCode != "BACKUP_OFFSITE_UNCONFIRMED" || r.Offsite.Delivery != (backup.Delivery{}) {
		t.Fatal(r)
	}
	if ok, e := b.x.OffsiteOnce(t.Context()); ok || e != nil {
		t.Fatal("backoff ignored", ok, e)
	}
	makeDeliveryDue(t, &b)
	// Model death after remote manifest commit but before local receipt commit.
	record := b.x.state.Backups[b.o.ID]
	record.Offsite.State, record.Offsite.ErrorCode = "running", ""
	b.x.state.Backups[b.o.ID] = record
	if e := b.x.saveLocked(); e != nil {
		t.Fatal(e)
	}
	restartOffsite(t, &b, d)
	d.fail = false
	if ok, e := b.x.OffsiteOnce(t.Context()); !ok || e != nil {
		t.Fatal(ok, e)
	}
	r = b.receipt(t)
	if r.Offsite.State != "completed" || r.Offsite.Delivery != d.proof || r.Offsite.Attempts != 2 || !ValidBackupOffsite(r) {
		t.Fatal(r)
	}
	restartOffsite(t, &b, d)
	if ok, e := b.x.OffsiteOnce(t.Context()); ok || e != nil {
		t.Fatal("completed re-uploaded", ok, e)
	}
	for _, phase := range []string{"queued", "quiescing", "snapshotting", "restoring_services", "verifying"} {
		if b.f.calls[phase] != 1 {
			t.Fatal("cold maintenance repeated", phase)
		}
	}
	if d.calls != 2 {
		t.Fatal(d.calls)
	}
}

func TestBackupOffsiteDoesNotBlockLaterMaintenance(t *testing.T) {
	b, d := offsiteFixture(t)
	finishCapture(t, &b)
	d.entered, d.proceed = make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() { _, e := b.x.OffsiteOnce(t.Context()); done <- e }()
	<-d.entered
	defer close(d.proceed)
	if ok, e := b.x.OffsiteOnce(t.Context()); ok || e != nil {
		t.Fatal("parallel delivery", ok, e)
	}
	op := fixtureOperation(b.c, "upgrade", "release-two", "release-one", 1)
	if _, e := b.x.Submit(op); e != nil {
		t.Fatal("delivery blocked deploy", e)
	}
	if ok, e := b.x.Once(t.Context()); !ok || e != nil {
		t.Fatal(ok, e)
	}
	d.proceed <- struct{}{}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if r := b.receipt(t); r.Offsite.State != "completed" || r.Operation.Binding.Generation != 1 {
		t.Fatal(r)
	}
	restartOffsite(t, &b, d)
}

func TestBackupOffsiteCorruptionAndForgedReceiptFailClosed(t *testing.T) {
	for _, name := range []string{"ciphertext", "replaced-proof", "foreign-owner", "foreign-target", "manifest", "size"} {
		t.Run(name, func(t *testing.T) {
			b, d := offsiteFixture(t)
			finishCapture(t, &b)
			if name == "ciphertext" {
				if e := os.WriteFile(filepath.Join(b.root, "backup-one-attempt-001", "database.sealed"), []byte("corrupt"), 0600); e != nil {
					t.Fatal(e)
				}
			} else if name == "replaced-proof" {
				r := b.x.state.Backups[b.o.ID]
				r.Archive.SHA256 = strings.Repeat("d", 64)
				b.x.state.Backups[b.o.ID] = r
			} else {
				d.alter = func(v *backup.Delivery) {
					switch name {
					case "foreign-owner":
						v.Binding.TenantID = "b"
					case "foreign-target":
						v.TargetID = "remote-b"
					case "manifest":
						v.Manifest.SHA256 = strings.Repeat("f", 64)
					case "size":
						v.Manifest.Size = 0
					}
				}
			}
			if ok, e := b.x.OffsiteOnce(t.Context()); !ok || !errors.Is(e, ErrUnconfirmed) {
				t.Fatal(ok, e)
			}
			if r := b.receipt(t); r.Offsite.State != "unconfirmed" || r.Offsite.Delivery != (backup.Delivery{}) {
				t.Fatal(r)
			}
			if (name == "ciphertext" || name == "replaced-proof") && d.calls != 0 {
				t.Fatal("unauthenticated archive uploaded")
			}
		})
	}
}

func TestBackupOffsiteConfigurationAndLegacyLocalOnly(t *testing.T) {
	b := newBackupFixture(t)
	if _, e := b.x.SubmitBackup(b.o); e != nil {
		t.Fatal(e)
	}
	d := &deliveryFixture{}
	if e := b.x.configureOffsite("remote-a", strings.Repeat("e", 64), d); e != nil {
		t.Fatal("upgrade stranded existing recovery", e)
	}
	for range 5 {
		if _, e := b.x.BackupOnce(t.Context()); e != nil {
			t.Fatal(e)
		}
	}
	if b.receipt(t).Offsite != (OffsiteReceipt{}) {
		t.Fatal("retroactive delivery claim")
	}
	newOp := b.o
	newOp.ID = "new-backup"
	if _, e := b.x.SubmitBackup(newOp); !errors.Is(e, ErrConflict) {
		t.Fatal("offsite obligation omitted", e)
	}
	newOp.OffsiteTargetID = "remote-a"
	if _, e := b.x.SubmitBackup(newOp); e != nil {
		t.Fatal(e)
	}
	b.restart(t)
	if e := b.x.configureOffsite("remote-a", strings.Repeat("f", 64), d); e == nil {
		t.Fatal("destination changed")
	}
	if _, e := b.x.SubmitBackup(BackupOperation{ID: "newer", HostFingerprint: b.o.HostFingerprint, Binding: b.o.Binding, OffsiteTargetID: "remote-a"}); e == nil {
		t.Fatal("missing config accepted")
	}
	if e := b.x.configureOffsite("remote-a", strings.Repeat("e", 64), d); e != nil {
		t.Fatal(e)
	}
}

func TestBackupOffsitePrivateConfigOwnerAndCredentialRotation(t *testing.T) {
	b := newBackupFixture(t)
	c := backup.OffsiteConfig{ID: "remote-a", Scope: "enterprise", TenantID: "a", ServerID: "server-a", Endpoint: "https://offsite.example", Bucket: "backup-test", Prefix: "backup", Region: "us-east-1", AccessKey: "fixture", SecretKey: strings.Repeat("s", 40)}
	wrong := c
	wrong.TenantID = "b"
	if b.x.ConfigureOffsite(wrong) == nil {
		t.Fatal("foreign owner configured")
	}
	if e := b.x.ConfigureOffsite(c); e != nil {
		t.Fatal(e)
	}
	b.restart(t)
	wrong = c
	wrong.Bucket = "another-bucket"
	if b.x.ConfigureOffsite(wrong) == nil {
		t.Fatal("repinned destination")
	}
	c.AccessKey = "rotated"
	c.SecretKey = strings.Repeat("r", 40)
	if e := b.x.ConfigureOffsite(c); e != nil {
		t.Fatal("credential-only rotation", e)
	}
}

func TestAgentOffsiteConfigurationCannotDisappear(t *testing.T) {
	for _, mode := range []string{"local_preview", "dedicated_host"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			keyPath := filepath.Join(root, "key")
			if e := os.WriteFile(keyPath, []byte(strings.Repeat("k", 32)), 0600); e != nil {
				t.Fatal(e)
			}
			catalog := fixtureCatalog()
			for id, r := range catalog {
				r.IsolationMode = mode
				r.TenantID = "a"
				r.ServerID = "server-a"
				catalog[id] = r
			}
			c := backup.OffsiteConfig{ID: "remote-a", Scope: "enterprise", TenantID: "a", ServerID: "server-a", Endpoint: "https://offsite.example", Bucket: "backup-test", Prefix: "backup", Region: "us-east-1", AccessKey: "fixture", SecretKey: strings.Repeat("s", 40)}
			bc := map[string]any{"directory": t.TempDir(), "keyFile": keyPath}
			config := map[string]any{"stateDirectory": t.TempDir(), "bundleDirectory": t.TempDir(), "dockerBinary": filepath.Join(root, "never-executed-docker"), "dockerEndpoint": "unix:///var/run/docker.sock", "catalog": catalogList(catalog), "backup": bc}
			path := filepath.Join(root, "executor.json")
			write := func() {
				t.Helper()
				raw, _ := json.Marshal(config)
				if e := os.WriteFile(path, raw, 0600); e != nil {
					t.Fatal(e)
				}
			}
			newAgent := func() *Agent {
				return &Agent{report: Inspection{ServerID: "server-a", TenantID: "a", Runtime: "linux/amd64", HostFingerprint: strings.Repeat("a", 64), IsolationMode: mode}}
			}
			write()
			a := newAgent()
			if mode == "dedicated_host" {
				if a.ConfigureExecutor(path) == nil {
					a.Close()
					t.Fatal("production local-only accepted")
				}
			}
			bc["offsite"] = c
			write()
			a = newAgent()
			if e := a.ConfigureExecutor(path); e != nil {
				t.Fatal(e)
			}
			a.Close()
			delete(bc, "offsite")
			write()
			a = newAgent()
			if a.ConfigureExecutor(path) == nil {
				a.Close()
				t.Fatal("removed target accepted")
			}
			bc["offsite"] = c
			write()
			a = newAgent()
			if e := a.ConfigureExecutor(path); e != nil {
				t.Fatal("same config restart", e)
			}
			a.Close()
		})
	}
}
