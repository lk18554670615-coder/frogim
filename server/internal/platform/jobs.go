package platform

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

// ProvisionInput is deliberately not a User: no business privileges, avatar,
// contacts, membership or historical referral relationship can cross tenants.
type ProvisionInput struct {
	PasswordRuneCount  int    `json:"passwordRuneCount,omitempty"`
	Phone              string `json:"phone"`
	Name               string `json:"name"`
	Gender             string `json:"gender,omitempty"`
	PersonalInviteCode string `json:"personalInviteCode,omitempty"`
	Method             string `json:"method"`
}

type Job struct {
	ID        string           `json:"id"`
	Kind      string           `json:"kind"`
	Source    tenancy.Identity `json:"source"`
	Target    tenancy.Identity `json:"target"`
	Step      string           `json:"step"`
	Input     ProvisionInput   `json:"-"`
	LeaseID   string           `json:"-"`
	ErrorCode string           `json:"errorCode,omitempty"`
	Attempts  int              `json:"attempts"`
}

type Reservation struct {
	JobID     string `json:"jobId"`
	PollToken string `json:"pollToken,omitempty"`
	Status    string `json:"status"`
}

type Registration struct {
	Phone, Password, Name, EnterpriseCode, PersonalInviteCode string
	// Caller is a verified OTP flow or authenticated tenant administrator.
	// The HTTP API must never accept these control fields from a public body.
	Method, ForcedTenantID, Actor string
	RequestID, Reason, Gender     string
}

var phoneDigits = regexp.MustCompile(`^[0-9]{11}$`)

func NormalizePhone(phone string) (string, error) {
	phone = strings.TrimSpace(phone)
	if strings.HasPrefix(phone, "+86") {
		phone = strings.TrimPrefix(phone, "+86")
	}
	if !phoneDigits.MatchString(phone) {
		return "", tenancy.ErrInvalid
	}
	return phone, nil
}

