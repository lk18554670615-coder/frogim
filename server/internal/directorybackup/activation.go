package directorybackup

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

// Credentials are supplied again on resume, never copied into operation input,
// evidence or audit. Only their commitments are persisted outside the auth row.
type RecoveryAccount struct {
	Identity           tenancy.Identity `json:"identity"`
	AuthVersion        int64            `json:"authVersion"`
	VerificationSHA256 string           `json:"verificationSha256"`
	CredentialSHA256   string           `json:"credentialSha256"`
	PasswordHash       string           `json:"-"`
}
type ActivationRequest struct {
	ReviewRequest
	// accounts releases only explicitly reverified held accounts while offline.
	Mode                      string            `json:"mode,omitempty"`
	ActivationID              string            `json:"activationId"`
	EvidenceDigest            string            `json:"evidenceDigest"`
	AuthoritySHA256           string            `json:"authoritySha256"`
	OldAuthoritiesSHA256      string            `json:"oldAuthoritiesSha256"`
	PlatformControlURL        string            `json:"platformControlUrl"`
	MaintenanceEvidenceSHA256 string            `json:"maintenanceEvidenceSha256"`
	AdminUsername             string            `json:"adminUsername"`
	AdminCredentialSHA256     string            `json:"adminCredentialSha256"`
	AdminPasswordHash         string            `json:"-"`
	Accounts                  []RecoveryAccount `json:"accounts"`
}
type ActivationResult struct {
	ID              string    `json:"id"`
	State           string    `json:"state"`
	ResumedAccounts int       `json:"resumedAccounts"`
	HeldAccounts    int       `json:"heldAccounts"`
	AuthoritySHA256 string    `json:"authoritySha256"`
	CompletedAt     time.Time `json:"completedAt,omitempty"`
}
type AuthorityVerifier func(context.Context, ReviewPeer) error

func validRecoveryHash(hash string) bool {
	cost, e := bcrypt.Cost([]byte(hash))
	return e == nil && cost >= 10 && cost <= 14 && len(hash) == 60
}
func (r ActivationRequest) validActivation(secrets bool) bool {
	if (r.Mode != "" && r.Mode != "accounts") || !r.ReviewRequest.valid() || !tenancy.ValidID(r.ActivationID) || !reviewDigest.MatchString(r.EvidenceDigest) || !reviewDigest.MatchString(r.AuthoritySHA256) || !reviewDigest.MatchString(r.OldAuthoritiesSHA256) || !reviewDigest.MatchString(r.MaintenanceEvidenceSHA256) || tenancy.ValidateBaseURL(r.PlatformControlURL, false) != nil || len(r.Accounts) > tenancy.RecoveryInventoryMaximum {
		return false
	}
	if r.Mode == "" && (!tenancy.ValidID(r.AdminUsername) || len(r.AdminUsername) < 3 || !reviewDigest.MatchString(r.AdminCredentialSHA256) || secrets && (!validRecoveryHash(r.AdminPasswordHash) || reviewHash(r.AdminPasswordHash) != r.AdminCredentialSHA256)) {
		return false
	}
	if r.Mode == "accounts" && (len(r.Accounts) == 0 || r.AdminUsername != "" || r.AdminCredentialSHA256 != "" || r.AdminPasswordHash != "") {
		return false
	}
	previous := ""
	for _, a := range r.Accounts {
		if a.Identity.Validate() != nil || a.Identity.AccountID <= previous || a.AuthVersion < 1 || !reviewDigest.MatchString(a.VerificationSHA256) || !reviewDigest.MatchString(a.CredentialSHA256) {
			return false
		}
		if secrets && (!validRecoveryHash(a.PasswordHash) || reviewHash(a.PasswordHash) != a.CredentialSHA256) {
			return false
		}
		previous = a.Identity.AccountID
	}
	return true
}

const activationSchema = `CREATE TABLE IF NOT EXISTS frogim_recovery.activations (
 id text PRIMARY KEY, input jsonb NOT NULL, state text NOT NULL CHECK(state IN ('prepared','activated')),
 result jsonb, created_at timestamptz NOT NULL DEFAULT clock_timestamp()
); REVOKE ALL ON frogim_recovery.activations FROM PUBLIC;`
const holdSchema = `CREATE TABLE IF NOT EXISTS public.platform_recovery_holds (
 kind text NOT NULL, object_id text NOT NULL, recovery_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(kind,object_id));`

