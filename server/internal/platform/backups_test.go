package platform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

// State-machine peer for platform transaction/failure tests. Real archive and
// Docker restart behavior lives in deployment's opt-in integration rehearsal.
type backupPeer struct {
	status       deployment.ExecutorStatus
	receipt      deployment.BackupReceipt
	lose         string
	calls        map[string]int
	afterInspect func()
	alter        func(*deployment.BackupControlReport)
	mode         string
}

func (p *backupPeer) Inspect(ctx context.Context, id, nonce string) (deployment.Inspection, error) {
	r, e := serverFixture("a", "a")(ctx, id, nonce)
	r.Capabilities = []string{"inspect", "deploy", "backup"}
	if p.mode != "" {
		r.IsolationMode = p.mode
	}
	if p.afterInspect != nil {
		p.afterInspect()
	}
	return r, e
}
func (p *backupPeer) Backup(_ context.Context, _ string, action string, in deployment.BackupControlRequest) (deployment.BackupControlReport, error) {
	p.calls[action]++
	if action == "submit" && p.receipt.Revision == 0 {
		p.receipt = deployment.BackupReceipt{Operation: in.Operation, State: "pending", Phase: "queued", Attempt: 1, Revision: 1, UpdatedAt: time.Now()}
		p.status.ActiveBackupID = in.Operation.ID
	}
	if action == "retry" || action == "cancel" {
		if p.receipt.Revision != in.ExpectedRevision {
			return deployment.BackupControlReport{}, ErrBackupChanged
		}
		p.receipt.Revision++
		p.receipt.State = "pending"
		p.receipt.RecoveryErrorCode = ""
		if action == "cancel" {
			p.receipt.State = "cancelled"
			p.receipt.Phase = "finished"
			p.status.ActiveBackupID = ""
		}
	}
	if p.lose == action {
		p.lose = ""
		return deployment.BackupControlReport{}, errors.New("private transport detail")
	}
	out := deployment.BackupControlReport{Nonce: in.Nonce, Status: p.status, Receipt: p.receipt}
	if p.alter != nil {
		p.alter(&out)
	}
	return out, nil
}
func (p *backupPeer) terminal(state string) {
	p.receipt.State, p.receipt.Phase = state, "finished"
	p.receipt.Revision++
	p.status.ActiveBackupID = ""
	if state == "completed" {
		p.receipt.Archive = deployment.ArchiveProof{SHA256: strings.Repeat("a", 64), Bytes: 8192, Files: 8}
	}
	if state == "failed" {
		p.receipt.ErrorCode = "BACKUP_ARCHIVE_UNCONFIRMED"
	}
}
func backupFixture(t *testing.T) (*Store, string, BackupRequest, map[string]deployment.Release, *backupPeer, BackupWorker) {
	t.Helper()
	s, token, in, c, p, _, w := deployFixture(t)
	deployRequest(t, s, token, in, c, p)
	deployStep(t, w)
	if _, e := p.x.Once(t.Context()); e != nil {
		t.Fatal(e)
	}
	deployStep(t, w)
	status, _, e := p.x.Status("")
	if e != nil {
		t.Fatal(e)
	}
	peer := &backupPeer{status: status, calls: map[string]int{}}
	inBackup := BackupRequest{RequestID: "backup-first", TenantID: in.TenantID, ServerID: in.ServerID, ReleaseID: in.ReleaseID, ReleaseDigest: in.ReleaseDigest, ExpectedRevision: 1, ExpectedConfigVersion: 1, ExpectedAccessVersion: 2, ExpectedGeneration: 1, Reason: "isolated backup rehearsal", Confirmed: true}
	return s, token, inBackup, c, peer, BackupWorker{Store: s, Agent: peer, Catalog: c, Readiness: deploymentReady}
}
func backupJob(t *testing.T, s *Store, id string) BackupJob {
	t.Helper()
	j, e := scanBackup(s.pool.QueryRow(t.Context(), `SELECT `+backupColumns+` FROM platform_backup_jobs WHERE id=$1`, id))
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func backupStep(t *testing.T, w BackupWorker) {
	t.Helper()
	if _, e := w.Store.pool.Exec(t.Context(), `UPDATE platform_backup_jobs SET retry_at=now()`); e != nil {
		t.Fatal(e)
	}
	if done, e := w.Once(t.Context()); e != nil || !done {
		t.Fatal("backup worker", done, e)
	}
}
func backupRequest(t *testing.T, s *Store, token string, in BackupRequest, c map[string]deployment.Release, p BackupControl) BackupJob {
	t.Helper()
	j, e := s.RequestBackup(t.Context(), "release-operator", token, in, c, p)
	if e != nil {
		t.Fatal(e)
	}
	return j
}

func TestPlatformPostgresBackupLifecycle(t *testing.T) {
	s, token, in, c, p, w := backupFixture(t)
	ctx := t.Context()
	j := backupRequest(t, s, token, in, c, p)
	if len(j.ID) != 39 || strings.Contains(j.ID, "_") {
		t.Fatal("unsafe archive id")
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, e := s.RequestBackup(ctx, "release-operator", token, in, nil, nil); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	changed := in
	changed.Reason = "changed request"
	if _, e := s.RequestBackup(ctx, "release-operator", token, changed, c, p); !errors.Is(e, ErrRequestChanged) {
		t.Fatal(e)
	}
	changed = in
	changed.RequestID = "second"
	if _, e := s.RequestBackup(ctx, "release-operator", token, changed, c, p); !errors.Is(e, ErrDeploymentMaintenance) {
		t.Fatal("second backup", e)
	}
	if _, e := s.RequestRealm(ctx, "a", "resume", "release-operator", "unsafe resume", 2, true, true); !errors.Is(e, ErrDeploymentMaintenance) {
		t.Fatal("resume", e)
	}
	deploymentIn := DeploymentRequest{RequestID: "during-backup", TenantID: in.TenantID, ServerID: in.ServerID, ReleaseID: in.ReleaseID, ReleaseDigest: in.ReleaseDigest, Action: "deploy", ExpectedRevision: 1, ExpectedConfigVersion: 1, ExpectedAccessVersion: 2, ExpectedGeneration: 1, ExpectedReleaseID: in.ReleaseID, Reason: "must not deploy", Confirmed: true}
	if _, e := s.RequestDeployment(ctx, "release-operator", token, deploymentIn, c, nil); !errors.Is(e, ErrDeploymentMaintenance) {
		t.Fatal("deployment", e)
	}
	if e := s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	// Accepted submission loses its ACK while the agent stops the business API.
	p.lose = "submit"
	backupStep(t, w)
	w.Readiness = func(context.Context, string, string) (tenancy.Readiness, error) {
		return tenancy.Readiness{}, ErrUnavailable
	}
	backupStep(t, w)
	if p.calls["submit"] != 2 || backupJob(t, s, j.ID).Receipt.Revision != 1 {
		t.Fatal("lost ACK could not recover without the stopped API")
	}
	p.terminal("completed")
	backupStep(t, w)
	if current := backupJob(t, s, j.ID); current.State != "pending" || current.ErrorCode != "BACKUP_BUSINESS_UNCONFIRMED" {
		t.Fatal("unverified completion", current)
	}
	w.Readiness = deploymentReady
	backupStep(t, w)
	if current := backupJob(t, s, j.ID); current.State != "completed" || current.Receipt.Archive.Bytes != 8192 {
		t.Fatal(current)
	}
	var state string
	var count int
	if e := s.pool.QueryRow(ctx, `SELECT status FROM platform_tenants WHERE id='a'`).Scan(&state); e != nil || state != "suspended" {
		t.Fatal("auto resumed", e)
	}
	if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_audits WHERE action='backup.completed'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("audit", count, e)
	}
	if _, e := s.RequestRealm(ctx, "a", "resume-after", "release-operator", "separate resume", 2, true, true); e != nil {
		t.Fatal(e)
	}
}

func TestPlatformPostgresBackupControls(t *testing.T) {
	for _, name := range []string{"retry_lost_ack", "cancel_lost_ack", "cancel_raced", "failed"} {
		t.Run(name, func(t *testing.T) {
			s, token, in, c, p, w := backupFixture(t)
			j := backupRequest(t, s, token, in, c, p)
			backupStep(t, w)
			if name == "failed" {
				p.terminal("failed")
				backupStep(t, w)
				if backupJob(t, s, j.ID).State != "failed" {
					t.Fatal("capture failure")
				}
				if _, e := s.RequestRealm(t.Context(), "a", "resume-after", "release-operator", "services restored", 2, true, true); e != nil {
					t.Fatal(e)
				}
				return
			}
			action := "cancel"
			if name == "retry_lost_ack" {
				p.receipt.State = "unconfirmed"
				p.receipt.Phase = "restoring_services"
				p.receipt.Revision++
				p.receipt.RecoveryErrorCode = "BACKUP_SERVICE_RECOVERY_UNCONFIRMED"
				backupStep(t, w)
				action = "retry"
				if worked, e := w.Once(t.Context()); worked || e != nil {
					t.Fatal("unconfirmed automatically retried")
				}
			}
			ctl := BackupAction{RequestID: "control-1", Action: action, ExpectedRevision: p.receipt.Revision, Reason: "checked original operation", Confirmed: true}
			if _, e := s.ControlBackup(t.Context(), "release-operator", token, j.ID, ctl); e != nil {
				t.Fatal(e)
			}
			if _, e := s.ControlBackup(t.Context(), "release-operator", token, j.ID, ctl); e != nil {
				t.Fatal("replay", e)
			}
			changed := ctl
			changed.Reason = "changed"
			if _, e := s.ControlBackup(t.Context(), "release-operator", token, j.ID, changed); !errors.Is(e, ErrRequestChanged) {
				t.Fatal(e)
			}
			if name == "cancel_raced" {
				p.receipt.Phase = "quiescing"
				p.receipt.State = "running"
				p.receipt.Revision++
			} else {
				p.lose = action
			}
			backupStep(t, w)
			backupStepIfPending(t, w)
			j = backupJob(t, s, j.ID)
			if name == "cancel_lost_ack" {
				if j.State != "cancelled" || p.calls[action] != 1 {
					t.Fatal(j, p.calls)
				}
			} else {
				if j.State != "pending" || (name == "cancel_raced" && p.calls[action] != 0) || (name == "retry_lost_ack" && p.calls[action] != 1) {
					t.Fatal(j, p.calls)
				}
			}
		})
	}
}
func backupStepIfPending(t *testing.T, w BackupWorker) {
	t.Helper()
	_, _ = w.Store.pool.Exec(t.Context(), `UPDATE platform_backup_jobs SET retry_at=now()`)
	if _, e := w.Once(t.Context()); e != nil {
		t.Fatal(e)
	}
}

func TestPlatformPostgresBackupFences(t *testing.T) {
	for _, name := range []string{"revoked", "changed_binding", "active_tenant", "active_deploy", "active_backup", "journal_reset", "wrong_release", "reason", "confirmation", "request_audit_failure"} {
		t.Run(name, func(t *testing.T) {
			s, token, in, c, p, _ := backupFixture(t)
			ctx := t.Context()
			switch name {
			case "revoked":
				p.afterInspect = func() {
					_, _ = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET enabled=false WHERE id='release-operator'`)
				}
			case "changed_binding":
				p.afterInspect = func() { _, _ = s.pool.Exec(ctx, `UPDATE platform_servers SET revision=revision+1`) }
			case "active_tenant":
				_, _ = s.pool.Exec(ctx, `UPDATE platform_tenants SET status='active' WHERE id='a'`)
			case "active_deploy":
				p.status.ActiveOperationID = "unknown"
			case "active_backup":
				p.status.ActiveBackupID = "unknown"
			case "journal_reset":
				p.status.Generation = 0
			case "wrong_release":
				in.ReleaseDigest = strings.Repeat("b", 64)
			case "reason":
				in.Reason = ""
			case "confirmation":
				in.Confirmed = false
			case "request_audit_failure":
				if _, e := s.pool.Exec(ctx, `CREATE FUNCTION reject_backup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='backup.requested' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$;CREATE TRIGGER reject_backup BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION reject_backup()`); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := s.RequestBackup(ctx, "release-operator", token, in, c, p); e == nil {
				t.Fatal("unsafe request")
			}
			var count int
			if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_backup_jobs`).Scan(&count); e != nil || count != 0 {
				t.Fatal("partial request", count, e)
			}
		})
	}
}

func TestPlatformPostgresBackupCompletionFences(t *testing.T) {
	for _, name := range []string{"audit_failure", "lease_loss", "wrong_tenant", "wrong_host", "active_deploy", "proof", "revision", "service_failure", "enabled_realm"} {
		t.Run(name, func(t *testing.T) {
			s, token, in, c, p, w := backupFixture(t)
			j := backupRequest(t, s, token, in, c, p)
			backupStep(t, w)
			p.terminal("completed")
			p.alter = func(out *deployment.BackupControlReport) {
				switch name {
				case "wrong_tenant":
					out.Status.TenantID = "b"
				case "wrong_host":
					out.Status.HostFingerprint = strings.Repeat("b", 64)
				case "active_deploy":
					out.Status.ActiveOperationID = "other"
				case "proof":
					out.Receipt.Archive.Files = 7
				case "revision":
					out.Receipt.Revision = 0
				}
			}
			if name == "audit_failure" {
				if _, e := s.pool.Exec(t.Context(), `CREATE FUNCTION reject_backup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='backup.completed' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$;CREATE TRIGGER reject_backup BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION reject_backup()`); e != nil {
					t.Fatal(e)
				}
			}
			w.Readiness = func(ctx context.Context, id, nonce string) (tenancy.Readiness, error) {
				r, e := deploymentReady(ctx, id, nonce)
				switch name {
				case "lease_loss":
					_, e = s.pool.Exec(ctx, `UPDATE platform_backup_jobs SET lease_id='new-owner' WHERE id=$1`, j.ID)
				case "service_failure":
					r.Checks["media"] = false
				case "enabled_realm":
					r.Realm.Enabled = true
				}
				return r, e
			}
			_, _ = s.pool.Exec(t.Context(), `UPDATE platform_backup_jobs SET retry_at=now()`)
			_, _ = w.Once(t.Context())
			if backupJob(t, s, j.ID).State == "completed" {
				t.Fatal("unsafe completion")
			}
			if _, e := s.RequestRealm(t.Context(), "a", "resume", "release-operator", "unsafe resume", 2, true, true); e == nil {
				t.Fatal("unsafe resume")
			}
		})
	}
}

func TestPlatformPostgresBackupHTTP(t *testing.T) {
	s, token, in, c, p, w := backupFixture(t)
	j := backupRequest(t, s, token, in, c, p)
	backupStep(t, w)
	api := &API{Store: s, Limiter: testLimit{}, DeploymentCatalog: c}
	status, result := adminTestCall(t, api, "GET", "/platform/admin/backups?q="+j.ID, token, "")
	if status != 200 || result["total"] != float64(1) {
		t.Fatal("list", status, result)
	}
	b, _ := json.Marshal(result)
	if strings.Contains(string(b), "keyFile") || strings.Contains(string(b), "directory") || strings.Contains(string(b), "containers") || strings.Contains(string(b), "volumes") {
		t.Fatal("private backup configuration returned")
	}
	for _, state := range []string{"reader", "disabled", "expired"} {
		switch state {
		case "reader":
			_, _ = s.pool.Exec(t.Context(), `UPDATE platform_admin_accounts SET role='reader' WHERE id='release-operator'`)
		case "disabled":
			_, _ = s.pool.Exec(t.Context(), `UPDATE platform_admin_accounts SET role='operator',enabled=false WHERE id='release-operator'`)
		case "expired":
			_, _ = s.pool.Exec(t.Context(), `UPDATE platform_admin_accounts SET enabled=true WHERE id='release-operator';UPDATE platform_admin_sessions SET expires_at=now()-interval '1 minute'`)
		}
		b, _ = json.Marshal(in)
		if status, _ = adminTestCall(t, api, "POST", "/platform/admin/backups", token, string(b)); status != 401 {
			t.Fatal(state, "request", status)
		}
		b, _ = json.Marshal(BackupAction{RequestID: "cancel", Action: "cancel", ExpectedRevision: 1, Reason: "test permission", Confirmed: true})
		if status, _ = adminTestCall(t, api, "POST", "/platform/admin/backups/"+j.ID+"/control", token, string(b)); status != 401 {
			t.Fatal(state, "control", status)
		}
	}
}