// Reserve requires identity verification by the transport layer; password
// knowledge alone does not prove ownership of a never-registered phone number.
func (s *Store) Reserve(ctx context.Context, r Registration) (Reservation, error) {
	phone, err := NormalizePhone(r.Phone)
	if err != nil {
		return Reservation{}, err
	}
	if r.Method != "password" && r.Method != "otp" && r.Method != "admin" {
		return Reservation{}, tenancy.ErrInvalid
	}
	if r.Method == "admin" && (!tenancy.ValidID(r.ForcedTenantID) || r.Actor == "") {
		return Reservation{}, ErrDenied
	}
	if r.Method != "admin" && r.ForcedTenantID != "" {
		return Reservation{}, ErrDenied
	}
	if r.RequestID != "" && (r.Method != "admin" || !tenancy.ValidID(r.RequestID) || !adminReason(r.Reason, true)) {
		return Reservation{}, tenancy.ErrInvalid
	}
	if r.Gender == "" {
		r.Gender = "unspecified"
	}
	if r.Gender != "unspecified" && r.Gender != "male" && r.Gender != "female" {
		return Reservation{}, tenancy.ErrInvalid
	}
	if len(r.Password) > 72 || (r.Method != "otp" && len([]rune(r.Password)) < 8) || len([]rune(strings.TrimSpace(r.Name))) < 1 || len([]rune(r.Name)) > 40 {
		return Reservation{}, tenancy.ErrInvalid
	}
	hash := ""
	if r.Password != "" {
		h, e := bcrypt.GenerateFromPassword([]byte(r.Password), 12)
		if e != nil {
			return Reservation{}, e
		}
		hash = string(h)
	}
	accountID, err := newID("acct")
	if err != nil {
		return Reservation{}, err
	}
	localID, err := newID("usr")
	if err != nil {
		return Reservation{}, err
	}
	jobID, err := newID("job")
	if err != nil {
		return Reservation{}, err
	}
	poll, err := tenancy.Secret()
	if err != nil {
		return Reservation{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Reservation{}, err
	}
	defer tx.Rollback(ctx)
	var tenantID string
	code := strings.ToUpper(strings.TrimSpace(r.EnterpriseCode))
	switch {
	case r.ForcedTenantID != "":
		err = tx.QueryRow(ctx, `SELECT id FROM platform_tenants WHERE id=$1 AND status='active' FOR SHARE`, r.ForcedTenantID).Scan(&tenantID)
	case code != "":
		err = tx.QueryRow(ctx, `SELECT t.id FROM platform_tenants t JOIN platform_enterprise_codes c ON c.tenant_id=t.id WHERE c.code_hash=$1 AND c.enabled AND t.status='active' FOR SHARE OF t,c`, tenancy.Hash(code)).Scan(&tenantID)
	default:
		err = tx.QueryRow(ctx, `SELECT id FROM platform_tenants WHERE is_default AND status='active' FOR SHARE`).Scan(&tenantID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, ErrDenied
	}
	if err != nil {
		return Reservation{}, err
	}
	if r.RequestID != "" {
		// Lock the request before the phone INSERT, so a lost response or two
		// concurrent retries resumes one durable job without a mapping crash gap.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,74))`, tenantID+":"+r.RequestID); err != nil {
			return Reservation{}, err
		}
		var previous, step, actor, reason, passwordHash string
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT j.id,CASE WHEN j.blocked THEN 'blocked' ELSE j.step END,j.actor_id,j.reason,j.input,a.password_hash FROM platform_jobs j JOIN platform_accounts a ON a.id=j.account_id WHERE j.kind='registration' AND j.target_tenant_id=$1 AND j.request_id=$2`, tenantID, r.RequestID).Scan(&previous, &step, &actor, &reason, &raw, &passwordHash)
		if err == nil {
			var in ProvisionInput
			if json.Unmarshal(raw, &in) != nil {
				return Reservation{}, ErrUnavailable
			}
			if actor != r.Actor || reason != strings.TrimSpace(r.Reason) || in.Phone != phone || in.Name != strings.TrimSpace(r.Name) || in.Gender != r.Gender || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(r.Password)) != nil {
				return Reservation{}, ErrRequestChanged
			}
			return Reservation{JobID: previous, Status: step}, tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Reservation{}, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO platform_accounts(id,phone,password_hash,state,tenant_id,local_user_id) VALUES($1,$2,$3,'provisioning',$4,$5)`, accountID, phone, hash, tenantID, localID)
	var conflict *pgconn.PgError
	if errors.As(err, &conflict) && conflict.Code == "23505" {
		return Reservation{}, ErrConflict
	}
	if err != nil {
		return Reservation{}, err
	}
	input, _ := json.Marshal(ProvisionInput{Phone: phone, Name: strings.TrimSpace(r.Name), Gender: r.Gender, PersonalInviteCode: strings.TrimSpace(r.PersonalInviteCode), Method: r.Method, PasswordRuneCount: len([]rune(r.Password))})
	actor := r.Actor
	if actor == "" {
		actor = accountID
	}
	var pollHash any = tenancy.Hash(poll)
	if r.Method == "admin" {
		poll = ""
		pollHash = nil
	}
	reason := strings.TrimSpace(r.Reason)
	if reason == "" {
		reason = "registration"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_jobs(id,kind,account_id,target_tenant_id,target_local_user_id,target_version,step,input,poll_hash,actor_id,reason,request_id) VALUES($1,'registration',$2,$3,$4,1,'prepare_target',$5,$6,$7,$8,NULLIF($9,''))`, jobID, accountID, tenantID, localID, input, pollHash, actor, reason, r.RequestID); err != nil {
		return Reservation{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,account_id,tenant_id,job_id,reason) VALUES($1,'account.reserved',$2,$3,$4,$5)`, actor, accountID, tenantID, jobID, reason); err != nil {
		return Reservation{}, err
	}
	return Reservation{JobID: jobID, PollToken: poll, Status: "pending"}, tx.Commit(ctx)
}

// Public administration must use preflight. It never holds a platform database
// transaction during the remote check; the locked version is rechecked below.
func (s *Store) RequestTransfer(ctx context.Context, accountID, targetTenant, actor, reason string, confirmed bool, checker interface {
	CheckTransfer(context.Context, tenancy.Identity) error
}) (string, error) {
	if !confirmed || !adminReason(reason, confirmed) || actor == "" || !tenancy.ValidID(targetTenant) || checker == nil {
		return "", tenancy.ErrInvalid
	}
	var i tenancy.Identity
	err := s.pool.QueryRow(ctx, `SELECT id,tenant_id,local_user_id,assignment_version FROM platform_accounts WHERE id=$1 AND state='active' AND NOT globally_blocked`, accountID).Scan(&i.AccountID, &i.TenantID, &i.LocalUserID, &i.AssignmentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrDenied
	}
	if err != nil {
		return "", err
	}
	if i.TenantID == targetTenant {
		return "", ErrConflict
	}
	if err = checker.CheckTransfer(ctx, i); err != nil {
		return "", err
	}
	return s.transfer(ctx, accountID, targetTenant, actor, reason, confirmed, &i)
}

func (s *Store) transfer(ctx context.Context, accountID, targetTenant, actor, reason string, confirmed bool, expected *tenancy.Identity) (string, error) {
	if !confirmed || actor == "" || strings.TrimSpace(reason) == "" || len(reason) > 1000 || !tenancy.ValidID(targetTenant) {
		return "", tenancy.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	a, err := readAccount(ctx, tx, accountID)
	if err != nil {
		return "", err
	}
	if a.State != "active" || a.GloballyBlocked || a.TenantID == targetTenant || a.CredentialsPending {
		return "", ErrConflict
	}
	if expected != nil && a.Identity != *expected {
		return "", ErrConflict
	}
	if _, err = readContext(ctx, tx, a); err != nil {
		return "", err
	}
	var target string
	if err = tx.QueryRow(ctx, `SELECT id FROM platform_tenants WHERE id=$1 AND status='active' FOR SHARE`, targetTenant).Scan(&target); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrDenied
		}
		return "", err
	}
	job, err := newID("job")
	if err != nil {
		return "", err
	}
	local, err := newID("usr")
	if err != nil {
		return "", err
	}
	input, _ := json.Marshal(ProvisionInput{Phone: a.Phone, Name: "新用户", Method: "transfer"})
	if _, err = tx.Exec(ctx, `UPDATE platform_accounts SET state='transferring',auth_version=auth_version+1,updated_at=now() WHERE id=$1`, accountID); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE account_id=$1`, accountID); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_jobs(id,kind,account_id,source_tenant_id,source_local_user_id,source_version,target_tenant_id,target_local_user_id,target_version,step,input,actor_id,reason) VALUES($1,'transfer',$2,$3,$4,$5,$6,$7,$8,'revoke_source',$9,$10,$11)`, job, accountID, a.TenantID, a.LocalUserID, a.AssignmentVersion, targetTenant, local, a.AssignmentVersion+1, input, actor, reason); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,account_id,tenant_id,job_id,reason) VALUES($1,'account.transfer.requested',$2,$3,$4,$5)`, actor, accountID, targetTenant, job, reason); err != nil {
		return "", err
	}
	return job, tx.Commit(ctx)
}

func (s *Store) Poll(ctx context.Context, jobID, token string) (string, string, error) {
	var step, code string
	err := s.pool.QueryRow(ctx, `SELECT CASE WHEN blocked THEN 'blocked' ELSE step END,error_code FROM platform_jobs WHERE id=$1 AND kind='registration' AND poll_hash=$2 AND created_at>clock_timestamp()-interval '24 hours'`, jobID, tenancy.Hash(token)).Scan(&step, &code)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrDenied
	}
	return step, code, err
}

// The registration poll credential is a one-use login capability, delivered
// only after real OTP verification (or a separately authenticated admin flow).
// Completion never needs the already consumed SMS code or a stored password.
func (s *Store) CompleteRegistration(ctx context.Context, jobID, token string) (LoginResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LoginResult{}, err
	}
	defer tx.Rollback(ctx)
	var accountID string
	err = tx.QueryRow(ctx, `SELECT account_id FROM platform_jobs WHERE id=$1 AND kind='registration' AND input->>'method' IN ('password','otp') AND poll_hash=$2 AND step='completed' AND created_at>clock_timestamp()-interval '24 hours'`, jobID, tenancy.Hash(token)).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return LoginResult{}, ErrDenied
	}
	if err != nil {
		return LoginResult{}, err
	}
	a, err := readAccount(ctx, tx, accountID)
	if err != nil {
		return LoginResult{}, err
	}
	if a.State != "active" {
		return LoginResult{}, ErrDenied
	}
	tag, err := tx.Exec(ctx, `UPDATE platform_jobs SET poll_hash=NULL WHERE id=$1 AND poll_hash=$2 AND step='completed' AND target_tenant_id=$3 AND target_local_user_id=$4 AND target_version=$5 AND created_at>clock_timestamp()-interval '24 hours'`, jobID, tenancy.Hash(token), a.TenantID, a.LocalUserID, a.AssignmentVersion)
	if err != nil {
		return LoginResult{}, err
	}
	if tag.RowsAffected() != 1 {
		return LoginResult{}, ErrDenied
	}
	result, err := issue(ctx, tx, a)
	if err != nil {
		return LoginResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,account_id,tenant_id,job_id) VALUES($1,'auth.registration.completed',$1,$2,$3)`, accountID, a.TenantID, jobID); err != nil {
		return LoginResult{}, err
	}
	return result, tx.Commit(ctx)
}

