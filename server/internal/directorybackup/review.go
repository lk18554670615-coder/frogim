package directorybackup

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/tenancy"
)

var ErrReviewChanged = errors.New("recovery evidence changed; retain quarantine and create a new review")
var reviewDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type ReviewPeer struct {
	TenantID     string `json:"tenantId"`
	HTTPBaseURL  string `json:"httpBaseUrl"`
	ControlURL   string `json:"controlUrl"`
	RealmVersion int64  `json:"realmVersion"`
}
type ReviewRequest struct {
	Request
	ReviewID  string       `json:"reviewId"`
	Manifest  backup.File  `json:"manifest"`
	Peers     []ReviewPeer `json:"peers"`
	Confirmed bool         `json:"confirmed"`
}

func (r ReviewRequest) valid() bool {
	if !r.Request.valid() || !tenancy.ValidID(r.ReviewID) || !r.Confirmed || r.Manifest.Name != "manifest" || !reviewDigest.MatchString(r.Manifest.SHA256) || r.Manifest.Size <= 0 || len(r.Peers) == 0 || len(r.Peers) > 100 {
		return false
	}
	previous := ""
	for _, p := range r.Peers {
		if !tenancy.ValidID(p.TenantID) || p.TenantID <= previous || tenancy.ValidateBaseURL(p.HTTPBaseURL, false) != nil || tenancy.ValidateBaseURL(p.ControlURL, false) != nil || p.RealmVersion < 1 {
			return false
		}
		previous = p.TenantID
	}
	return true
}

type ReviewResult struct {
	ReviewID          string        `json:"reviewId"`
	State             string        `json:"state"`
	ObservedAt        time.Time     `json:"observedAt"`
	SourceDigest      string        `json:"sourceDigest"`
	EvidenceDigest    string        `json:"evidenceDigest"`
	PeerCount         int           `json:"peerCount"`
	IdentityCount     int           `json:"identityCount"`
	Issues            []ReviewIssue `json:"issues"`
	ActivationAllowed bool          `json:"activationAllowed"`
}
type InventoryReader func(context.Context, ReviewPeer) (tenancy.RecoveryInventory, string, error)

const reviewSchema = `CREATE TABLE IF NOT EXISTS frogim_recovery.reviews (
 id text PRIMARY KEY,input jsonb NOT NULL,source jsonb NOT NULL,source_digest text NOT NULL,
 state text NOT NULL CHECK(state IN ('collecting','completed')),
 evidence jsonb,result jsonb,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),completed_at timestamptz
); REVOKE ALL ON frogim_recovery.reviews FROM PUBLIC;`

func checkReviewGuard(ctx context.Context, conn *pgx.Conn, r ReviewRequest) error {
	binding, _ := json.Marshal(r.Expected)
	var matches bool
	var phase string
	e := conn.QueryRow(ctx, `SELECT binding=$1::jsonb AND manifest_sha256=$2 AND manifest_size=$3,phase FROM frogim_recovery.guard WHERE singleton`, binding, r.Manifest.SHA256, r.Manifest.Size).Scan(&matches, &phase)
	if e != nil || !matches {
		return ErrQuarantined
	}
	if phase == "staged" {
		return checkRestored(ctx, conn, r.Request)
	}
	if phase != "activated" {
		return ErrQuarantined
	}
	return nil
}

