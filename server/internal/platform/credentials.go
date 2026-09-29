package platform

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

// CredentialJob is the only public task representation. No hash, password,
// requester capability, or upstream error body may be serialized to clients.
type CredentialJob struct {
	ID        string `json:"jobId"`
	RequestID string `json:"requestId"`
	Status    string `json:"status"`
	ErrorCode string `json:"errorCode,omitempty"`
}

type credentialRequest struct {
	ID, AccountID, Actor, Purpose, Reason, Password, CurrentPassword, Token string
	Tenant, LocalUserID                                                     string
}

type PasswordPolicy interface {
	CheckPassword(context.Context, tenancy.Identity, int) error
}

func (s *Store) ChangePassword(ctx context.Context, requestID, token, current, next string, policy PasswordPolicy) (CredentialJob, error) {
	if len(token) != 43 || len(current) > 72 {
		return CredentialJob{}, ErrDenied
	}
	var accountID string
	// A revoked session can only resume its exact original task below, never
	// authorize a new task, sign in, or mint a replacement session.
	err := s.pool.QueryRow(ctx, `SELECT account_id FROM platform_sessions WHERE token_hash=$1`, tenancy.Hash(token)).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CredentialJob{}, ErrDenied
	}
	if err != nil {
		return CredentialJob{}, err
	}
	return s.requestCredentials(ctx, credentialRequest{ID: requestID, AccountID: accountID, Actor: accountID, Purpose: "change", Reason: "user password change", Password: next, CurrentPassword: current, Token: token}, policy)
}

func (s *Store) AdminResetPassword(ctx context.Context, tenant, localUserID, actor, requestID, next, reason string, confirmed bool, policy PasswordPolicy) (CredentialJob, error) {
	if !tenancy.ValidID(tenant) || !tenancy.ValidID(localUserID) || !tenancy.ValidID(actor) || !adminReason(reason, confirmed) {
		return CredentialJob{}, tenancy.ErrInvalid
	}
	var accountID string
	err := s.pool.QueryRow(ctx, `SELECT id FROM platform_accounts WHERE tenant_id=$1 AND local_user_id=$2`, tenant, localUserID).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CredentialJob{}, ErrDenied
	}
	if err != nil {
		return CredentialJob{}, err
	}
	actor = "tenant:" + tenant + ":admin:" + actor
	return s.requestCredentials(ctx, credentialRequest{ID: requestID, AccountID: accountID, Actor: actor, Purpose: "admin", Reason: strings.TrimSpace(reason), Password: next, Token: actor, Tenant: tenant, LocalUserID: localUserID}, policy)
}

func credentialReplay(ctx context.Context, tx pgx.Tx, r credentialRequest) (*CredentialJob, error) {
	var j CredentialJob
	var accountID, hash, reason, tenant, local string
	var requester []byte
	err := tx.QueryRow(ctx, `SELECT id,request_id,state,error_code,account_id,new_password_hash,requester_hash,reason,tenant_id,local_user_id FROM platform_credential_jobs WHERE actor_id=$1 AND purpose=$2 AND request_id=$3`, r.Actor, r.Purpose, r.ID).Scan(&j.ID, &j.RequestID, &j.Status, &j.ErrorCode, &accountID, &hash, &requester, &reason, &tenant, &local)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if accountID != r.AccountID || !bytes.Equal(requester, tenancy.Hash(r.Token)) || reason != r.Reason || (r.Purpose == "admin" && (tenant != r.Tenant || local != r.LocalUserID)) || bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.Password)) != nil {
		return nil, ErrRequestChanged
	}
	return &j, nil
}