// Enterprise is implemented by the authenticated server-to-server transport.
// Revoke must not return success until local access, refresh, IM and push are
// actually fenced. Enqueuing an IM kick is insufficient acknowledgement.
type Enterprise interface {
	PrepareIdentity(context.Context, string, tenancy.Identity, ProvisionInput) error
	RevokeIdentity(context.Context, string, tenancy.Identity) error
}

type JobStore interface {
	Claim(context.Context) (*Job, error)
	Advance(context.Context, *Job, string) error
	Retry(context.Context, *Job, string) error
	Block(context.Context, *Job, string) error
}

type Worker struct {
	Store      JobStore
	Enterprise Enterprise
}

func (w Worker) Once(ctx context.Context) (bool, error) {
	j, err := w.Store.Claim(ctx)
	if err != nil || j == nil {
		return false, err
	}
	next := ""
	switch j.Step {
	case "revoke_source":
		err = w.Enterprise.RevokeIdentity(ctx, j.ID, j.Source)
		next = "prepare_target"
	case "prepare_target":
		err = w.Enterprise.PrepareIdentity(ctx, j.ID, j.Target, j.Input)
		next = "activate"
	case "activate":
		next = "completed"
	default:
		return true, w.Store.Retry(ctx, j, "INVALID_TASK_STEP")
	}
	if err != nil {
		var rejected *tenancy.OperationRejected
		if errors.As(err, &rejected) && tenancy.RepairableCode(rejected.Code) {
			return true, w.Store.Block(ctx, j, rejected.Code)
		}
		// Never persist remote response bodies, credentials, addresses or OTPs.
		return true, w.Store.Retry(ctx, j, "ENTERPRISE_OPERATION_UNCONFIRMED")
	}
	if err = w.Store.Advance(ctx, j, next); err != nil {
		// A suspended destination (or a failed progress transaction) must leave
		// the original task recoverable, with an explicit state and backoff.
		// Retry is fenced by the lease, so a lost committed response cannot
		// rewind a completed job or overwrite a newer worker.
		return true, w.Store.Retry(ctx, j, "IDENTITY_PROGRESS_UNCONFIRMED")
	}
	return true, nil
}

