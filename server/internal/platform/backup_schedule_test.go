package platform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

func TestMaintenanceWindow(t *testing.T) {
	for _, tc := range []struct {
		at, start, end   string
		minute, duration int
		due              bool
	}{
		{"2026-09-29T22:59:59Z", "2026-09-28T23:00:00Z", "2026-09-29T01:00:00Z", 1380, 120, false},
		{"2026-09-29T23:00:00Z", "2026-09-29T23:00:00Z", "2026-09-30T01:00:00Z", 1380, 120, true},
		{"2026-09-30T00:59:59Z", "2026-09-29T23:00:00Z", "2026-09-30T01:00:00Z", 1380, 120, true},
		{"2026-09-30T01:00:00Z", "2026-09-29T23:00:00Z", "2026-09-30T01:00:00Z", 1380, 120, false},
		{"2026-09-30T07:30:00+08:00", "2026-09-29T23:00:00Z", "2026-09-30T01:00:00Z", 1380, 120, true},
		{"2026-09-30T00:15:00Z", "2026-09-30T00:00:00Z", "2026-09-30T00:15:00Z", 0, 15, false},
	} {
		t.Run(tc.at, func(t *testing.T) {
			now, e := time.Parse(time.RFC3339, tc.at)
			if e != nil {
				t.Fatal(e)
			}
			a, b, due := maintenanceWindow(now, tc.minute, tc.duration)
			if a.Format(time.RFC3339) != tc.start || b.Format(time.RFC3339) != tc.end || due != tc.due {
				t.Fatal(a, b, due)
			}
		})
	}
}

func scheduleInput() BackupScheduleInput {
	now := time.Now().UTC().Add(-time.Minute)
	return BackupScheduleInput{RequestID: "daily-window", Enabled: true, StartMinuteUTC: now.Hour()*60 + now.Minute(), WindowMinutes: 60, Reason: "confirmed daily maintenance window", Confirmed: true}
}

// Each fixture owns an isolated schema. No production/default enterprise is
// configured, and the peer does not assert that any real archive was created.
func maintenanceFixture(t *testing.T) (*Store, string, MaintenanceWorker, BackupWorker, *backupPeer, RealmWorker) {
	t.Helper()
	s, token, _, catalog, peer, backup := backupFixture(t)
	realm := RealmWorker{Store: s, Enterprise: &realmPeer{}}
	if _, e := s.RequestRealm(t.Context(), "a", "resume-before-daily", "release-operator", "fixture ready", 2, true, true); e != nil {
		t.Fatal(e)
	}
	if done, e := realm.Once(t.Context()); e != nil || !done {
		t.Fatal(done, e)
	}
	ready := func(ctx context.Context, id, nonce string) (tenancy.Readiness, error) {
		r, e := deploymentReady(ctx, id, nonce)
		if e != nil {
			return r, e
		}
		var state string
		e = s.pool.QueryRow(ctx, `SELECT status,access_version FROM platform_tenants WHERE id=$1`, id).Scan(&state, &r.Realm.Version)
		r.Realm.Enabled = state == "active"
		r.Realm.SuspensionConfirmed = state == "suspended"
		return r, e
	}
	backup.Readiness = ready
	return s, token, MaintenanceWorker{Store: s, Agent: peer, Catalog: catalog, Readiness: ready}, backup, peer, realm
}