func authorizeCredentials(ctx context.Context, tx pgx.Tx, a account, r credentialRequest) error {
	if a.State != "active" || a.GloballyBlocked || a.CredentialsPending {
		return ErrDenied
	}
	if _, err := readContext(ctx, tx, a); err != nil {
		return err
	}
	if r.Purpose == "admin" {
		if a.TenantID != r.Tenant || a.LocalUserID != r.LocalUserID {
			return ErrDenied
		}
		return nil
	}
	if r.Purpose == "reset" {
		return authorizeRecovery(ctx, tx, a, r)
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_sessions WHERE token_hash=$1 AND account_id=$2 AND assignment_version=$3 AND auth_version=$4 AND revoked_at IS NULL AND expires_at>clock_timestamp() AND realm_version=(SELECT access_version FROM platform_tenants WHERE id=$5 AND status='active'))`, tenancy.Hash(r.Token), a.AccountID, a.AssignmentVersion, a.AuthVersion, a.TenantID).Scan(&valid); err != nil {
		return err
	}
	if !valid || a.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(r.CurrentPassword)) != nil {
		return ErrDenied
	}
	return nil
}

func (s *Store) requestCredentials(ctx context.Context, r credentialRequest, policy PasswordPolicy) (CredentialJob, error) {
	if !tenancy.ValidID(r.ID) || len(r.Password) > 72 || len([]rune(r.Password)) < 8 || policy == nil {
		return CredentialJob{}, tenancy.ErrInvalid
	}
	// Check and release locks before calling the enterprise; the second locked
	// check prevents a concurrent transfer, ban, session refresh or reset race.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CredentialJob{}, err
	}
	defer tx.Rollback(ctx)
	a, err := readAccount(ctx, tx, r.AccountID)
	if err != nil {
		return CredentialJob{}, err
	}
	if j, e := credentialReplay(ctx, tx, r); e != nil {
		return CredentialJob{}, e
	} else if j != nil {
		return *j, tx.Commit(ctx)
	}
	if err = authorizeCredentials(ctx, tx, a, r); err != nil {
		return CredentialJob{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CredentialJob{}, err
	}
	if err = policy.CheckPassword(ctx, a.Identity, len([]rune(r.Password))); err != nil {
		return CredentialJob{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(r.Password), 12)
	if err != nil {
		return CredentialJob{}, err
	}
	jobID, err := newID("cred")
	if err != nil {
		return CredentialJob{}, err
	}
	tx, err = s.pool.Begin(ctx)
	if err != nil {
		return CredentialJob{}, err
	}
	defer tx.Rollback(ctx)
	current, err := readAccount(ctx, tx, r.AccountID)
	if err != nil {
		return CredentialJob{}, err
	}
	if j, e := credentialReplay(ctx, tx, r); e != nil {
		return CredentialJob{}, e
	} else if j != nil {
		return *j, tx.Commit(ctx)
	}
	if current.Identity != a.Identity || current.AuthVersion != a.AuthVersion {
		return CredentialJob{}, ErrDenied
	}
	if err = authorizeCredentials(ctx, tx, current, r); err != nil {
		return CredentialJob{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_accounts SET credentials_pending=true,auth_version=auth_version+1,updated_at=now() WHERE id=$1`, a.AccountID); err != nil {
		return CredentialJob{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE account_id=$1`, a.AccountID); err != nil {
		return CredentialJob{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_credential_jobs(id,request_id,account_id,tenant_id,local_user_id,assignment_version,auth_version,new_password_hash,requester_hash,actor_id,purpose,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, jobID, r.ID, a.AccountID, a.TenantID, a.LocalUserID, a.AssignmentVersion, a.AuthVersion+1, string(hash), tenancy.Hash(r.Token), r.Actor, r.Purpose, r.Reason); err != nil {
		return CredentialJob{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,account_id,tenant_id,job_id,reason,metadata) VALUES($1,'auth.password.requested',$2,$3,$4,$5,jsonb_build_object('purpose',$6::text,'authVersion',$7::bigint))`, r.Actor, a.AccountID, a.TenantID, jobID, r.Reason, r.Purpose, a.AuthVersion+1); err != nil {
		return CredentialJob{}, err
	}
	if r.Purpose == "reset" {
		if _, err = tx.Exec(ctx, `UPDATE platform_password_recovery SET consumed_at=clock_timestamp() WHERE id=$1`, r.ID); err != nil {
			return CredentialJob{}, err
		}
	}
	return CredentialJob{ID: jobID, RequestID: r.ID, Status: "pending"}, tx.Commit(ctx)
}

func (s *Store) CredentialStatus(ctx context.Context, id, token string) (CredentialJob, error) {
	var j CredentialJob
	if !tenancy.ValidID(id) || len(token) != 43 {
		return j, ErrDenied
	}
	err := s.pool.QueryRow(ctx, `SELECT id,request_id,state,error_code FROM platform_credential_jobs WHERE id=$1 AND purpose='change' AND requester_hash=$2 AND created_at>clock_timestamp()-interval '24 hours'`, id, tenancy.Hash(token)).Scan(&j.ID, &j.RequestID, &j.Status, &j.ErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrDenied
	}
	return j, err
}