func (w Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			call, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, _ = w.Once(call)
			cancel()
		}
	}
}

func (s *Store) Claim(ctx context.Context) (*Job, error) {
	lease, err := tenancy.Secret()
	if err != nil {
		return nil, err
	}
	var j Job
	var input []byte
	err = s.pool.QueryRow(ctx, `WITH candidate AS (SELECT id FROM platform_jobs WHERE NOT EXISTS(SELECT 1 FROM platform_recovery_holds h WHERE h.kind='platform_jobs' AND h.object_id=platform_jobs.id) AND step<>'completed' AND NOT blocked AND retry_at<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE platform_jobs j SET lease_id=$1,lease_until=now()+interval '60 seconds',attempts=attempts+1 FROM candidate c WHERE j.id=c.id RETURNING j.id,j.kind,j.account_id,COALESCE(j.source_tenant_id,''),COALESCE(j.source_local_user_id,''),COALESCE(j.source_version,0),j.target_tenant_id,j.target_local_user_id,j.target_version,j.step,j.input,j.attempts`, lease).Scan(&j.ID, &j.Kind, &j.Target.AccountID, &j.Source.TenantID, &j.Source.LocalUserID, &j.Source.AssignmentVersion, &j.Target.TenantID, &j.Target.LocalUserID, &j.Target.AssignmentVersion, &j.Step, &input, &j.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.Source.AccountID = j.Target.AccountID
	j.LeaseID = lease
	if err = json.Unmarshal(input, &j.Input); err != nil {
		return nil, err
	}
	return &j, nil
}

func validTransition(from, to string) bool {
	return (from == "revoke_source" && to == "prepare_target") || (from == "prepare_target" && to == "activate") || (from == "activate" && to == "completed")
}

func (s *Store) Advance(ctx context.Context, j *Job, next string) error {
	if !validTransition(j.Step, next) {
		return tenancy.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Same lock order as transfer/auth: account before job.
	a, err := readAccount(ctx, tx, j.Target.AccountID)
	if err != nil {
		return err
	}
	expected := "provisioning"
	if j.Kind == "transfer" {
		expected = "transferring"
	}
	if a.State != expected {
		return ErrDenied
	}
	tag, err := tx.Exec(ctx, `UPDATE platform_jobs SET step=$1,lease_id=NULL,lease_until=NULL,error_code='',updated_at=now(),retry_at=now() WHERE id=$2 AND step=$3 AND lease_id=$4 AND lease_until>clock_timestamp()`, next, j.ID, j.Step, j.LeaseID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if next == "completed" {
		// Suspension racing with a job must prevent activation.
		check := account{Identity: j.Target}
		if _, err = readContext(ctx, tx, check); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE platform_accounts SET state=CASE WHEN globally_blocked THEN 'blocked' ELSE 'active' END,tenant_id=$2,local_user_id=$3,assignment_version=$4,updated_at=now() WHERE id=$1`, j.Target.AccountID, j.Target.TenantID, j.Target.LocalUserID, j.Target.AssignmentVersion); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,account_id,tenant_id,job_id,metadata) VALUES('system:identity-worker',$1,$2,$3,$4,jsonb_build_object('step',$5::text,'version',$6::bigint))`, "account."+j.Kind+".progress", j.Target.AccountID, j.Target.TenantID, j.ID, next, j.Target.AssignmentVersion); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Retry(ctx context.Context, j *Job, code string) error {
	seconds := min(300, 1<<min(j.Attempts, 8))
	_, err := s.pool.Exec(ctx, `UPDATE platform_jobs SET lease_id=NULL,lease_until=NULL,error_code=$1,retry_at=now()+($2::integer*interval '1 second'),updated_at=now() WHERE id=$3 AND lease_id=$4`, code, seconds, j.ID, j.LeaseID)
	return err
}

func (s *Store) Block(ctx context.Context, j *Job, code string) error {
	if !tenancy.RepairableCode(code) {
		return tenancy.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE platform_jobs SET blocked=true,lease_id=NULL,lease_until=NULL,error_code=$1,updated_at=now() WHERE id=$2 AND lease_id=$3 AND step=$4 AND lease_until>clock_timestamp()`, code, j.ID, j.LeaseID, j.Step)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,account_id,tenant_id,job_id,metadata) VALUES('system:identity-worker','identity.job.blocked',$1,$2,$3,jsonb_build_object('code',$4::text,'step',$5::text))`, j.Target.AccountID, j.Target.TenantID, j.ID, code, j.Step)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