func setSchedule(t *testing.T, s *Store, token string) BackupSchedule {
	t.Helper()
	in := scheduleInput()
	out, e := s.SetBackupSchedule(t.Context(), "release-operator", token, "a", in)
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func scheduleRun(t *testing.T, s *Store, token string) {
	t.Helper()
	setSchedule(t, s, token)
	if n, e := s.ScheduleBackups(t.Context()); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}
func maintenanceJob(t *testing.T, s *Store) MaintenanceRun {
	t.Helper()
	var j MaintenanceRun
	e := s.pool.QueryRow(t.Context(), `SELECT id,tenant_id,actor_id,state,phase,window_end,pause_id,backup_id,resume_id,backup_result,error_code FROM platform_maintenance_runs WHERE tenant_id='a'`).Scan(&j.ID, &j.TenantID, &j.ActorID, &j.State, &j.Phase, &j.WindowEnd, &j.PauseID, &j.BackupID, &j.ResumeID, &j.BackupResult, &j.ErrorCode)
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func maintenanceStep(t *testing.T, w MaintenanceWorker) MaintenanceRun {
	t.Helper()
	if _, e := w.Store.pool.Exec(t.Context(), `UPDATE platform_maintenance_runs SET retry_at=now()`); e != nil {
		t.Fatal(e)
	}
	if done, e := w.Once(t.Context()); e != nil || !done {
		t.Fatal("maintenance step", done, e)
	}
	return maintenanceJob(t, w.Store)
}
func runRealm(t *testing.T, w RealmWorker) {
	t.Helper()
	realmRetry(t, w.Store)
	if done, e := w.Once(t.Context()); e != nil || !done {
		t.Fatal("realm step", done, e)
	}
}
func pauseDaily(t *testing.T, w MaintenanceWorker, rw RealmWorker) {
	t.Helper()
	if j := maintenanceStep(t, w); j.Phase != "pause" {
		t.Fatal(j)
	}
	if j := maintenanceStep(t, w); j.PauseID == "" || j.Phase != "pause" {
		t.Fatal(j)
	}
	runRealm(t, rw)
	if j := maintenanceStep(t, w); j.Phase != "backup" {
		t.Fatal(j)
	}
}
func finishDaily(t *testing.T, w MaintenanceWorker, rw RealmWorker, want string) {
	t.Helper()
	if j := maintenanceStep(t, w); j.ResumeID == "" || j.Phase != "resume" {
		t.Fatal(j)
	}
	runRealm(t, rw)
	if j := maintenanceStep(t, w); j.State != want || j.Phase != "finished" {
		t.Fatal(j)
	}
	var state string
	var version int64
	if e := w.Store.pool.QueryRow(t.Context(), `SELECT status,access_version FROM platform_tenants WHERE id='a'`).Scan(&state, &version); e != nil || state != "active" || version != 5 {
		t.Fatal(state, version, e)
	}
}

func TestPlatformPostgresBackupSchedule(t *testing.T) {
	s, token, _, _, _, _ := maintenanceFixture(t)
	ctx := t.Context()
	if n, e := s.ScheduleBackups(ctx); e != nil || n != 0 {
		t.Fatal("default must be disabled", n, e)
	}
	in := scheduleInput()
	first, e := s.SetBackupSchedule(ctx, "release-operator", token, "a", in)
	if e != nil || first.Version != 1 {
		t.Fatal(first, e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if again, e := s.SetBackupSchedule(ctx, "release-operator", token, "a", in); e != nil || again.Version != first.Version {
		t.Fatal("replay", again, e)
	}
	changed := in
	changed.Reason = "new input"
	if _, e = s.SetBackupSchedule(ctx, "release-operator", token, "a", changed); !errors.Is(e, ErrRequestChanged) {
		t.Fatal(e)
	}
	changed.RequestID = "stale"
	if _, e = s.SetBackupSchedule(ctx, "release-operator", token, "a", changed); !errors.Is(e, ErrMaintenanceChanged) {
		t.Fatal(e)
	}
	changed.ExpectedVersion = 1
	if next, e := s.SetBackupSchedule(ctx, "release-operator", token, "a", changed); e != nil || next.Version != 1 {
		t.Fatal("no-op", next, e)
	}
	var audits int
	if e = s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_audits WHERE action='backup.schedule.updated'`).Scan(&audits); e != nil || audits != 1 {
		t.Fatal(audits, e)
	}
	var created atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			n, err := s.ScheduleBackups(ctx)
			if err != nil {
				t.Error(err)
			}
			created.Add(int32(n))
		})
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatal("duplicate daily runs", created.Load())
	}
	api := &API{Store: s, Limiter: testLimit{}}
	for _, path := range []string{"/platform/admin/backup-schedules?q=a", "/platform/admin/maintenance?q=a"} {
		status, out := adminTestCall(t, api, "GET", path, token, "")
		if status != 200 || out["total"] != float64(1) {
			t.Fatal(path, status, out)
		}
		raw, _ := json.Marshal(out)
		for _, private := range []string{"hostFingerprint", "binding", "releaseDigest", "lease_id"} {
			if strings.Contains(string(raw), private) {
				t.Fatal("private data", private)
			}
		}
	}
}

func TestPlatformPostgresBackupScheduleValidation(t *testing.T) {
	for _, tc := range []string{"confirmation", "reason", "minute_low", "minute_high", "window_low", "window_high", "reader", "disabled", "expired", "unmanaged", "audit"} {
		t.Run(tc, func(t *testing.T) {
			s, token, _, _, _, _ := maintenanceFixture(t)
			in := scheduleInput()
			tenant := "a"
			switch tc {
			case "confirmation":
				in.Confirmed = false
			case "reason":
				in.Reason = ""
			case "minute_low":
				in.StartMinuteUTC = -1
			case "minute_high":
				in.StartMinuteUTC = 1440
			case "window_low":
				in.WindowMinutes = 14
			case "window_high":
				in.WindowMinutes = 181
			case "reader":
				_, _ = s.pool.Exec(t.Context(), `UPDATE platform_admin_accounts SET role='reader'`)
			case "disabled":
				_, _ = s.pool.Exec(t.Context(), `UPDATE platform_admin_accounts SET enabled=false`)
			case "expired":
				_, _ = s.pool.Exec(t.Context(), `UPDATE platform_admin_sessions SET expires_at=now()-interval '1 minute'`)
			case "unmanaged":
				tenant = "b"
			case "audit":
				if _, e := s.pool.Exec(t.Context(), `CREATE FUNCTION reject_schedule() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='backup.schedule.updated' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$;CREATE TRIGGER reject_schedule BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION reject_schedule()`); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := s.SetBackupSchedule(t.Context(), "release-operator", token, tenant, in); e == nil {
				t.Fatal("unsafe schedule")
			}
			var count int
			if e := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM platform_backup_schedules`).Scan(&count); e != nil || count != 0 {
				t.Fatal(count, e)
			}
		})
	}
}