// Review writes evidence/audit only, NEVER changes account authority or removes
// the restore guard. The private reader must authenticate each selected peer.
// Completion is a point-in-time report, not a cross-server activation permit.
func (d Database) Review(ctx context.Context, r ReviewRequest, read InventoryReader) (ReviewResult, error) {
	var empty ReviewResult
	if !r.valid() || read == nil {
		return empty, ErrInvalid
	}
	conn, e := d.connect(ctx)
	if e != nil {
		return empty, e
	}
	defer conn.Close(context.Background())
	var locked bool
	if e = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, tenancy.PlatformMigrationLock).Scan(&locked); e != nil || !locked {
		return empty, ErrUnconfirmed
	}
	if e = checkReviewGuard(ctx, conn, r); e != nil {
		return empty, e
	}
	source, e := loadReviewSource(ctx, conn, r.Expected.SchemaVersion)
	if e != nil {
		return empty, e
	}
	input, _ := json.Marshal(r)
	rawSource, _ := json.Marshal(source)
	sourceDigest := reviewHash(source)
	tx, e := conn.Begin(ctx)
	if e != nil {
		return empty, ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, reviewSchema); e != nil {
		return empty, ErrUnconfirmed
	}
	inserted, e := tx.Exec(ctx, `INSERT INTO frogim_recovery.reviews(id,input,source,source_digest,state) VALUES($1,$2,$3,$4,'collecting') ON CONFLICT DO NOTHING`, r.ReviewID, input, rawSource, sourceDigest)
	if e != nil {
		return empty, ErrUnconfirmed
	}
	var same bool
	var state string
	var previous []byte
	if e = tx.QueryRow(ctx, `SELECT input=$2::jsonb AND source_digest=$3,state,result FROM frogim_recovery.reviews WHERE id=$1 FOR UPDATE`, r.ReviewID, input, sourceDigest).Scan(&same, &state, &previous); e != nil || !same {
		return empty, ErrReviewChanged
	}
	if inserted.RowsAffected() == 1 {
		meta, _ := json.Marshal(map[string]any{"sourceDigest": sourceDigest, "peerCount": len(r.Peers), "backupId": r.Expected.BackupID})
		if _, e = tx.Exec(ctx, `INSERT INTO public.platform_audits(actor_id,action,job_id,reason,metadata) VALUES($1,'platform.recovery.review_started',$2,$3,$4)`, r.Actor, r.ReviewID, r.Reason, meta); e != nil {
			return empty, ErrUnconfirmed
		}
	}
	if tx.Commit(ctx) != nil {
		return empty, ErrUnconfirmed
	}
	evidence := []ReviewEvidence{}
	count := 0
	e = watched(ctx, conn, func(work context.Context) error {
		for _, p := range r.Peers {
			inventory, digest, e := read(work, p)
			if e != nil {
				return ErrUnconfirmed
			}
			actual, e := inventory.Digest()
			if e != nil || actual != digest || inventory.TenantID != p.TenantID || inventory.HTTPBaseURL != p.HTTPBaseURL || inventory.Realm.Version != p.RealmVersion {
				return ErrUnconfirmed
			}
			count += len(inventory.Users)
			if count > tenancy.RecoveryInventoryMaximum {
				return ErrUnconfirmed
			}
			evidence = append(evidence, ReviewEvidence{inventory, digest})
		}
		return nil
	})
	if e != nil {
		return empty, ErrUnconfirmed
	}
	if checkReviewGuard(ctx, conn, r) != nil {
		return empty, ErrQuarantined
	}
	current, e := loadReviewSource(ctx, conn, r.Expected.SchemaVersion)
	if e != nil || reviewHash(current) != sourceDigest {
		return empty, ErrReviewChanged
	}
	result := ReviewResult{ReviewID: r.ReviewID, State: "completed", ObservedAt: time.Now().UTC(), SourceDigest: sourceDigest, EvidenceDigest: reviewHash(evidence), PeerCount: len(evidence), IdentityCount: count, Issues: compareRecovery(source, evidence)}
	if state == "completed" {
		var saved ReviewResult
		if json.Unmarshal(previous, &saved) != nil || saved.EvidenceDigest != result.EvidenceDigest || saved.SourceDigest != sourceDigest || saved.ActivationAllowed {
			return empty, ErrReviewChanged
		}
		return saved, nil
	}
	tx, e = conn.Begin(ctx)
	if e != nil {
		return empty, ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	rawEvidence, _ := json.Marshal(evidence)
	rawResult, _ := json.Marshal(result)
	updated, e := tx.Exec(ctx, `UPDATE frogim_recovery.reviews SET evidence=$2,result=$3,state='completed',completed_at=clock_timestamp() WHERE id=$1 AND state='collecting' AND source_digest=$4`, r.ReviewID, rawEvidence, rawResult, sourceDigest)
	if e != nil || updated.RowsAffected() != 1 {
		return empty, ErrUnconfirmed
	}
	// Audits contain only stable digests and aggregate issue counts, never the
	// identity snapshot, phone numbers or credential data.
	counts := map[string]int{}
	for _, i := range result.Issues {
		counts[i.Code]++
	}
	meta, _ := json.Marshal(map[string]any{"sourceDigest": sourceDigest, "evidenceDigest": result.EvidenceDigest, "peerCount": result.PeerCount, "identityCount": count, "issueCounts": counts, "activationAllowed": false})
	if _, e = tx.Exec(ctx, `INSERT INTO public.platform_audits(actor_id,action,job_id,reason,metadata) VALUES($1,'platform.recovery.review_completed',$2,$3,$4)`, r.Actor, r.ReviewID, r.Reason, meta); e != nil || tx.Commit(ctx) != nil {
		return empty, ErrUnconfirmed
	}
	return result, nil
}

