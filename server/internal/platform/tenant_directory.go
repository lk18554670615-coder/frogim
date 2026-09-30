package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
)

var (
	ErrTenantDirectoryChanged = errors.New("tenant directory changed")
	ErrTenantArchiveBlocked   = errors.New("tenant archive prerequisites not met")
	ErrTenantNotFound         = errors.New("tenant not found")
)

type tenantDirectoryInput struct {
	DisplayName              string `json:"displayName"`
	Note                     string `json:"note"`
	ExpectedDirectoryVersion int64  `json:"expectedDirectoryVersion"`
	Reason                   string `json:"reason"`
	Confirmed                bool   `json:"confirmed"`
}

type tenantDirectoryAction struct {
	ExpectedDirectoryVersion int64  `json:"expectedDirectoryVersion"`
	Reason                   string `json:"reason"`
	Confirmed                bool   `json:"confirmed"`
}

type defaultTenantInput struct {
	ExpectedCurrentDefaultID string `json:"expectedCurrentDefaultId"`
	ExpectedDirectoryVersion int64  `json:"expectedDirectoryVersion"`
	Reason                   string `json:"reason"`
	Confirmed                bool   `json:"confirmed"`
}

func (a *API) adminTenantDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !tenancy.ValidID(id) {
		failure(w, tenancy.ErrInvalid)
		return
	}
	var raw []byte
	err := a.Store.pool.QueryRow(r.Context(), `SELECT jsonb_build_object(
 'id',t.id,'displayName',t.display_name,'note',t.note,'httpBaseUrl',t.http_base_url,
 'status',t.status,'isDefault',t.is_default,'configVersion',t.config_version,
 'currentDefaultId',COALESCE((SELECT d.id FROM platform_tenants d WHERE d.is_default),''),
 'accessVersion',t.access_version,'directoryVersion',t.directory_version,
 'archivedAt',t.archived_at,'archivedBy',t.archived_by,'createdAt',t.created_at,'updatedAt',t.updated_at,
 'accountCount',(SELECT count(*) FROM platform_accounts a WHERE a.tenant_id=t.id),
 'enabledCodeCount',(SELECT count(*) FROM platform_enterprise_codes c WHERE c.tenant_id=t.id AND c.enabled),
 'serverCount',(SELECT count(*) FROM platform_servers s WHERE s.tenant_id=t.id),
 'pendingJobCount',(
   (SELECT count(*) FROM platform_jobs j WHERE (j.source_tenant_id=t.id OR j.target_tenant_id=t.id) AND j.step<>'completed')+
   (SELECT count(*) FROM platform_realm_jobs j WHERE j.tenant_id=t.id AND j.state<>'completed')+
   (SELECT count(*) FROM platform_access_jobs j JOIN platform_accounts a ON a.id=j.account_id WHERE COALESCE(j.tenant_id,a.tenant_id)=t.id AND j.state<>'completed')+
   (SELECT count(*) FROM platform_credential_jobs j WHERE j.tenant_id=t.id AND j.state<>'completed')+
   (SELECT count(*) FROM platform_deployment_jobs j WHERE j.tenant_id=t.id AND j.state<>'completed')+
   (SELECT count(*) FROM platform_backup_jobs j WHERE j.tenant_id=t.id AND j.state IN ('pending','unconfirmed'))+
   (SELECT count(*) FROM platform_maintenance_runs j WHERE j.tenant_id=t.id AND j.state='pending')+
   (SELECT count(*) FROM platform_legacy_import_batches j WHERE j.tenant_id=t.id AND j.state<>'completed')+
   (SELECT count(*) FROM platform_legacy_cutovers j WHERE j.tenant_id=t.id AND j.phase NOT IN ('completed','rolled_back'))),
 'maintenanceEnabled',COALESCE((SELECT s.enabled FROM platform_backup_schedules s WHERE s.tenant_id=t.id),false))
 FROM platform_tenants t WHERE t.id=$1`, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrTenantNotFound
	}
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, json.RawMessage(raw))
}

