package legacyimport

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/clientversion"
	"github.com/linli/im/server/internal/tenancy"
)

// Cutover coordinates manual infrastructure operations with the existing
// adopter/importer. Original database/volume copies are never modified here.
// Evidence files are independently retained by the operator; receipts contain
// only digests. No acknowledgement claims to execute a firewall or DNS change.
type CutoverRequest struct {
	ID               string            `json:"id"`
	TenantID         string            `json:"tenantId"`
	BatchID          string            `json:"batchId"`
	Actor            string            `json:"actor"`
	Reason           string            `json:"reason"`
	OriginalDatabase string            `json:"originalDatabase"` // explicit cluster-id/database, no credentials
	AdoptedDatabase  string            `json:"adoptedDatabase"`
	OldVersions      map[string]string `json:"oldVersions"`
}
type CutoverStep struct {
	ExpectedPhase  string `json:"expectedPhase"`
	Phase          string `json:"phase"`
	Reason         string `json:"reason"`
	EvidenceSHA256 string `json:"evidenceSha256"`
	Confirmed      bool   `json:"confirmed"`
}
type CutoverStatus struct {
	ID       string                    `json:"id"`
	Phase    string                    `json:"phase"`
	Receipts map[string]CutoverReceipt `json:"receipts"`
}
type CutoverReceipt struct {
	CutoverStep
	Actor      string    `json:"actor"`
	RecordedAt time.Time `json:"recordedAt"`
}