func TestPlatformPostgresDailyMaintenance(t *testing.T) {
	for _, result := range []string{"completed", "failed", "cancelled"} {
		t.Run(result, func(t *testing.T) {
			s, token, w, bw, peer, rw := maintenanceFixture(t)
			scheduleRun(t, s, token)
			if _, e := s.RequestRealm(t.Context(), "a", "manual-pause", "release-operator", "other operator", 3, false, true); !errors.Is(e, ErrDeploymentMaintenance) {
				t.Fatal("manual pause", e)
			}
			pauseDaily(t, w, rw)
			if _, e := s.RequestRealm(t.Context(), "a", "manual-resume", "release-operator", "other operator", 4, true, true); !errors.Is(e, ErrDeploymentMaintenance) {
				t.Fatal("manual resume", e)
			}
			if j := maintenanceStep(t, w); j.BackupID == "" {
				t.Fatal(j)
			}
			peer.lose = "submit"
			backupStep(t, bw)
			backupStep(t, bw)
			// Disabling the schedule/author prevents NEW runs, but cannot strand an
			// already accepted pause. No stored admin session is used for recovery.
			_, e := s.pool.Exec(t.Context(), `UPDATE platform_backup_schedules SET enabled=false; UPDATE platform_admin_accounts SET enabled=false; UPDATE platform_maintenance_runs SET window_end=now()-interval '1 minute'`)
			if e != nil {
				t.Fatal(e)
			}
			peer.terminal(result)
			backupStep(t, bw)
			if j := maintenanceStep(t, w); j.Phase != "resume" || j.BackupResult != result {
				t.Fatal(j)
			}
			want := "failed"
			if result == "completed" {
				want = "completed"
			}
			finishDaily(t, w, rw, want)
			if n, e := s.ScheduleBackups(t.Context()); e != nil || n != 0 {
				t.Fatal("second run", n, e)
			}
		})
	}
}