func (s *Store) updateTenantDirectory(ctx context.Context, id, actor string, in tenantDirectoryInput) error {
	name, note := strings.TrimSpace(in.DisplayName), strings.TrimSpace(in.Note)
	if !tenancy.ValidID(id) || actor == "" || !adminReason(in.Reason, in.Confirmed) ||
		in.ExpectedDirectoryVersion < 1 || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 120 || utf8.RuneCountInString(note) > 1000 {
		return tenancy.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var previousName, previousNote string
	var version int64
	err = tx.QueryRow(ctx, `SELECT display_name,note,directory_version FROM platform_tenants WHERE id=$1 FOR UPDATE`, id).Scan(&previousName, &previousNote, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTenantNotFound
	}
	if err != nil {
		return err
	}
	if version != in.ExpectedDirectoryVersion {
		return ErrTenantDirectoryChanged
	}
	if name == previousName && note == previousNote {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_tenants SET display_name=$2,note=$3,directory_version=directory_version+1,updated_at=now() WHERE id=$1`, id, name, note); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,reason,metadata)
 VALUES($1,'tenant.directory.updated',$2,$3,jsonb_build_object('beforeName',$4::text,'afterName',$5::text,'noteChanged',$6::boolean,'directoryVersion',$7::bigint))`, actor, id, in.Reason, previousName, name, previousNote != note, version+1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func tenantPendingWork(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	var pending bool
	err := tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM platform_jobs WHERE (source_tenant_id=$1 OR target_tenant_id=$1) AND step<>'completed') OR
 EXISTS(SELECT 1 FROM platform_realm_jobs WHERE tenant_id=$1 AND state<>'completed') OR
 EXISTS(SELECT 1 FROM platform_access_jobs j JOIN platform_accounts a ON a.id=j.account_id WHERE COALESCE(j.tenant_id,a.tenant_id)=$1 AND j.state<>'completed') OR
 EXISTS(SELECT 1 FROM platform_credential_jobs WHERE tenant_id=$1 AND state<>'completed') OR
 EXISTS(SELECT 1 FROM platform_deployment_jobs WHERE tenant_id=$1 AND state<>'completed') OR
 EXISTS(SELECT 1 FROM platform_backup_jobs WHERE tenant_id=$1 AND state IN ('pending','unconfirmed')) OR
 EXISTS(SELECT 1 FROM platform_maintenance_runs WHERE tenant_id=$1 AND state='pending') OR
 EXISTS(SELECT 1 FROM platform_legacy_import_batches WHERE tenant_id=$1 AND state<>'completed') OR
 EXISTS(SELECT 1 FROM platform_legacy_cutovers WHERE tenant_id=$1 AND phase NOT IN ('completed','rolled_back'))`, id).Scan(&pending)
	return pending, err
}

func (s *Store) archiveTenant(ctx context.Context, id, actor string, in tenantDirectoryAction, archive bool) error {
	if !tenancy.ValidID(id) || actor == "" || !adminReason(in.Reason, in.Confirmed) || in.ExpectedDirectoryVersion < 1 {
		return tenancy.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var state string
	var isDefault, archived bool
	var version int64
	err = tx.QueryRow(ctx, `SELECT status,is_default,archived_at IS NOT NULL,directory_version FROM platform_tenants WHERE id=$1 FOR UPDATE`, id).Scan(&state, &isDefault, &archived, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTenantNotFound
	}
	if err != nil {
		return err
	}
	if version != in.ExpectedDirectoryVersion {
		return ErrTenantDirectoryChanged
	}
	if archived == archive {
		return ErrTenantArchiveBlocked
	}
	var disabledSchedule, disabledCodes int64
	if archive {
		if isDefault || (state != "suspended" && state != "provisioning") {
			return ErrTenantArchiveBlocked
		}
		pending, e := tenantPendingWork(ctx, tx, id)
		if e != nil {
			return e
		}
		if pending {
			return ErrTenantArchiveBlocked
		}
		tag, e := tx.Exec(ctx, `UPDATE platform_backup_schedules SET enabled=false,version=version+1,actor_id=$2,reason=$3,updated_at=now() WHERE tenant_id=$1 AND enabled`, id, actor, in.Reason)
		if e != nil {
			return e
		}
		disabledSchedule = tag.RowsAffected()
		tag, e = tx.Exec(ctx, `UPDATE platform_enterprise_codes SET enabled=false WHERE tenant_id=$1 AND enabled`, id)
		if e != nil {
			return e
		}
		disabledCodes = tag.RowsAffected()
		_, err = tx.Exec(ctx, `UPDATE platform_tenants SET archived_at=now(),archived_by=$2,directory_version=directory_version+1,updated_at=now() WHERE id=$1`, id, actor)
	} else {
		_, err = tx.Exec(ctx, `UPDATE platform_tenants SET archived_at=NULL,archived_by=NULL,directory_version=directory_version+1,updated_at=now() WHERE id=$1`, id)
	}
	if err != nil {
		return err
	}
	action := "tenant.directory.unarchived"
	if archive {
		action = "tenant.directory.archived"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,reason,metadata)
 VALUES($1,$2,$3,$4,jsonb_build_object('status',$5::text,'directoryVersion',$6::bigint,'disabledSchedule',$7::bigint,'disabledCodes',$8::bigint,'restoreDoesNotReenable',true))`, actor, action, id, in.Reason, state, version+1, disabledSchedule, disabledCodes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) makeDefaultTenant(ctx context.Context, id, actor string, in defaultTenantInput) error {
	if !tenancy.ValidID(id) || actor == "" || !adminReason(in.Reason, in.Confirmed) || in.ExpectedDirectoryVersion < 1 ||
		(in.ExpectedCurrentDefaultID != "" && !tenancy.ValidID(in.ExpectedCurrentDefaultID)) {
		return tenancy.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// A single registry lock serializes default switches and avoids lock-order
	// inversions when two operators choose different targets concurrently.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(490739182)`); err != nil {
		return err
	}
	var current string
	err = tx.QueryRow(ctx, `SELECT id FROM platform_tenants WHERE is_default FOR UPDATE`).Scan(&current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if current != in.ExpectedCurrentDefaultID || current == id {
		return ErrTenantDirectoryChanged
	}
	var state string
	var archived bool
	var version int64
	err = tx.QueryRow(ctx, `SELECT status,archived_at IS NOT NULL,directory_version FROM platform_tenants WHERE id=$1 FOR UPDATE`, id).Scan(&state, &archived, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTenantNotFound
	}
	if err != nil {
		return err
	}
	if state != "active" || archived || version != in.ExpectedDirectoryVersion {
		return ErrTenantDirectoryChanged
	}
	if current != "" {
		if _, err = tx.Exec(ctx, `UPDATE platform_tenants SET is_default=false,directory_version=directory_version+1,updated_at=now() WHERE id=$1`, current); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_tenants SET is_default=true,directory_version=directory_version+1,updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,reason,metadata)
 VALUES($1,'tenant.default.changed',$2,$3,jsonb_build_object('previousDefaultId',$4::text,'directoryVersion',$5::bigint,'affects','future-registration-without-enterprise-code'))`, actor, id, in.Reason, current, version+1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *API) adminUpdateTenant(w http.ResponseWriter, r *http.Request) {
	var in tenantDirectoryInput
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if err := a.Store.updateTenantDirectory(r.Context(), r.PathValue("id"), actorID(r), in); err != nil {
		failure(w, err)
		return
	}
	a.adminTenantDetail(w, r)
}

func (a *API) adminArchiveTenant(w http.ResponseWriter, r *http.Request) {
	a.tenantArchiveAction(w, r, true)
}

func (a *API) adminUnarchiveTenant(w http.ResponseWriter, r *http.Request) {
	a.tenantArchiveAction(w, r, false)
}

func (a *API) tenantArchiveAction(w http.ResponseWriter, r *http.Request, archive bool) {
	var in tenantDirectoryAction
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if err := a.Store.archiveTenant(r.Context(), r.PathValue("id"), actorID(r), in, archive); err != nil {
		failure(w, err)
		return
	}
	a.adminTenantDetail(w, r)
}

func (a *API) adminMakeDefaultTenant(w http.ResponseWriter, r *http.Request) {
	var in defaultTenantInput
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if err := a.Store.makeDefaultTenant(r.Context(), r.PathValue("id"), actorID(r), in); err != nil {
		failure(w, err)
		return
	}
	a.adminTenantDetail(w, r)
}