func checkActivationGuard(ctx context.Context, conn *pgx.Conn, r ActivationRequest) error {
	if checkReviewGuard(ctx, conn, r.ReviewRequest) != nil {
		return ErrQuarantined
	}
	var phase string
	if conn.QueryRow(ctx, `SELECT phase FROM frogim_recovery.guard WHERE singleton`).Scan(&phase) != nil {
		return ErrQuarantined
	}
	if r.Mode == "" && phase == "staged" {
		return nil
	}
	var matches bool
	if r.Mode != "accounts" || phase != "activated" || conn.QueryRow(ctx, `SELECT authority_sha256=$1 FROM frogim_recovery.guard WHERE singleton`, r.AuthoritySHA256).Scan(&matches) != nil || !matches {
		return ErrQuarantined
	}
	return nil
}

func lockRecovery(ctx context.Context, conn *pgx.Conn) error {
	for _, key := range []int64{tenancy.PlatformRecoveryLock, tenancy.PlatformMigrationLock} {
		var ok bool
		if conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&ok) != nil || !ok {
			return ErrUnconfirmed
		}
	}
	return nil
}

func (d Database) PrepareActivation(ctx context.Context, r ActivationRequest) (ActivationResult, error) {
	if !r.validActivation(true) {
		return ActivationResult{}, ErrInvalid
	}
	conn, e := d.connect(ctx)
	if e != nil {
		return ActivationResult{}, e
	}
	defer conn.Close(context.Background())
	if lockRecovery(ctx, conn) != nil {
		return ActivationResult{}, ErrUnconfirmed
	}
	if checkActivationGuard(ctx, conn, r) != nil {
		return ActivationResult{}, ErrQuarantined
	}
	if _, e = activationReview(ctx, conn, r); e != nil {
		return ActivationResult{}, e
	}
	tx, e := conn.Begin(ctx)
	if e != nil {
		return ActivationResult{}, ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, activationSchema); e != nil {
		return ActivationResult{}, ErrUnconfirmed
	}
	raw, _ := json.Marshal(r)
	inserted, e := tx.Exec(ctx, `INSERT INTO frogim_recovery.activations(id,input,state) VALUES($1,$2,'prepared') ON CONFLICT DO NOTHING`, r.ActivationID, raw)
	if e != nil {
		return ActivationResult{}, ErrUnconfirmed
	}
	var same bool
	var state string
	if tx.QueryRow(ctx, `SELECT input=$2::jsonb,state FROM frogim_recovery.activations WHERE id=$1 FOR UPDATE`, r.ActivationID, raw).Scan(&same, &state) != nil || !same {
		return ActivationResult{}, ErrReviewChanged
	}
	if inserted.RowsAffected() == 1 {
		if _, e = tx.Exec(ctx, `INSERT INTO public.platform_audits(actor_id,action,job_id,reason,metadata) VALUES($1,'platform.recovery.prepared',$2,$3,jsonb_build_object('inputDigest',$4::text,'approvedAccounts',$5::int))`, r.Actor, r.ActivationID, r.Reason, reviewHash(r), len(r.Accounts)); e != nil {
			return ActivationResult{}, ErrUnconfirmed
		}
	}
	if tx.Commit(ctx) != nil {
		return ActivationResult{}, ErrUnconfirmed
	}
	return ActivationResult{ID: r.ActivationID, State: state, AuthoritySHA256: r.AuthoritySHA256}, nil
}

func activationReview(ctx context.Context, conn *pgx.Conn, r ActivationRequest) ([]ReviewEvidence, error) {
	input, _ := json.Marshal(r.ReviewRequest)
	var evidence []byte
	var sourceDigest string
	var same bool
	if conn.QueryRow(ctx, `SELECT input=$2::jsonb AND state='completed' AND result->>'evidenceDigest'=$3,source_digest,evidence FROM frogim_recovery.reviews WHERE id=$1`, r.ReviewID, input, r.EvidenceDigest).Scan(&same, &sourceDigest, &evidence) != nil || !same {
		return nil, ErrReviewChanged
	}
	source, e := loadReviewSource(ctx, conn, r.Expected.SchemaVersion)
	if e != nil || reviewHash(source) != sourceDigest {
		return nil, ErrReviewChanged
	}
	var out []ReviewEvidence
	if json.Unmarshal(evidence, &out) != nil || reviewHash(out) != r.EvidenceDigest || len(out) != len(r.Peers) {
		return nil, ErrReviewChanged
	}
	seen := map[string]bool{}
	for _, entry := range out {
		seen[entry.Inventory.TenantID] = true
	}
	for _, tenant := range source.Tenants {
		if !seen[tenant.ID] {
			return nil, ErrUnconfirmed
		}
	}
	return out, nil
}