func TestPlatformPostgresMaintenanceWindowExpiry(t *testing.T) {
	for _, phase := range []string{"prepare", "pause", "backup"} {
		t.Run(phase, func(t *testing.T) {
			s, token, w, _, peer, rw := maintenanceFixture(t)
			scheduleRun(t, s, token)
			if phase == "pause" {
				maintenanceStep(t, w)
			}
			if phase == "backup" {
				pauseDaily(t, w, rw)
			}
			if _, e := s.pool.Exec(t.Context(), `UPDATE platform_maintenance_runs SET window_end=now()-interval '1 second'`); e != nil {
				t.Fatal(e)
			}
			j := maintenanceStep(t, w)
			if phase == "backup" {
				if j.Phase != "resume" || j.BackupResult != "window_expired" {
					t.Fatal(j)
				}
				finishDaily(t, w, rw, "skipped")
			} else if j.State != "skipped" || j.PauseID != "" {
				t.Fatal(j)
			}
			if peer.calls["submit"] != 0 {
				t.Fatal("late capture")
			}
		})
	}
}

func TestPlatformPostgresMaintenanceSkipsUnavailable(t *testing.T) {
	for _, state := range []string{"suspended", "disabled", "reader", "outside_window"} {
		t.Run(state, func(t *testing.T) {
			s, token, _, _, _, rw := maintenanceFixture(t)
			setSchedule(t, s, token)
			if state == "suspended" {
				if _, e := s.RequestRealm(t.Context(), "a", "operator-pause", "release-operator", "separate maintenance", 3, false, true); e != nil {
					t.Fatal(e)
				}
				runRealm(t, rw)
			} else if state == "disabled" {
				_, _ = s.pool.Exec(t.Context(), `UPDATE platform_admin_accounts SET enabled=false`)
			} else if state == "reader" {
				_, _ = s.pool.Exec(t.Context(), `UPDATE platform_admin_accounts SET role='reader'`)
			} else {
				_, _ = s.pool.Exec(t.Context(), `UPDATE platform_backup_schedules SET start_minute_utc=(start_minute_utc+720)%1440`)
			}
			n, e := s.ScheduleBackups(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			if state == "suspended" {
				if n != 1 || maintenanceJob(t, s).State != "skipped" {
					t.Fatal("did not skip manual pause")
				}
			} else if n != 0 {
				t.Fatal("unauthorized/outside window", n)
			}
		})
	}
}

func TestPlatformPostgresMaintenanceUnconfirmedBackup(t *testing.T) {
	s, token, w, bw, peer, rw := maintenanceFixture(t)
	scheduleRun(t, s, token)
	pauseDaily(t, w, rw)
	j := maintenanceStep(t, w)
	backupStep(t, bw)
	peer.receipt.State = "unconfirmed"
	peer.receipt.Phase = "restoring_services"
	peer.receipt.Revision++
	peer.receipt.RecoveryErrorCode = "BACKUP_SERVICE_RECOVERY_UNCONFIRMED"
	backupStep(t, bw)
	for range 3 {
		if j := maintenanceStep(t, w); j.Phase != "backup" || j.ErrorCode != "MAINTENANCE_BACKUP_NEEDS_ATTENTION" {
			t.Fatal(j)
		}
	}
	if peer.calls["retry"] != 0 {
		t.Fatal("automatic unsafe retry")
	}
	if _, e := s.ControlBackup(t.Context(), "release-operator", token, j.BackupID, BackupAction{RequestID: "operator-repair", Action: "retry", ExpectedRevision: peer.receipt.Revision, Reason: "original failure repaired", Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	backupStep(t, bw)
	peer.terminal("completed")
	backupStep(t, bw)
	if j := maintenanceStep(t, w); j.Phase != "resume" {
		t.Fatal(j)
	}
	finishDaily(t, w, rw, "completed")
}

func TestPlatformPostgresMaintenanceProgressLost(t *testing.T) {
	for _, child := range []string{"pause", "backup", "resume"} {
		t.Run(child, func(t *testing.T) {
			s, token, w, bw, peer, rw := maintenanceFixture(t)
			scheduleRun(t, s, token)
			if child == "pause" {
				maintenanceStep(t, w)
			} else {
				pauseDaily(t, w, rw)
			}
			if child == "resume" {
				maintenanceStep(t, w)
				backupStep(t, bw)
				peer.terminal("completed")
				backupStep(t, bw)
				maintenanceStep(t, w)
			}
			// The child request commits, but updating its parent's progress fails.
			// Reclaimed work must discover the ORIGINAL child by immutable ID.
			if _, e := s.pool.Exec(t.Context(), `CREATE FUNCTION reject_progress() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture'; END $$;CREATE TRIGGER reject_progress BEFORE UPDATE ON platform_maintenance_runs FOR EACH ROW WHEN (OLD.lease_id IS NOT NULL AND NEW.lease_id IS NULL) EXECUTE FUNCTION reject_progress()`); e != nil {
				t.Fatal(e)
			}
			if _, e := s.pool.Exec(t.Context(), `UPDATE platform_maintenance_runs SET retry_at=now()`); e != nil {
				t.Fatal(e)
			}
			if done, e := w.Once(t.Context()); !done || e == nil {
				t.Fatal("progress failure not exercised", done, e)
			}
			j := maintenanceJob(t, s)
			if j.Phase != child || (child == "pause" && j.PauseID != "") || (child == "backup" && j.BackupID != "") || (child == "resume" && j.ResumeID != "") {
				t.Fatal(j)
			}
			if _, e := s.pool.Exec(t.Context(), `DROP TRIGGER reject_progress ON platform_maintenance_runs; UPDATE platform_maintenance_runs SET lease_until=now()-interval '1 second',window_end=now()-interval '1 second'`); e != nil {
				t.Fatal(e)
			}
			// Simulate a new process, expired window and no retained admin token.
			restarted := MaintenanceWorker{Store: s, Agent: peer, Catalog: w.Catalog, Readiness: w.Readiness}
			j = maintenanceStep(t, restarted)
			if (child == "pause" && j.PauseID == "") || (child == "backup" && j.BackupID == "") || (child == "resume" && j.ResumeID == "") {
				t.Fatal("lost child", j)
			}
			var count int
			if child == "backup" {
				if e := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM platform_backup_jobs`).Scan(&count); e != nil || count != 1 {
					t.Fatal(count, e)
				}
				backupStep(t, bw)
				peer.terminal("completed")
				backupStep(t, bw)
				maintenanceStep(t, restarted)
				finishDaily(t, restarted, rw, "completed")
			} else if child == "pause" {
				runRealm(t, rw)
				maintenanceStep(t, restarted)
				maintenanceStep(t, restarted)
				finishDaily(t, restarted, rw, "skipped")
			} else {
				runRealm(t, rw)
				if j := maintenanceStep(t, restarted); j.State != "completed" {
					t.Fatal(j)
				}
			}
			if e := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM platform_realm_jobs WHERE request_id LIKE 'maintenance-%'`).Scan(&count); e != nil || count != 2 {
				t.Fatal("duplicate realm children", count, e)
			}
		})
	}
}

func TestPlatformPostgresMaintenanceFences(t *testing.T) {
	for _, fault := range []string{"lease", "readiness", "generation", "binding", "pause_ownership", "audit"} {
		t.Run(fault, func(t *testing.T) {
			s, token, w, _, peer, rw := maintenanceFixture(t)
			scheduleRun(t, s, token)
			switch fault {
			case "lease":
				peer.afterInspect = func() {
					_, _ = s.pool.Exec(t.Context(), `UPDATE platform_maintenance_runs SET lease_id='another-worker'`)
				}
			case "readiness":
				w.Readiness = func(context.Context, string, string) (tenancy.Readiness, error) {
					return tenancy.Readiness{}, ErrUnavailable
				}
			case "generation":
				peer.status.Generation++
			case "binding":
				_, _ = s.pool.Exec(t.Context(), `UPDATE platform_servers SET revision=revision+1`)
			case "pause_ownership":
				pauseDaily(t, w, rw)
				_, _ = s.pool.Exec(t.Context(), `UPDATE platform_realm_jobs SET request_id='not-this-run' WHERE request_id LIKE 'maintenance-%-pause'`)
			case "audit":
				if _, e := s.pool.Exec(t.Context(), `CREATE FUNCTION reject_maintenance() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='maintenance.progress' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$;CREATE TRIGGER reject_maintenance BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION reject_maintenance()`); e != nil {
					t.Fatal(e)
				}
			}
			_, _ = s.pool.Exec(t.Context(), `UPDATE platform_maintenance_runs SET retry_at=now()`)
			_, _ = w.Once(t.Context())
			j := maintenanceJob(t, s)
			if j.State != "pending" || j.BackupID != "" || j.ResumeID != "" || fault != "pause_ownership" && j.Phase != "prepare" {
				t.Fatal("unsafe progress", j)
			}
			if peer.calls["submit"] != 0 {
				t.Fatal("captured without verified authority")
			}
			var txVersion int64
			if e := s.pool.QueryRow(t.Context(), `SELECT access_version FROM platform_tenants WHERE id='a'`).Scan(&txVersion); e != nil {
				t.Fatal(e)
			}
			if _, e := s.RequestRealm(t.Context(), "a", "unsafe-manual", "release-operator", "still owned by run", txVersion, true, true); !errors.Is(e, ErrDeploymentMaintenance) {
				t.Fatal("unsafe unlock", e)
			}
		})
	}
}

func TestPlatformPostgresMaintenanceHTTPPermissions(t *testing.T) {
	s, token, _, _, _, _ := maintenanceFixture(t)
	api := &API{Store: s, Limiter: testLimit{}}
	body, _ := json.Marshal(scheduleInput())
	for _, path := range []string{"/platform/admin/maintenance", "/platform/admin/backup-schedules"} {
		if status, _ := adminTestCall(t, api, "GET", path, "", ""); status != 401 {
			t.Fatal(path, status)
		}
	}
	if status, _ := adminTestCall(t, api, "PUT", "/platform/admin/backup-schedules/a", token, string(body)); status != 200 {
		t.Fatal("operator", status)
	}
	for _, role := range []string{"reader", "disabled", "expired"} {
		switch role {
		case "reader":
			_, _ = s.pool.Exec(t.Context(), `UPDATE platform_admin_accounts SET role='reader'`)
		case "disabled":
			_, _ = s.pool.Exec(t.Context(), `UPDATE platform_admin_accounts SET role='operator',enabled=false`)
		case "expired":
			_, _ = s.pool.Exec(t.Context(), `UPDATE platform_admin_accounts SET enabled=true;UPDATE platform_admin_sessions SET expires_at=now()-interval '1 second'`)
		}
		if status, _ := adminTestCall(t, api, "PUT", "/platform/admin/backup-schedules/a", token, string(body)); status != 401 {
			t.Fatal(role, status)
		}
		if role == "reader" {
			if status, _ := adminTestCall(t, api, "GET", "/platform/admin/backup-schedules", token, ""); status != 200 {
				t.Fatal("reader view", status)
			}
		}
	}
}

func TestPlatformPostgresMaintenanceTenantFailureIsolation(t *testing.T) {
	s, token, w, _, _, _ := maintenanceFixture(t)
	setSchedule(t, s, token)
	in := serverInput()
	in.TenantID = "b"
	in.ServerID = "server-b"
	in.RequestID = "register-b"
	if _, e := s.ManageServer(t.Context(), "release-operator", token, in, serverFixture("b", "b")); e != nil {
		t.Fatal(e)
	}
	release := w.Catalog["release-one"]
	release.TenantID = "b"
	release.ServerID = "server-b"
	raw, _ := json.Marshal(release)
	if _, e := s.pool.Exec(t.Context(), `INSERT INTO platform_deployment_jobs(id,actor_id,request_id,tenant_id,server_id,input,operation,release,binding,state,generation) VALUES('fixture-b','release-operator','fixture-b','b','server-b','{}','{}',$1,'{}','completed',1)`, raw); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetBackupSchedule(t.Context(), "release-operator", token, "b", BackupScheduleInput{RequestID: "daily-b", Enabled: true, StartMinuteUTC: scheduleInput().StartMinuteUTC, WindowMinutes: 60, Reason: "separate fixture window", Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.pool.Exec(t.Context(), `UPDATE platform_deployment_jobs SET release='{}' WHERE tenant_id='a'`); e != nil {
		t.Fatal(e)
	}
	if n, e := s.ScheduleBackups(t.Context()); n != 1 || !errors.Is(e, ErrMaintenanceChanged) {
		t.Fatal("first enterprise starved second", n, e)
	}
	var tenant string
	if e := s.pool.QueryRow(t.Context(), `SELECT tenant_id FROM platform_maintenance_runs`).Scan(&tenant); e != nil || tenant != "b" {
		t.Fatal(tenant, e)
	}
}