func (s *Store) PasswordChangeStatus(ctx context.Context, requestID, token string) (CredentialJob, error) {
	var j CredentialJob
	if !tenancy.ValidID(requestID) || len(token) != 43 {
		return j, ErrDenied
	}
	err := s.pool.QueryRow(ctx, `SELECT id,request_id,state,error_code FROM platform_credential_jobs WHERE request_id=$1 AND purpose='change' AND requester_hash=$2 AND created_at>clock_timestamp()-interval '24 hours'`, requestID, tenancy.Hash(token)).Scan(&j.ID, &j.RequestID, &j.Status, &j.ErrorCode)
	if err == nil {
		return j, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return j, err
	}
	var valid bool
	if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_sessions WHERE token_hash=$1 AND expires_at>clock_timestamp() AND (revoked_at IS NULL OR revoked_at>clock_timestamp()-interval '24 hours'))`, tenancy.Hash(token)).Scan(&valid); err != nil {
		return j, err
	}
	if !valid {
		return j, ErrDenied
	}
	// An in-flight original request may still commit. Absence is not cancellation
	// and must never be presented as a definite failed password change.
	// A locally abandoned attempt may have logged out; its recently revoked
	// credential only yields this non-sensitive status, never new login authority.
	return CredentialJob{RequestID: requestID, Status: "unconfirmed"}, nil
}

func (s *Store) TenantCredentialStatus(ctx context.Context, tenant, localUserID, id string) (CredentialJob, error) {
	var j CredentialJob
	err := s.pool.QueryRow(ctx, `SELECT j.id,j.request_id,j.state,j.error_code FROM platform_credential_jobs j JOIN platform_accounts a ON a.id=j.account_id WHERE j.id=$1 AND j.purpose='admin' AND j.tenant_id=$2 AND j.local_user_id=$3 AND a.tenant_id=$2 AND a.local_user_id=$3`, id, tenant, localUserID).Scan(&j.ID, &j.RequestID, &j.Status, &j.ErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrDenied
	}
	return j, err
}

func (s *Store) TenantCredentialJobs(ctx context.Context, tenant, localUserID string) ([]CredentialJob, error) {
	rows, err := s.pool.Query(ctx, `SELECT j.id,j.request_id,j.state,j.error_code FROM platform_credential_jobs j JOIN platform_accounts a ON a.id=j.account_id WHERE j.purpose='admin' AND j.tenant_id=$1 AND j.local_user_id=$2 AND a.tenant_id=$1 AND a.local_user_id=$2 ORDER BY j.created_at DESC,j.id DESC LIMIT 25`, tenant, localUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CredentialJob{}
	for rows.Next() {
		var j CredentialJob
		if err = rows.Scan(&j.ID, &j.RequestID, &j.Status, &j.ErrorCode); err != nil {
			return nil, err
		}
		items = append(items, j)
	}
	return items, rows.Err()
}

type credentialWork struct {
	Op       tenancy.CredentialOperation
	Lease    string
	Attempts int
}

func (s *Store) claimCredentials(ctx context.Context) (*credentialWork, error) {
	lease, err := tenancy.Secret()
	if err != nil {
		return nil, err
	}
	j := credentialWork{Lease: lease}
	err = s.pool.QueryRow(ctx, `WITH candidate AS (SELECT id FROM platform_credential_jobs WHERE NOT EXISTS(SELECT 1 FROM platform_recovery_holds h WHERE h.kind='platform_credential_jobs' AND h.object_id=platform_credential_jobs.id) AND state='pending' AND retry_at<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE platform_credential_jobs j SET lease_id=$1,lease_until=now()+interval '60 seconds',attempts=attempts+1 FROM candidate c WHERE j.id=c.id RETURNING j.id,j.account_id,j.tenant_id,j.local_user_id,j.assignment_version,j.auth_version,j.attempts`, lease).Scan(&j.Op.OperationID, &j.Op.Identity.AccountID, &j.Op.Identity.TenantID, &j.Op.Identity.LocalUserID, &j.Op.Identity.AssignmentVersion, &j.Op.AuthVersion, &j.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *Store) finishCredentials(ctx context.Context, j *credentialWork) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	a, err := readAccount(ctx, tx, j.Op.Identity.AccountID)
	if err != nil {
		return err
	}
	if a.Identity != j.Op.Identity || a.AuthVersion != j.Op.AuthVersion || !a.CredentialsPending {
		return ErrConflict
	}
	// Never restore a banned account to active; finishing the password task
	// changes credentials only. State remains independently governed.
	var hash string
	err = tx.QueryRow(ctx, `UPDATE platform_credential_jobs SET state='completed',lease_id=NULL,lease_until=NULL,error_code='',updated_at=now() WHERE id=$1 AND state='pending' AND lease_id=$2 AND lease_until>clock_timestamp() RETURNING new_password_hash`, j.Op.OperationID, j.Lease).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_accounts SET password_hash=$2,credentials_pending=false,updated_at=now() WHERE id=$1`, a.AccountID, hash); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,account_id,tenant_id,job_id,metadata) VALUES('system:credential-worker','auth.password.completed',$1,$2,$3,jsonb_build_object('authVersion',$4::bigint))`, a.AccountID, a.TenantID, j.Op.OperationID, a.AuthVersion); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type CredentialWorker struct {
	Store      *Store
	Enterprise interface {
		RevokeCredentials(context.Context, tenancy.CredentialOperation) error
	}
}

func (w CredentialWorker) Once(ctx context.Context) (bool, error) {
	j, err := w.Store.claimCredentials(ctx)
	if err != nil || j == nil {
		return false, err
	}
	if err = w.Enterprise.RevokeCredentials(ctx, j.Op); err == nil {
		err = w.Store.finishCredentials(ctx, j)
	}
	if err == nil {
		return true, nil
	}
	// Both a remote timeout and a lost commit response are safe to retry; the
	// enterprise completed marker prevents revoking a subsequent login.
	_, err = w.Store.pool.Exec(ctx, `UPDATE platform_credential_jobs SET lease_id=NULL,lease_until=NULL,error_code='ENTERPRISE_CREDENTIALS_UNCONFIRMED',retry_at=now()+($3::integer*interval '1 second'),updated_at=now() WHERE id=$1 AND lease_id=$2 AND state='pending'`, j.Op.OperationID, j.Lease, min(300, 1<<min(j.Attempts, 8)))
	return true, err
}

func (w CredentialWorker) Run(ctx context.Context) {
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