func (d Database) ActivationStatus(ctx context.Context, r ActivationRequest) (ActivationResult, error) {
	var out ActivationResult
	if !r.validActivation(false) {
		return out, ErrInvalid
	}
	conn, e := d.connect(ctx)
	if e != nil {
		return out, e
	}
	defer conn.Close(context.Background())
	raw, _ := json.Marshal(r)
	var result []byte
	var same bool
	if conn.QueryRow(ctx, `SELECT input=$2::jsonb,state,result FROM frogim_recovery.activations WHERE id=$1`, r.ActivationID, raw).Scan(&same, &out.State, &result) != nil || !same {
		return ActivationResult{}, ErrInvalid
	}
	out.ID = r.ActivationID
	out.AuthoritySHA256 = r.AuthoritySHA256
	if out.State == "activated" && json.Unmarshal(result, &out) != nil {
		return ActivationResult{}, ErrUnconfirmed
	}
	return out, nil
}

func (d Database) Activate(ctx context.Context, r ActivationRequest, read InventoryReader, verify AuthorityVerifier) (ActivationResult, error) {
	var out ActivationResult
	if !r.validActivation(true) || read == nil || verify == nil {
		return out, ErrInvalid
	}
	conn, e := d.connect(ctx)
	if e != nil {
		return out, e
	}
	defer conn.Close(context.Background())
	if lockRecovery(ctx, conn) != nil {
		return out, ErrUnconfirmed
	}
	raw, _ := json.Marshal(r)
	var same bool
	var state string
	var result []byte
	if conn.QueryRow(ctx, `SELECT input=$2::jsonb,state,result FROM frogim_recovery.activations WHERE id=$1`, r.ActivationID, raw).Scan(&same, &state, &result) != nil || !same {
		return out, ErrReviewChanged
	}
	if state == "activated" {
		if json.Unmarshal(result, &out) != nil {
			return out, ErrUnconfirmed
		}
		return out, nil
	}
	if checkActivationGuard(ctx, conn, r) != nil {
		return out, ErrQuarantined
	}
	evidence, e := activationReview(ctx, conn, r)
	if e != nil {
		return out, e
	}
	e = watched(ctx, conn, func(work context.Context) error {
		for index, peer := range r.Peers {
			if e := verify(work, peer); e != nil {
				return e
			}
			current, digest, e := read(work, peer)
			if e != nil {
				return e
			}
			actual, e := current.Digest()
			if e != nil || actual != digest || digest != evidence[index].Digest || current.TenantID != peer.TenantID || current.HTTPBaseURL != peer.HTTPBaseURL || current.Realm.Version != peer.RealmVersion {
				return ErrReviewChanged
			}
		}
		return nil
	})
	if e != nil {
		return out, e
	}
	// Recheck the private database after all network calls, under the same lock.
	if _, e = activationReview(ctx, conn, r); e != nil {
		return out, e
	}
	approved, e := approvedRecoveryAccounts(r, evidence)
	if e != nil {
		return out, e
	}
	tx, e := conn.Begin(ctx)
	if e != nil {
		return out, ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, holdSchema); e != nil {
		return out, ErrUnconfirmed
	}
	// Preserve every old operation and its original state. Claims exclude holds;
	// neither successful nor failed old operations are silently replayed.
	if r.Mode == "" {
		for _, table := range recoveryHeldTables {
			if table == "platform_directory_daily_runs" && r.Expected.SchemaVersion < 18 {
				continue
			}
			column := "id"
			if table == "platform_legacy_ban_expiries" {
				column = "account_id"
			}
			if _, e = tx.Exec(ctx, `INSERT INTO public.platform_recovery_holds(kind,object_id,recovery_id) SELECT $1,`+column+`,$2 FROM public.`+table+` ON CONFLICT DO NOTHING`, table, r.ActivationID); e != nil {
				return out, ErrUnconfirmed
			}
		}
		if _, e = tx.Exec(ctx, `INSERT INTO public.platform_recovery_holds(kind,object_id,recovery_id) SELECT 'account',id,$1 FROM public.platform_accounts ON CONFLICT DO NOTHING;`, r.ActivationID); e != nil {
			return out, ErrUnconfirmed
		}
		if _, e = tx.Exec(ctx, `UPDATE public.platform_accounts SET password_hash='',state=CASE WHEN state='deleted' THEN state ELSE 'blocked' END,updated_at=clock_timestamp(); UPDATE public.platform_admin_accounts SET enabled=false,password_hash='',auth_version=auth_version+1; UPDATE public.platform_enterprise_codes SET enabled=false; UPDATE public.platform_jobs SET poll_hash=NULL;`); e != nil {
			return out, ErrUnconfirmed
		}
	}
	for _, entry := range evidence {
		v := entry.Inventory
		// A recovered directory must not reopen a peer whose revocations or
		// identities remain unconfirmed. Later offline review can release only
		// this tenant gate; original operation holds are never removed here.
		if !v.Pending.Settled() || v.UnlinkedUsers != 0 {
			if _, e = tx.Exec(ctx, `INSERT INTO public.platform_recovery_holds(kind,object_id,recovery_id) VALUES('tenant',$1,$2) ON CONFLICT DO NOTHING`, v.TenantID, r.ActivationID); e != nil {
				return out, ErrUnconfirmed
			}
		} else if _, e = tx.Exec(ctx, `DELETE FROM public.platform_recovery_holds WHERE kind='tenant' AND object_id=$1`, v.TenantID); e != nil {
			return out, ErrUnconfirmed
		}
		var stale bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_tenants WHERE id=$1 AND access_version>$2)`, v.TenantID, v.Realm.Version).Scan(&stale) != nil || stale {
			return out, ErrReviewChanged
		}
		if _, e = tx.Exec(ctx, `INSERT INTO public.platform_tenants(id,display_name,http_base_url,status,access_version) VALUES($1,$1,$2,'suspended',$3) ON CONFLICT(id) DO UPDATE SET status='suspended',http_base_url=EXCLUDED.http_base_url,access_version=GREATEST(platform_tenants.access_version,EXCLUDED.access_version),config_version=platform_tenants.config_version+1`, v.TenantID, v.HTTPBaseURL, v.Realm.Version); e != nil {
			return out, ErrUnconfirmed
		}
	}
	for index, a := range r.Accounts {
		u := approved[index]
		var pending bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.platform_jobs WHERE account_id=$1 AND step<>'completed') OR EXISTS(SELECT 1 FROM public.platform_credential_jobs WHERE account_id=$1 AND state<>'completed') OR EXISTS(SELECT 1 FROM public.platform_access_jobs WHERE account_id=$1 AND state<>'completed') OR EXISTS(SELECT 1 FROM public.platform_accounts WHERE id=$1 AND (state='deleted' OR assignment_version>$2 OR auth_version>$3 OR (assignment_version=$2 AND (tenant_id<>$4 OR local_user_id<>$5))))`, a.Identity.AccountID, a.Identity.AssignmentVersion, a.AuthVersion, a.Identity.TenantID, a.Identity.LocalUserID).Scan(&pending) != nil || pending {
			return out, ErrUnconfirmed
		}
		if r.Mode == "accounts" {
			var held bool
			if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_recovery_holds WHERE kind='account' AND object_id=$1)`, a.Identity.AccountID).Scan(&held) != nil || !held {
				return out, ErrUnconfirmed
			}
		}
		// A fresh hash, explicit verification and a single live identity are all
		// required. Assignment/auth versions come from the suspended enterprise.
		if _, e = tx.Exec(ctx, `INSERT INTO public.platform_accounts(id,phone,password_hash,state,tenant_id,local_user_id,assignment_version,auth_version,credentials_pending,globally_blocked) VALUES($1,$2,$3,'active',$4,$5,$6,$7,false,false) ON CONFLICT(id) DO UPDATE SET phone=EXCLUDED.phone,password_hash=EXCLUDED.password_hash,state=CASE WHEN platform_accounts.globally_blocked THEN 'blocked' ELSE 'active' END,tenant_id=EXCLUDED.tenant_id,local_user_id=EXCLUDED.local_user_id,assignment_version=EXCLUDED.assignment_version,auth_version=EXCLUDED.auth_version,credentials_pending=false,updated_at=clock_timestamp()`, a.Identity.AccountID, u.Phone, a.PasswordHash, a.Identity.TenantID, a.Identity.LocalUserID, a.Identity.AssignmentVersion, a.AuthVersion); e != nil {
			return out, ErrUnconfirmed
		}
		if _, e = tx.Exec(ctx, `DELETE FROM public.platform_recovery_holds WHERE kind='account' AND object_id=$1`, a.Identity.AccountID); e != nil {
			return out, ErrUnconfirmed
		}
	}
	if r.Mode == "" {
		if _, e = tx.Exec(ctx, `INSERT INTO public.platform_admin_accounts(id,username,password_hash,role,enabled) VALUES($1,$2,$3,'operator',true)`, "recovery-"+r.ActivationID, strings.ToLower(r.AdminUsername), r.AdminPasswordHash); e != nil {
			return out, ErrUnconfirmed
		}
	}
	var held int
	if tx.QueryRow(ctx, `SELECT count(*) FROM public.platform_recovery_holds WHERE kind='account'`).Scan(&held) != nil {
		return out, ErrUnconfirmed
	}
	out = ActivationResult{ID: r.ActivationID, State: "activated", ResumedAccounts: len(r.Accounts), HeldAccounts: held, AuthoritySHA256: r.AuthoritySHA256, CompletedAt: time.Now().UTC()}
	encoded, _ := json.Marshal(out)
	if r.Mode == "" {
		if _, e = tx.Exec(ctx, `ALTER TABLE frogim_recovery.guard DROP CONSTRAINT guard_phase_check; ALTER TABLE frogim_recovery.guard ADD CONSTRAINT guard_phase_check CHECK(phase IN ('restoring','staged','activated')); ALTER TABLE frogim_recovery.guard ADD COLUMN activation_id text; ALTER TABLE frogim_recovery.guard ADD COLUMN authority_sha256 text;`); e != nil {
			return ActivationResult{}, ErrUnconfirmed
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE frogim_recovery.activations SET state='activated',result=$2 WHERE id=$1;`, r.ActivationID, encoded); e != nil {
		return ActivationResult{}, ErrUnconfirmed
	}
	if r.Mode == "" {
		if _, e = tx.Exec(ctx, `UPDATE frogim_recovery.guard SET phase='activated',activation_id=$1,authority_sha256=$2,updated_at=clock_timestamp() WHERE singleton`, r.ActivationID, r.AuthoritySHA256); e != nil {
			return ActivationResult{}, ErrUnconfirmed
		}
	}
	if _, e = tx.Exec(ctx, `INSERT INTO public.platform_audits(actor_id,action,job_id,reason,metadata) VALUES($1,'platform.recovery.activated',$2,$3,$4)`, r.Actor, r.ActivationID, r.Reason, encoded); e != nil || tx.Commit(ctx) != nil {
		return ActivationResult{}, ErrUnconfirmed
	}
	return out, nil
}

