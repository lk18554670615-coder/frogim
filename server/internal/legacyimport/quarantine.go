package legacyimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
)

type QuarantineRequest struct {
	ID, TenantID, Actor, Reason, ExpectedFingerprint string
	UserIDs                                          []string
	Confirmed                                        bool
}

// QuarantineInvalidPhones retires only explicitly reviewed, unbound legacy
// authentication identities. User rows, phone values and business relations
// remain intact. The existing retired state prevents login and import; the
// durable audit distinguishes quarantine from a transferred identity. Restoring
// access requires verified identity repair and a credential reset, never merely
// clearing a flag. This operation cannot activate or unban an account.
func QuarantineInvalidPhones(ctx context.Context, source, target *pgxpool.Pool, r QuarantineRequest) (int, error) {
	if source == nil || target == nil || !tenancy.ValidID(r.ID) || !tenancy.ValidID(r.TenantID) || !tenancy.ValidID(r.Actor) || !r.Confirmed || strings.TrimSpace(r.Reason) == "" || len([]rune(r.Reason)) > 500 || !validFingerprint(r.ExpectedFingerprint) || len(r.UserIDs) == 0 || len(r.UserIDs) > MaxRows {
		return 0, ErrConfig
	}
	r.UserIDs = append([]string{}, r.UserIDs...)
	sort.Strings(r.UserIDs)
	for i, id := range r.UserIDs {
		if !tenancy.ValidID(id) || (i > 0 && id == r.UserIDs[i-1]) {
			return 0, ErrConfig
		}
	}
	input, _ := json.Marshal(r)
	digest := sha256.Sum256(input)
	hash := hex.EncodeToString(digest[:])
	s, e := source.Begin(ctx)
	if e != nil {
		return 0, ErrImportUnavailable
	}
	defer s.Rollback(ctx)
	if _, e = s.Exec(ctx, `LOCK TABLE im_users IN SHARE ROW EXCLUSIVE MODE`); e != nil {
		return 0, ErrImportUnavailable
	}
	d, e := target.Begin(ctx)
	if e != nil {
		return 0, ErrImportUnavailable
	}
	defer d.Rollback(ctx)
	v, e := sourceMaintenance(ctx, s, r.TenantID, 0)
	if e != nil {
		return 0, e
	}
	if e = targetMaintenance(ctx, d, r.TenantID, v); e != nil {
		return 0, e
	}
	var suspension string
	var matching bool
	if s.QueryRow(ctx, `SELECT operation_id FROM im_tenant_realm_operations WHERE access_version=$1 AND NOT enabled AND state='completed'`, v).Scan(&suspension) != nil || d.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_realm_jobs WHERE id=$1 AND tenant_id=$2 AND access_version=$3 AND state='completed' AND NOT enabled)`, suspension, r.TenantID, v).Scan(&matching) != nil || !matching {
		return 0, ErrMaintenance
	}
	auditID := "aud_legacy_quarantine_" + r.ID
	var prior string
	e = s.QueryRow(ctx, `SELECT metadata->>'requestDigest' FROM im_audits WHERE id=$1 AND action='identity.legacy.quarantined'`, auditID).Scan(&prior)
	if e == nil {
		if prior != hash {
			return 0, ErrImportConflict
		}
		return len(r.UserIDs), nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return 0, ErrImportUnavailable
	}
	in := inventory{tenant: r.TenantID}
	if e = readSource(ctx, s, &in); e != nil {
		return 0, e
	}
	if e = readTarget(ctx, d, &in); e != nil {
		return 0, e
	}
	if in.sourceVersion != 79 || in.boundTenant != r.TenantID || in.targetVersion != platform.SchemaVersion {
		return 0, ErrSchema
	}
	if analyze(in, time.Now().UTC()).Fingerprint != r.ExpectedFingerprint {
		return 0, ErrImportConflict
	}
	users := map[string]sourceUser{}
	for _, u := range in.source {
		users[u.id] = u
	}
	for _, id := range r.UserIDs {
		u, ok := users[id]
		if !ok || u.deleted || u.state != "active" || u.platformID != "" || u.assignment != 1 || u.auth != 1 {
			return 0, ErrImportConflict
		}
		if _, e = platform.NormalizePhone(u.phone); e == nil {
			return 0, ErrImportConflict
		}
	}
	// Completed realm suspension already proved IM/RTC disconnection for all
	// users. Keep that proof mandatory; do not substitute local token deletion.
	if _, e = s.Exec(ctx, `UPDATE im_users SET local_identity_state='retired',banned=true,banned_until=NULL,password_hash='',password_updated_at=now(),updated_at=now() WHERE id=ANY($1::text[])`, r.UserIDs); e != nil {
		return 0, ErrImportUnavailable
	}
	meta, _ := json.Marshal(map[string]any{"requestDigest": hash, "inventoryFingerprint": r.ExpectedFingerprint, "userIds": r.UserIDs, "count": len(r.UserIDs), "reason": r.Reason, "realmVersion": v, "recoveryRequires": "verified-phone-and-credential-reset"})
	if _, e = s.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'identity.legacy.quarantined','tenant',$3,$4,now())`, auditID, r.Actor, r.TenantID, meta); e != nil {
		return 0, ErrImportUnavailable
	}
	if s.Commit(ctx) != nil {
		return 0, ErrImportUnavailable
	}
	return len(r.UserIDs), nil
}
