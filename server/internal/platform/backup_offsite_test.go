package platform

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/deployment"
)

func completedLocalOffsite(t *testing.T) (*Store, BackupJob, *backupPeer, BackupWorker) {
	t.Helper()
	s, token, in, c, p, w := backupFixture(t)
	p.status.BackupOffsiteTargetID = "remote-a"
	j := backupRequest(t, s, token, in, c, p)
	if j.Operation.OffsiteTargetID != "remote-a" {
		t.Fatal("target not pinned")
	}
	backupStep(t, w)
	p.terminal("completed")
	now := time.Now().UTC()
	p.receipt.Offsite = deployment.OffsiteReceipt{State: "pending", UpdatedAt: now, RetryAt: now}
	backupStep(t, w)
	j = backupJob(t, s, j.ID)
	if j.State != "completed" || j.Receipt.Offsite.State != "pending" {
		t.Fatal(j)
	}
	return s, j, p, w
}
func deliveryStep(t *testing.T, w BackupWorker) {
	t.Helper()
	if _, e := w.Store.pool.Exec(t.Context(), `UPDATE platform_backup_jobs SET retry_at=now()`); e != nil {
		t.Fatal(e)
	}
	if ok, e := w.OffsiteOnce(t.Context()); !ok || e != nil {
		t.Fatal(ok, e)
	}
}
func finishPeerDelivery(p *backupPeer) {
	p.receipt.Revision++
	p.receipt.UpdatedAt = time.Now().UTC()
	p.receipt.Offsite = deployment.OffsiteReceipt{State: "completed", Attempts: 1, UpdatedAt: time.Now().UTC(), Delivery: backup.Delivery{Version: 1, TargetID: "remote-a", Binding: p.receipt.Operation.Binding, Manifest: backup.File{Name: "manifest", SHA256: p.receipt.Archive.SHA256, Size: 1000}}}
}

func TestPlatformPostgresOffsiteDeliveryAfterBusinessResume(t *testing.T) {
	s, j, p, w := completedLocalOffsite(t)
	ctx := t.Context()
	if _, e := s.RequestRealm(ctx, "a", "resume-with-delivery", "release-operator", "local recovery verified", 2, true, true); e != nil {
		t.Fatal("delivery blocked resumption", e)
	}
	// A newer deployment/current access state is not the archive's identity.
	p.status.Generation++
	p.lose = "status"
	deliveryStep(t, w)
	if r := backupJob(t, s, j.ID); r.State != "completed" || r.ErrorCode != "BACKUP_OFFSITE_CONTROL_UNCONFIRMED" || r.Receipt.Offsite.State != "pending" {
		t.Fatal(r)
	}
	finishPeerDelivery(p)
	// A new worker instance recovers using the saved immutable operation.
	deliveryStep(t, BackupWorker{Store: s, Agent: p})
	r := backupJob(t, s, j.ID)
	if r.State != "completed" || r.ErrorCode != "" || r.Receipt.Offsite.State != "completed" || r.Receipt.Offsite.Delivery.Binding.AccessVersion != 2 {
		t.Fatal(r)
	}
	var count int
	if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_audits WHERE action='backup.offsite.completed' AND job_id=$1`, j.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	if ok, e := w.OffsiteOnce(ctx); ok || e != nil {
		t.Fatal("duplicate completion", ok, e)
	}
	if p.calls["submit"] != 1 || p.calls["retry"] != 0 {
		t.Fatal("delivery recaptured archive")
	}
	if e := s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestPlatformPostgresOffsiteRejectsUnboundOrRegressedReceipts(t *testing.T) {
	for _, name := range []string{"owner", "host", "target", "manifest", "capture-attempt", "revision", "equal-revision", "missing", "size"} {
		t.Run(name, func(t *testing.T) {
			s, j, p, w := completedLocalOffsite(t)
			finishPeerDelivery(p)
			p.alter = func(out *deployment.BackupControlReport) {
				switch name {
				case "owner":
					out.Receipt.Offsite.Delivery.Binding.TenantID = "b"
				case "host":
					out.Status.HostFingerprint = strings.Repeat("f", 64)
				case "target":
					out.Receipt.Offsite.Delivery.TargetID = "remote-b"
				case "manifest":
					out.Receipt.Offsite.Delivery.Manifest.SHA256 = strings.Repeat("d", 64)
				case "capture-attempt":
					out.Receipt.Attempt++
				case "revision":
					out.Receipt.Revision = 1
				case "equal-revision":
					out.Receipt.Revision = j.Receipt.Revision
				case "missing":
					out.Receipt.Offsite = deployment.OffsiteReceipt{}
				case "size":
					out.Receipt.Offsite.Delivery.Manifest.Size = 0
				}
			}
			deliveryStep(t, w)
			if r := backupJob(t, s, j.ID); r.ErrorCode != "BACKUP_OFFSITE_CONTROL_UNCONFIRMED" || r.Receipt.Offsite.State != "pending" {
				t.Fatal(r)
			}
			var count int
			if e := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM platform_audits WHERE action='backup.offsite.completed'`).Scan(&count); e != nil || count != 0 {
				t.Fatal(count, e)
			}
		})
	}
}

