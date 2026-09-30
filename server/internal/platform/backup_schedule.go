package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

type BackupSchedule struct {
	TenantID       string    `json:"tenantId"`
	Enabled        bool      `json:"enabled"`
	StartMinuteUTC int       `json:"startMinuteUtc"`
	WindowMinutes  int       `json:"windowMinutes"`
	Version        int64     `json:"version"`
	ActorID        string    `json:"actorId"`
	UpdatedAt      time.Time `json:"updatedAt"`
}
type BackupScheduleInput struct {
	RequestID       string `json:"requestId"`
	Enabled         bool   `json:"enabled"`
	StartMinuteUTC  int    `json:"startMinuteUtc"`
	WindowMinutes   int    `json:"windowMinutes"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Reason          string `json:"reason"`
	Confirmed       bool   `json:"confirmed"`
}

const scheduleColumns = `tenant_id,enabled,start_minute_utc,window_minutes,version,actor_id,updated_at`

func scanSchedule(row pgx.Row) (BackupSchedule, error) {
	var s BackupSchedule
	e := row.Scan(&s.TenantID, &s.Enabled, &s.StartMinuteUTC, &s.WindowMinutes, &s.Version, &s.ActorID, &s.UpdatedAt)
	return s, e
}

func (s *Store) SetBackupSchedule(ctx context.Context, actor, token, tenant string, in BackupScheduleInput) (BackupSchedule, error) {
	var empty BackupSchedule
	in.Reason = strings.TrimSpace(in.Reason)
	if !tenancy.ValidID(actor) || !tenancy.ValidID(tenant) || !tenancy.ValidID(in.RequestID) || in.ExpectedVersion < 0 || in.StartMinuteUTC < 0 || in.StartMinuteUTC >= 1440 || in.WindowMinutes < 15 || in.WindowMinutes > 180 || !adminReason(in.Reason, in.Confirmed) {
		return empty, tenancy.ErrInvalid
	}
	raw, _ := json.Marshal(in)
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return empty, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,147))`, actor+":"+in.RequestID); e != nil {
		return empty, e
	}
	if e = serverOperator(ctx, tx, actor, token); e != nil {
		return empty, e
	}
	var priorTenant string
	var same bool
	var snapshot []byte
	e = tx.QueryRow(ctx, `SELECT tenant_id,input=$3::jsonb,snapshot FROM platform_backup_schedule_operations WHERE actor_id=$1 AND request_id=$2`, actor, in.RequestID, raw).Scan(&priorTenant, &same, &snapshot)
	if e == nil {
		if priorTenant != tenant || !same {
			return empty, ErrRequestChanged
		}
		e = json.Unmarshal(snapshot, &empty)
		return empty, e
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return empty, e
	}
	var status string
	var archived bool
	if e = tx.QueryRow(ctx, `SELECT status,archived_at IS NOT NULL FROM platform_tenants WHERE id=$1 FOR UPDATE`, tenant).Scan(&status, &archived); e != nil {
		return empty, e
	}
	if archived {
		return empty, ErrTenantArchiveBlocked
	}
	before, e := scanSchedule(tx.QueryRow(ctx, `SELECT `+scheduleColumns+` FROM platform_backup_schedules WHERE tenant_id=$1 FOR UPDATE`, tenant))
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return empty, e
	}
	if before.Version != in.ExpectedVersion {
		return empty, ErrMaintenanceChanged
	}
	// Enabling a schedule does not adopt an unmanaged/default server or grant
	// an agent execution access. A verified deployment must already exist.
	if in.Enabled {
		var managed bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_servers s JOIN platform_deployment_jobs j ON j.server_id=s.id AND j.tenant_id=s.tenant_id WHERE s.tenant_id=$1 AND j.state='completed')`, tenant).Scan(&managed); e != nil {
			return empty, e
		}
		if !managed {
			return empty, ErrDeploymentMaintenance
		}
	}
	next := before
	if before.Version == 0 || before.Enabled != in.Enabled || before.StartMinuteUTC != in.StartMinuteUTC || before.WindowMinutes != in.WindowMinutes || before.ActorID != actor {
		next, e = scanSchedule(tx.QueryRow(ctx, `INSERT INTO platform_backup_schedules(tenant_id,enabled,start_minute_utc,window_minutes,version,actor_id,reason) VALUES($1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT(tenant_id) DO UPDATE SET enabled=excluded.enabled,start_minute_utc=excluded.start_minute_utc,window_minutes=excluded.window_minutes,version=excluded.version,actor_id=excluded.actor_id,reason=excluded.reason,updated_at=now() RETURNING `+scheduleColumns, tenant, in.Enabled, in.StartMinuteUTC, in.WindowMinutes, before.Version+1, actor, in.Reason))
		if e != nil {
			return empty, e
		}
		oldJSON, _ := json.Marshal(before)
		newJSON, _ := json.Marshal(next)
		if _, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,reason,metadata) VALUES($1,'backup.schedule.updated',$2,$3,jsonb_build_object('before',$4::jsonb,'after',$5::jsonb,'inProgressRunsContinue',true))`, actor, tenant, in.Reason, oldJSON, newJSON); e != nil {
			return empty, e
		}
	}
	out, _ := json.Marshal(next)
	if _, e = tx.Exec(ctx, `INSERT INTO platform_backup_schedule_operations(actor_id,request_id,tenant_id,input,snapshot) VALUES($1,$2,$3,$4,$5)`, actor, in.RequestID, tenant, raw, out); e != nil {
		return empty, e
	}
	return next, tx.Commit(ctx)
}