var recoveryHeldTables = []string{"platform_jobs", "platform_credential_jobs", "platform_access_jobs", "platform_realm_jobs", "platform_deployment_jobs", "platform_backup_jobs", "platform_maintenance_runs", "platform_legacy_ban_expiries", "platform_legacy_import_batches", "platform_legacy_import_items", "platform_directory_daily_runs"}

func approvedRecoveryAccounts(r ActivationRequest, evidence []ReviewEvidence) ([]tenancy.RecoveryIdentity, error) {
	identities := map[string][]tenancy.RecoveryIdentity{}
	phones := map[string]int{}
	settled := map[string]bool{}
	for _, e := range evidence {
		settled[e.Inventory.TenantID] = e.Inventory.Pending.Settled() && e.Inventory.UnlinkedUsers == 0
		for _, u := range e.Inventory.Users {
			if u.State != "retired" && !u.Deleted {
				identities[u.Identity.AccountID] = append(identities[u.Identity.AccountID], u)
				phones[u.Phone]++
			}
		}
	}
	out := make([]tenancy.RecoveryIdentity, 0, len(r.Accounts))
	for _, a := range r.Accounts {
		users := identities[a.Identity.AccountID]
		if len(users) != 1 {
			return nil, ErrUnconfirmed
		}
		u := users[0]
		if u.Identity != a.Identity || u.AuthVersion != a.AuthVersion || u.State != "active" || u.EnterpriseBanned || u.Deleted || phones[u.Phone] != 1 || !settled[a.Identity.TenantID] {
			return nil, ErrUnconfirmed
		}
		out = append(out, u)
	}
	return out, nil
}