func TestPlatformPostgresOffsiteLeaseAndReceiptAuditAtomicity(t *testing.T) {
	s, j, p, w := completedLocalOffsite(t)
	if _, e := s.pool.Exec(t.Context(), `UPDATE platform_backup_jobs SET retry_at=now()`); e != nil {
		t.Fatal(e)
	}
	var claims atomic.Int32
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			got, e := w.claimOffsite(t.Context())
			if e != nil {
				t.Error(e)
			}
			if got != nil {
				claims.Add(1)
			}
		})
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal(claims.Load())
	}
	if _, e := s.pool.Exec(t.Context(), `UPDATE platform_backup_jobs SET lease_until=now()-interval '1 second'`); e != nil {
		t.Fatal(e)
	}
	finishPeerDelivery(p)
	// Failure to commit the audit also rolls back the delivery confirmation.
	if _, e := s.pool.Exec(t.Context(), `CREATE FUNCTION deny_offsite_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='backup.offsite.completed' THEN RAISE EXCEPTION 'fixture audit unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER deny_offsite BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION deny_offsite_audit()`); e != nil {
		t.Fatal(e)
	}
	if ok, e := w.OffsiteOnce(t.Context()); !ok || e == nil {
		t.Fatal("audit failure accepted", ok, e)
	}
	if r := backupJob(t, s, j.ID); r.Receipt.Offsite.State != "pending" {
		t.Fatal(r)
	}
	if _, e := s.pool.Exec(t.Context(), `DROP TRIGGER deny_offsite ON platform_audits; UPDATE platform_backup_jobs SET lease_until=now()-interval '1 second'`); e != nil {
		t.Fatal(e)
	}
	deliveryStep(t, w)
	if r := backupJob(t, s, j.ID); r.Receipt.Offsite.State != "completed" {
		t.Fatal(r)
	}
}

func TestPlatformPostgresOffsiteCompletionBeforeFirstPoll(t *testing.T) {
	s, token, in, c, p, w := backupFixture(t)
	p.status.BackupOffsiteTargetID = "remote-a"
	j := backupRequest(t, s, token, in, c, p)
	backupStep(t, w)
	p.terminal("completed")
	finishPeerDelivery(p)
	backupStep(t, w)
	var count int
	if e := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM platform_audits WHERE action='backup.offsite.completed' AND job_id=$1`, j.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	if ok, e := w.OffsiteOnce(t.Context()); ok || e != nil {
		t.Fatal(ok, e)
	}
}

func TestPlatformPostgresOffsiteDoesNotResubmitUncertainLocalBackup(t *testing.T) {
	s, j, p, w := completedLocalOffsite(t)
	p.receipt.Offsite.State = "unconfirmed"
	p.receipt.Offsite.ErrorCode = "BACKUP_OFFSITE_UNCONFIRMED"
	p.receipt.Offsite.Attempts = 1
	p.receipt.Offsite.RetryAt = time.Now().Add(time.Minute).UTC()
	p.receipt.Revision++
	deliveryStep(t, w)
	first := backupJob(t, s, j.ID)
	if first.Receipt.Offsite.State != "unconfirmed" {
		t.Fatal(first)
	}
	deliveryStep(t, w)
	var count int
	if e := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM platform_audits WHERE action='backup.offsite.unconfirmed' AND job_id=$1`, j.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	raw, _ := json.Marshal(first)
	if strings.Contains(string(raw), "credential") || p.calls["submit"] != 1 || p.calls["retry"] != 0 {
		t.Fatal("unsafe delivery recovery")
	}
}

func TestPlatformPostgresProductionBackupRequiresOffsite(t *testing.T) {
	s, token, in, c, p, _ := backupFixture(t)
	p.mode = "dedicated_host"
	if _, e := s.pool.Exec(t.Context(), `UPDATE platform_servers SET isolation_mode='dedicated_host' WHERE id=$1`, in.ServerID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RequestBackup(t.Context(), "release-operator", token, in, c, p); e == nil {
		t.Fatal("production backup without offsite accepted")
	}
	var count int
	if e := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM platform_backup_jobs`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	p.status.BackupOffsiteTargetID = "remote-a"
	j := backupRequest(t, s, token, in, c, p)
	if j.Operation.OffsiteTargetID != "remote-a" {
		t.Fatal("offsite not required")
	}
}

func TestPlatformPostgresDailyBackupResumesBeforeOffsiteConfirmation(t *testing.T) {
	s, token, maintenance, worker, peer, realm := maintenanceFixture(t)
	peer.status.BackupOffsiteTargetID = "remote-a"
	scheduleRun(t, s, token)
	pauseDaily(t, maintenance, realm)
	j := maintenanceStep(t, maintenance)
	if j.BackupID == "" {
		t.Fatal(j)
	}
	backupStep(t, worker)
	peer.terminal("completed")
	peer.receipt.Offsite = deployment.OffsiteReceipt{State: "unconfirmed", Attempts: 1, ErrorCode: "BACKUP_OFFSITE_UNCONFIRMED", UpdatedAt: time.Now().UTC(), RetryAt: time.Now().Add(time.Minute).UTC()}
	backupStep(t, worker)
	if j = maintenanceStep(t, maintenance); j.Phase != "resume" {
		t.Fatal(j)
	}
	finishDaily(t, maintenance, realm, "completed")
	if r := backupJob(t, s, j.BackupID); r.State != "completed" || r.Receipt.Offsite.State != "unconfirmed" {
		t.Fatal(r)
	}
	finishPeerDelivery(peer)
	deliveryStep(t, worker)
	if r := backupJob(t, s, j.BackupID); r.Receipt.Offsite.State != "completed" {
		t.Fatal(r)
	}
}