func (r CutoverRequest) valid() bool {
	if !tenancy.ValidID(r.ID) || !tenancy.ValidID(r.TenantID) || !tenancy.ValidID(r.BatchID) || !tenancy.ValidID(r.Actor) || strings.TrimSpace(r.Reason) == "" || len(r.Reason) > 1000 || r.OriginalDatabase == r.AdoptedDatabase || !validDatabaseIdentity(r.OriginalDatabase) || !validDatabaseIdentity(r.AdoptedDatabase) || len(r.OldVersions) != 3 {
		return false
	}
	for _, p := range []string{"android", "ios", "web"} {
		if _, ok := clientversion.Parse(r.OldVersions[p]); !ok {
			return false
		}
	}
	return true
}
func validDatabaseIdentity(v string) bool {
	parts := strings.Split(v, "/")
	if len(parts) != 2 || !tenancy.ValidID(parts[1]) || len(parts[0]) < 1 || len(parts[0]) > 24 {
		return false
	}
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func DatabaseIdentity(ctx context.Context, p *pgxpool.Pool) (string, error) {
	var id string
	if p.QueryRow(ctx, `SELECT system_identifier::text||'/'||current_database() FROM pg_control_system()`).Scan(&id) != nil || !validDatabaseIdentity(id) {
		return "", ErrRead
	}
	return id, nil
}

func StartCutover(ctx context.Context, p *pgxpool.Pool, r CutoverRequest, confirmed bool) (CutoverStatus, error) {
	var out CutoverStatus
	if !r.valid() || !confirmed {
		return out, ErrConfig
	}
	tx, e := p.Begin(ctx)
	if e != nil {
		return out, ErrImportUnavailable
	}
	defer tx.Rollback(ctx)
	var allowed bool
	if tx.QueryRow(ctx, `SELECT is_default AND status IN ('provisioning','suspended') FROM platform_tenants WHERE id=$1 FOR UPDATE`, r.TenantID).Scan(&allowed) != nil || !allowed {
		return out, ErrMaintenance
	}
	raw, _ := json.Marshal(r)
	tag, e := tx.Exec(ctx, `INSERT INTO platform_legacy_cutovers(id,tenant_id,input,phase) VALUES($1,$2,$3,'planned') ON CONFLICT DO NOTHING`, r.ID, r.TenantID, raw)
	if e != nil {
		return out, ErrImportUnavailable
	}
	var same bool
	if tx.QueryRow(ctx, `SELECT input=$2::jsonb FROM platform_legacy_cutovers WHERE id=$1`, r.ID, raw).Scan(&same) != nil || !same {
		return out, ErrImportConflict
	}
	if tag.RowsAffected() == 1 {
		if _, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,reason,metadata) VALUES($1,'legacy.cutover.planned',$2,$3,$4,jsonb_build_object('batchId',$5::text))`, r.Actor, r.TenantID, r.ID, r.Reason, r.BatchID); e != nil {
			return out, ErrImportUnavailable
		}
	}
	if tx.Commit(ctx) != nil {
		return out, ErrImportUnavailable
	}
	return ReadCutover(ctx, p, r)
}

func ReadCutover(ctx context.Context, p *pgxpool.Pool, r CutoverRequest) (CutoverStatus, error) {
	out := CutoverStatus{ID: r.ID}
	var raw []byte
	var same bool
	input, _ := json.Marshal(r)
	if !r.valid() || p.QueryRow(ctx, `SELECT input=$2::jsonb,phase,receipts FROM platform_legacy_cutovers WHERE id=$1`, r.ID, input).Scan(&same, &out.Phase, &raw) != nil || !same || json.Unmarshal(raw, &out.Receipts) != nil {
		return CutoverStatus{}, ErrImportConflict
	}
	return out, nil
}

func validCutoverTransition(from, to string) bool {
	if to == "rolled_back" {
		return from != "opening" && from != "completed" && from != "rolled_back" && from != ""
	}
	return map[string]string{"planned": "stopped", "stopped": "backed_up", "backed_up": "adopted", "adopted": "imported", "imported": "routed", "routed": "opening", "opening": "completed"}[from] == to
}

func AdvanceCutover(ctx context.Context, source, target *pgxpool.Pool, r CutoverRequest, step CutoverStep) (CutoverStatus, error) {
	var out CutoverStatus
	if !r.valid() || !step.Confirmed || !validFingerprint(step.EvidenceSHA256) || strings.TrimSpace(step.Reason) == "" || len(step.Reason) > 1000 || !validCutoverTransition(step.ExpectedPhase, step.Phase) {
		return out, ErrConfig
	}
	// Serialize with realm resumption on the tenant row, not just the ledger.
	tx, e := target.Begin(ctx)
	if e != nil {
		return out, ErrImportUnavailable
	}
	defer tx.Rollback(ctx)
	var state string
	var realm int64
	if tx.QueryRow(ctx, `SELECT status,access_version FROM platform_tenants WHERE id=$1 AND is_default FOR UPDATE`, r.TenantID).Scan(&state, &realm) != nil {
		return out, ErrMaintenance
	}
	var phase string
	var raw []byte
	var same bool
	input, _ := json.Marshal(r)
	if tx.QueryRow(ctx, `SELECT input=$2::jsonb,phase,receipts FROM platform_legacy_cutovers WHERE id=$1 FOR UPDATE`, r.ID, input).Scan(&same, &phase, &raw) != nil || !same {
		return out, ErrImportConflict
	}
	receipts := map[string]CutoverReceipt{}
	if json.Unmarshal(raw, &receipts) != nil {
		return out, ErrImportUnavailable
	}
	if receipt, ok := receipts[step.Phase]; ok {
		if receipt.CutoverStep != step || receipt.Actor != r.Actor {
			return out, ErrImportConflict
		}
		return CutoverStatus{r.ID, phase, receipts}, nil
	}
	if phase != step.ExpectedPhase || (state != "suspended" && state != "provisioning" && step.Phase != "completed") {
		return out, ErrMaintenance
	}
	if step.Phase == "completed" {
		if state != "active" {
			return out, ErrMaintenance
		}
		var settled bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_realm_jobs WHERE tenant_id=$1 AND access_version=$2 AND enabled AND state='completed')`, r.TenantID, realm).Scan(&settled) != nil || !settled {
			return out, ErrMaintenance
		}
	} else if step.Phase == "adopted" || step.Phase == "imported" || step.Phase == "routed" || step.Phase == "opening" {
		if source == nil {
			return out, ErrConfig
		}
		identity, e := DatabaseIdentity(ctx, source)
		if e != nil || identity != r.AdoptedDatabase {
			return out, ErrRealm
		}
		s, e := source.Begin(ctx)
		if e != nil {
			return out, ErrImportUnavailable
		}
		defer s.Rollback(ctx)
		if _, e = sourceMaintenance(ctx, s, r.TenantID, realm); e != nil {
			return out, e
		}
		if e = targetMaintenance(ctx, tx, r.TenantID, realm); e != nil {
			return out, e
		}
		if step.Phase != "adopted" {
			var complete bool
			if tx.QueryRow(ctx, `SELECT state='completed' AND tenant_id=$2 AND NOT EXISTS(SELECT 1 FROM platform_legacy_import_items WHERE batch_id=$1 AND step<>'completed') FROM platform_legacy_import_batches WHERE id=$1`, r.BatchID, r.TenantID).Scan(&complete) != nil || !complete {
				return out, ErrImportUnavailable
			}
			var unlinked bool
			if s.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE deleted_at IS NULL AND local_identity_state<>'retired' AND COALESCE(platform_account_id,'')='')`).Scan(&unlinked) != nil || unlinked {
				return out, ErrImportUnavailable
			}
		}
		if s.Commit(ctx) != nil {
			return out, ErrImportUnavailable
		}
	}
	if step.Phase == "routed" || step.Phase == "opening" {
		for _, os := range []string{"android", "ios", "web"} {
			var enabled bool
			var body []byte
			if tx.QueryRow(ctx, `SELECT enabled,policy FROM platform_client_version_policies WHERE platform=$1 FOR SHARE`, os).Scan(&enabled, &body) != nil || !enabled {
				return out, ErrMaintenance
			}
			var policy clientversion.Policy
			if json.Unmarshal(body, &policy) != nil {
				return out, ErrMaintenance
			}
			d, e := clientversion.Evaluate(os, r.OldVersions[os], "migration-check", &policy)
			if e != nil || !d.ForceUpdate || d.DownloadURL == "" {
				return out, ErrMaintenance
			}
		}
	}
	// Rollback only records a safe stopped candidate. Reopening the preserved
	// original stack is a separate manual act; credentials are never re-enabled.
	if step.Phase == "rolled_back" {
		if _, e = tx.Exec(ctx, `UPDATE platform_tenants SET status='suspended' WHERE id=$1;`, r.TenantID); e != nil {
			return out, ErrImportUnavailable
		}
	}
	receipts[step.Phase] = CutoverReceipt{step, r.Actor, time.Now().UTC()}
	encoded, _ := json.Marshal(receipts)
	if _, e = tx.Exec(ctx, `UPDATE platform_legacy_cutovers SET phase=$2,receipts=$3,updated_at=clock_timestamp() WHERE id=$1`, r.ID, step.Phase, encoded); e != nil {
		return out, ErrImportUnavailable
	}
	if _, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,reason,metadata) VALUES($1,'legacy.cutover.advanced',$2,$3,$4,jsonb_build_object('from',$5::text,'to',$6::text,'evidenceSha256',$7::text))`, r.Actor, r.TenantID, r.ID, step.Reason, phase, step.Phase, step.EvidenceSHA256); e != nil || tx.Commit(ctx) != nil {
		return out, ErrImportUnavailable
	}
	return CutoverStatus{r.ID, step.Phase, receipts}, nil
}