func maintenanceWindow(now time.Time, minute, duration int) (time.Time, time.Time, bool) {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(time.Duration(minute) * time.Minute)
	if now.Before(start) {
		start = start.AddDate(0, 0, -1)
	}
	end := start.Add(time.Duration(duration) * time.Minute)
	return start, end, !now.Before(start) && now.Before(end)
}

// Create a single audited run for each UTC slot date. SQL row fences, not a
// process-local cron lock, serialize replicas and operator configuration changes.
func (s *Store) ScheduleBackups(ctx context.Context) (int, error) {
	rows, e := s.pool.Query(ctx, `SELECT tenant_id,actor_id FROM platform_backup_schedules WHERE enabled ORDER BY tenant_id`)
	if e != nil {
		return 0, e
	}
	type candidate struct{ tenant, actor string }
	var candidates []candidate
	for rows.Next() {
		var v candidate
		if e = rows.Scan(&v.tenant, &v.actor); e != nil {
			break
		}
		candidates = append(candidates, v)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return 0, e
	}
	n := 0
	var failures error
	for _, v := range candidates {
		created, e := s.startMaintenance(ctx, v.tenant, v.actor)
		if e != nil {
			// A broken release/configuration for one enterprise must not starve
			// later enterprises in the same daily window.
			failures = errors.Join(failures, e)
			continue
		}
		if created {
			n++
		}
	}
	return n, failures
}

func (s *Store) startMaintenance(ctx context.Context, tenant, actor string) (bool, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return false, e
	}
	defer tx.Rollback(ctx)
	var role string
	e = tx.QueryRow(ctx, `SELECT role FROM platform_admin_accounts WHERE id=$1 AND enabled FOR SHARE`, actor).Scan(&role)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && role != "operator" {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var b deploymentBinding
	b.TenantID = tenant
	if e = tx.QueryRow(ctx, `SELECT http_base_url,status,config_version,access_version FROM platform_tenants WHERE id=$1 FOR UPDATE`, tenant).Scan(&b.Address, &b.State, &b.Config, &b.Access); e != nil {
		return false, e
	}
	policy, e := scanSchedule(tx.QueryRow(ctx, `SELECT `+scheduleColumns+` FROM platform_backup_schedules WHERE tenant_id=$1 FOR SHARE`, tenant))
	if e != nil {
		return false, e
	}
	if !policy.Enabled || policy.ActorID != actor {
		return false, nil
	}
	var now time.Time
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return false, e
	}
	start, end, due := maintenanceWindow(now, policy.StartMinuteUTC, policy.WindowMinutes)
	if !due {
		return false, nil
	}
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_maintenance_runs WHERE tenant_id=$1 AND slot_date=$2)`, tenant, start.Format("2006-01-02")).Scan(&exists); e != nil {
		return false, e
	}
	if exists {
		return false, nil
	}
	// Another maintenance/upgrade/import may own the tenant. Never take over
	// its pause or silently resume it. Retry within this window, not afterwards.
	if e = noPendingDeployment(ctx, tx, tenant); errors.Is(e, ErrDeploymentMaintenance) {
		return false, nil
	} else if e != nil {
		return false, e
	}
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_legacy_import_batches WHERE tenant_id=$1 AND state<>'completed') OR EXISTS(SELECT 1 FROM platform_realm_jobs WHERE tenant_id=$1 AND state<>'completed')`, tenant).Scan(&exists); e != nil {
		return false, e
	}
	if exists {
		return false, nil
	}
	e = tx.QueryRow(ctx, `SELECT id,host_fingerprint,runtime,isolation_mode,revision FROM platform_servers WHERE tenant_id=$1 FOR SHARE`, tenant).Scan(&b.ServerID, &b.Host, &b.Runtime, &b.Mode, &b.Revision)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var releaseRaw []byte
	var generation int64
	e = tx.QueryRow(ctx, `SELECT release,generation FROM platform_deployment_jobs WHERE server_id=$1 AND tenant_id=$2 AND state='completed' ORDER BY generation DESC LIMIT 1`, b.ServerID, tenant).Scan(&releaseRaw, &generation)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var release deployment.Release
	if json.Unmarshal(releaseRaw, &release) != nil || !release.Valid() || !release.MatchesTarget(tenant, b.ServerID) || generation < 1 {
		return false, ErrMaintenanceChanged
	}
	state, phase, code := "pending", "prepare", ""
	if b.State != "active" {
		state, phase, code = "skipped", "finished", "MAINTENANCE_TENANT_NOT_ACTIVE"
	}
	var entropy [16]byte
	if _, e = rand.Read(entropy[:]); e != nil {
		return false, e
	}
	id := "maintenance-" + hex.EncodeToString(entropy[:])
	bindingRaw, _ := json.Marshal(b)
	if _, e = tx.Exec(ctx, `INSERT INTO platform_maintenance_runs(id,tenant_id,actor_id,schedule_version,slot_date,window_end,state,phase,binding,release,generation,error_code) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, tenant, actor, policy.Version, start.Format("2006-01-02"), end, state, phase, bindingRaw, releaseRaw, generation, code); e != nil {
		return false, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,metadata) VALUES($1,'maintenance.scheduled',$2,$3,jsonb_build_object('scheduleVersion',$4::bigint,'state',$5::text,'windowEnd',$6::timestamptz,'accessVersion',$7::bigint))`, actor, tenant, id, policy.Version, state, end, b.Access); e != nil {
		return false, e
	}
	return true, tx.Commit(ctx)
}
