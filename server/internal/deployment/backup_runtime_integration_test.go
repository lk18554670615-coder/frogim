package deployment

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/backup"
)

// Runs exclusively inside the disposable nine-service fixture, after its
// realm suspension has been confirmed. No default-enterprise resources used.
func durableBackupDrill(t *testing.T, r *ComposeRunner, x *Executor, binding backup.Binding, key []byte) {
	t.Helper()
	ctx := t.Context()
	root := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "backup.key")
	if e := writePrivate(keyPath, key); e != nil {
		t.Fatal(e)
	}
	if e := x.ConfigureBackups(root, keyPath); e != nil {
		t.Fatal("backup configure", e)
	}
	status, _, _ := x.Status("")
	op := BackupOperation{ID: "durable-backup-fixture", HostFingerprint: status.HostFingerprint, Binding: binding}
	if _, e := x.SubmitBackup(op); e != nil {
		t.Fatal(e)
	}
	step := func(want string) {
		t.Helper()
		if _, e := x.BackupOnce(ctx); e != nil {
			t.Fatal("durable backup step", want, e)
		}
		_, receipt, _ := x.BackupStatus(op.ID)
		if receipt.Phase != want {
			t.Fatal("wrong phase", receipt.Phase, want)
		}
	}
	step("quiescing")
	step("snapshotting")
	p, e := x.backupPlan(op)
	if e != nil {
		t.Fatal(e)
	}
	p.Attempt = 1
	s := x.state.Backups[op.ID].Source.clone()
	path := filepath.Join(root, op.ID+"-attempt-001")
	proof, e := x.backupIO.Capture(ctx, p, s, path, key)
	if e != nil {
		t.Fatal("capture before lost ACK", e)
	}
	// Crash equivalent: complete archive on disk, journal still snapshotting.
	// Also create the exact read-only helper a killed process may leave behind.
	c := x.backupIO.(*composeBackupIO)
	b, e := c.bundle(p)
	if e != nil {
		t.Fatal(e)
	}
	drifted := s.clone()
	drifted.Volumes["postgres"] = strings.Repeat("0", 64)
	if c.Restore(ctx, p, drifted) == nil || c.match(ctx, p, b, s, true) != nil {
		t.Fatal("changed volume identity started services")
	}
	sources, _ := coldVolumeSources(b)
	args := []string{"create", "--pull=never", "--network=none", "--read-only", "--user=0:0", "--cap-drop=ALL", "--cap-add=DAC_OVERRIDE", "--security-opt=no-new-privileges", "--interactive", "--mount", "type=volume,source=" + r.Project + "_" + sources["im"] + ",target=/volume,volume-nocopy,readonly"}
	args = append(args, r.helperLabels("volume-export")...)
	args = append(args, "--label", "io.frogim.backup-job="+op.ID, "--label", "io.frogim.backup-attempt=1", "--entrypoint", "/opt/frogim/tenant-volume", b.Services["enterprise-api"].Image, "export")
	out, e := r.command(ctx, nil, args...)
	helper := strings.TrimSpace(string(out))
	if e != nil || !fingerprint.MatchString(helper) {
		t.Fatal("orphan fixture create")
	}
	t.Cleanup(func() { r.removeHelper(helper, "volume-export") })
	wrongAttempt := p
	wrongAttempt.Attempt++
	if c.recoverHelpers(ctx, wrongAttempt, b) == nil {
		t.Fatal("another attempt's helper was adopted")
	}
	if _, e = r.command(ctx, nil, "inspect", "--format", "{{.Id}}", helper); e != nil {
		t.Fatal("another attempt's helper was removed")
	}
	step("restoring_services")
	_, receipt, _ := x.BackupStatus(op.ID)
	if receipt.Archive != proof {
		t.Fatal("lost ACK re-created archive")
	}
	if _, e = r.command(ctx, nil, "inspect", "--format", "{{.Id}}", helper); e == nil {
		t.Fatal("owned orphan retained")
	}
	step("verifying")
	step("finished")
	_, receipt, _ = x.BackupStatus(op.ID)
	if receipt.State != "completed" || c.match(ctx, p, b, s, false) != nil {
		t.Fatal("original service identities not recovered")
	}
	// A partial archive must fail, retain files, and recover the SAME services.
	op.ID = "partial-backup-fixture"
	if _, e = x.SubmitBackup(op); e != nil {
		t.Fatal(e)
	}
	step("quiescing")
	step("snapshotting")
	partial := filepath.Join(root, op.ID+"-attempt-001")
	if os.Mkdir(partial, 0700) != nil {
		t.Fatal("partial fixture")
	}
	if _, e = x.BackupOnce(ctx); e == nil {
		t.Fatal("partial archive accepted")
	}
	step("verifying")
	step("finished")
	_, receipt, _ = x.BackupStatus(op.ID)
	if receipt.State != "failed" || receipt.ErrorCode != "BACKUP_ARCHIVE_UNCONFIRMED" {
		t.Fatal("partial attempt reported success")
	}
	if _, e = os.Stat(partial); e != nil {
		t.Fatal("partial evidence removed")
	}
	check, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if c.Verify(check, p, s) != nil {
		t.Fatal("failed backup did not recover original healthy suspended services")
	}
	t.Log("durable backup recovered completed archive without re-capture, removed only owned read-only orphan helper, restored original container/volume identities after success and partial failure, and kept realm suspended")
}