// Status returns historical evidence with its observation time. It deliberately
// does not imply that enterprise state still matches that evidence.
func (d Database) ReviewStatus(ctx context.Context, r ReviewRequest) (ReviewResult, error) {
	var out ReviewResult
	if !r.valid() {
		return out, ErrInvalid
	}
	conn, e := d.connect(ctx)
	if e != nil {
		return out, e
	}
	defer conn.Close(context.Background())
	if e = checkReviewGuard(ctx, conn, r); e != nil {
		return out, e
	}
	input, _ := json.Marshal(r)
	var raw []byte
	var same bool
	e = conn.QueryRow(ctx, `SELECT input=$2::jsonb,state,result FROM frogim_recovery.reviews WHERE id=$1`, r.ReviewID, input).Scan(&same, &out.State, &raw)
	if e != nil || !same {
		return ReviewResult{}, ErrInvalid
	}
	out.ReviewID = r.ReviewID
	if out.State == "completed" && (json.Unmarshal(raw, &out) != nil || out.ActivationAllowed) {
		return ReviewResult{}, ErrUnconfirmed
	}
	return out, nil
}

func loadReviewSource(ctx context.Context, conn *pgx.Conn, version int) (reviewSource, error) {
	out := reviewSource{Accounts: []reviewAccount{}, Tenants: []reviewTenant{}, Pending: map[string]int{}}
	tx, e := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return out, ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, `SELECT id,phone,COALESCE(tenant_id,''),COALESCE(local_user_id,''),assignment_version,auth_version,state,credentials_pending,globally_blocked FROM public.platform_accounts ORDER BY id COLLATE "C" LIMIT $1`, tenancy.RecoveryInventoryMaximum+1)
	if e != nil {
		return out, ErrUnconfirmed
	}
	for rows.Next() {
		var a reviewAccount
		if e = rows.Scan(&a.ID, &a.Phone, &a.TenantID, &a.LocalUserID, &a.AssignmentVersion, &a.AuthVersion, &a.State, &a.CredentialsPending, &a.GloballyBlocked); e != nil {
			rows.Close()
			return out, ErrUnconfirmed
		}
		out.Accounts = append(out.Accounts, a)
	}
	e = rows.Err()
	rows.Close()
	if e != nil || len(out.Accounts) > tenancy.RecoveryInventoryMaximum {
		return out, ErrUnconfirmed
	}
	rows, e = tx.Query(ctx, `SELECT id,http_base_url,status,access_version FROM public.platform_tenants ORDER BY id COLLATE "C" LIMIT 101`)
	if e != nil {
		return out, ErrUnconfirmed
	}
	for rows.Next() {
		var t reviewTenant
		if e = rows.Scan(&t.ID, &t.HTTPBaseURL, &t.State, &t.RealmVersion); e != nil {
			rows.Close()
			return out, ErrUnconfirmed
		}
		out.Tenants = append(out.Tenants, t)
	}
	e = rows.Err()
	rows.Close()
	if e != nil || len(out.Tenants) > 100 {
		return out, ErrUnconfirmed
	}
	queries := map[string]string{
		"IDENTITY_JOBS":     `SELECT count(*) FROM public.platform_jobs WHERE step<>'completed'`,
		"CREDENTIAL_JOBS":   `SELECT count(*) FROM public.platform_credential_jobs WHERE state<>'completed'`,
		"ACCESS_JOBS":       `SELECT count(*) FROM public.platform_access_jobs WHERE state<>'completed'`,
		"REALM_JOBS":        `SELECT count(*) FROM public.platform_realm_jobs WHERE state<>'completed'`,
		"IMPORTS":           `SELECT count(*) FROM public.platform_legacy_import_batches WHERE state<>'completed'`,
		"IMPORT_ITEMS":      `SELECT count(*) FROM public.platform_legacy_import_items WHERE step<>'completed'`,
		"BAN_EXPIRIES":      `SELECT count(*) FROM public.platform_legacy_ban_expiries WHERE state IN ('pending','queued')`,
		"DEPLOYMENTS":       `SELECT count(*) FROM public.platform_deployment_jobs WHERE state<>'completed'`,
		"BACKUPS":           `SELECT count(*) FROM public.platform_backup_jobs WHERE state IN ('pending','unconfirmed')`,
		"MAINTENANCE":       `SELECT count(*) FROM public.platform_maintenance_runs WHERE state='pending'`,
		"DIRECTORY_BACKUPS": `SELECT count(*) FROM public.platform_directory_backups WHERE state<>'completed'`,
	}
	if version >= 18 {
		queries["DIRECTORY_DAILY_RUNS"] = `SELECT count(*) FROM public.platform_directory_daily_runs WHERE state NOT IN ('completed','skipped')`
	}
	keys := []string{}
	for key := range queries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, kind := range keys {
		var n int
		if e = tx.QueryRow(ctx, queries[kind]).Scan(&n); e != nil {
			return out, ErrUnconfirmed
		}
		out.Pending[kind] = n
	}
	if tx.Commit(ctx) != nil {
		return out, ErrUnconfirmed
	}
	return out, nil
}
