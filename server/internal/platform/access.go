package platform

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
)

var ErrAccessPending = errors.New("account access operation pending")

type AccessJob struct {
	ID        string `json:"jobId"`
	RequestID string `json:"requestId"`
	AccountID string `json:"accountId"`
	Blocked   bool   `json:"blocked"`
	Status    string `json:"status"`
	ErrorCode string `json:"errorCode,omitempty"`
}

// A global block stops authentication in this transaction. Existing enterprise
// sessions remain unconfirmed until the durable operation has been acknowledged.
// A ban racing with provisioning/transfer never corrupts that lifecycle: the
// directory stays blocked while its job settles, then this job fences its final
// enterprise identity. Unblocking cannot overtake unfinished work.
func (s *Store) RequestAccess(ctx context.Context, accountID, requestID, actor, reason string, expectedVersion int64, blocked, confirmed bool) (AccessJob, error) {
	if !tenancy.ValidID(accountID) || !tenancy.ValidID(requestID) || !tenancy.ValidID(actor) || !adminReason(reason, confirmed) || expectedVersion < 1 {
		return AccessJob{}, tenancy.ErrInvalid
	}
	reason = strings.TrimSpace(reason)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AccessJob{}, err
	}
	defer tx.Rollback(ctx)
	a, err := readAccount(ctx, tx, accountID)
	if err != nil {
		return AccessJob{}, err
	}
	var j AccessJob
	var previousReason string
	var previousVersion int64
	err = tx.QueryRow(ctx, `SELECT id,request_id,account_id,blocked,state,error_code,reason,expected_auth_version FROM platform_access_jobs WHERE actor_id=$1 AND request_id=$2`, actor, requestID).Scan(&j.ID, &j.RequestID, &j.AccountID, &j.Blocked, &j.Status, &j.ErrorCode, &previousReason, &previousVersion)
	if err == nil {
		if j.AccountID != accountID || j.Blocked != blocked || previousReason != reason || previousVersion != expectedVersion {
			return AccessJob{}, ErrRequestChanged
		}
		return j, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AccessJob{}, err
	}
	if a.State == "deleted" {
		return AccessJob{}, ErrDenied
	}
	if a.AuthVersion != expectedVersion {
		return AccessJob{}, ErrConflict
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_access_jobs WHERE account_id=$1 AND state<>'completed')`, accountID).Scan(&pending); err != nil {
		return AccessJob{}, err
	}
	if pending || (!blocked && (a.CredentialsPending || (a.State != "blocked" && a.State != "active"))) {
		return AccessJob{}, ErrAccessPending
	}
	if !blocked && !a.GloballyBlocked && a.State != "blocked" {
		return AccessJob{}, ErrConflict
	}
	id, err := newID("access")
	if err != nil {
		return AccessJob{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_accounts SET globally_blocked=true,state=CASE WHEN state='active' THEN 'blocked' ELSE state END,updated_at=now() WHERE id=$1`, accountID); err != nil {
		return AccessJob{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE account_id=$1`, accountID); err != nil {
		return AccessJob{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_access_jobs(id,request_id,account_id,actor_id,blocked,reason,expected_auth_version) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, requestID, accountID, actor, blocked, reason, expectedVersion); err != nil {
		return AccessJob{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,account_id,tenant_id,job_id,reason,metadata) VALUES($1,'account.global_access.requested',$2,$3,$4,$5,jsonb_build_object('beforeBlocked',$6::boolean,'requestedBlocked',$7::boolean))`, actor, accountID, a.TenantID, id, reason, a.GloballyBlocked || a.State == "blocked", blocked); err != nil {
		return AccessJob{}, err
	}
	return AccessJob{ID: id, RequestID: requestID, AccountID: accountID, Blocked: blocked, Status: "waiting"}, tx.Commit(ctx)
}

type accessWork struct {
	ID, Account, Lease, State string
	Attempts                  int
	Op                        tenancy.AccessOperation
}

func (s *Store) claimAccess(ctx context.Context) (*accessWork, error) {
	lease, err := tenancy.Secret()
	if err != nil {
		return nil, err
	}
	j := &accessWork{Lease: lease}
	err = s.pool.QueryRow(ctx, `WITH candidate AS (SELECT id FROM platform_access_jobs WHERE NOT EXISTS(SELECT 1 FROM platform_recovery_holds h WHERE h.kind='platform_access_jobs' AND h.object_id=platform_access_jobs.id) AND state<>'completed' AND retry_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE platform_access_jobs j SET lease_id=$1,lease_until=clock_timestamp()+interval '60 seconds',attempts=attempts+1 FROM candidate c WHERE j.id=c.id RETURNING j.id,j.account_id,j.attempts`, lease).Scan(&j.ID, &j.Account, &j.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

func (s *Store) prepareAccess(ctx context.Context, j *accessWork) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	a, err := readAccount(ctx, tx, j.Account)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT state,blocked,COALESCE(tenant_id,''),COALESCE(local_user_id,''),COALESCE(assignment_version,0),COALESCE(auth_version,0) FROM platform_access_jobs WHERE id=$1 AND lease_id=$2 AND lease_until>clock_timestamp() FOR UPDATE`, j.ID, j.Lease).Scan(&j.State, &j.Op.Blocked, &j.Op.Identity.TenantID, &j.Op.Identity.LocalUserID, &j.Op.Identity.AssignmentVersion, &j.Op.AuthVersion)
	if err != nil {
		return err
	}
	j.Op.OperationID = j.ID
	j.Op.Identity.AccountID = j.Account
	if j.State == "completed" {
		return ErrConflict
	}
	if !a.GloballyBlocked || a.State == "deleted" {
		return ErrConflict
	}
	if j.State == "waiting" {
		var identityPending bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_jobs WHERE account_id=$1 AND step<>'completed')`, j.Account).Scan(&identityPending); err != nil {
			return err
		}
		if identityPending || a.CredentialsPending {
			return ErrAccessPending
		}
		if a.State != "blocked" && a.State != "active" {
			return ErrConflict
		}
		j.Op.Identity = a.Identity
		j.Op.AuthVersion = a.AuthVersion + 1
		if _, err = tx.Exec(ctx, `UPDATE platform_accounts SET auth_version=$2,state='blocked',updated_at=now() WHERE id=$1`, j.Account, j.Op.AuthVersion); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE platform_access_jobs SET state='applying',tenant_id=$2,local_user_id=$3,assignment_version=$4,auth_version=$5,updated_at=now() WHERE id=$1`, j.ID, a.TenantID, a.LocalUserID, a.AssignmentVersion, j.Op.AuthVersion); err != nil {
			return err
		}
		j.State = "applying"
	} else if j.Op.Identity != a.Identity || j.Op.AuthVersion != a.AuthVersion {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *Store) finishAccess(ctx context.Context, j *accessWork) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	a, err := readAccount(ctx, tx, j.Account)
	if err != nil {
		return err
	}
	if a.Identity != j.Op.Identity || a.AuthVersion != j.Op.AuthVersion || !a.GloballyBlocked || a.State != "blocked" || a.CredentialsPending {
		return ErrConflict
	}
	tag, err := tx.Exec(ctx, `UPDATE platform_access_jobs SET state='completed',lease_id=NULL,lease_until=NULL,error_code='',updated_at=now() WHERE id=$1 AND state='applying' AND lease_id=$2 AND lease_until>clock_timestamp()`, j.ID, j.Lease)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_accounts SET globally_blocked=$2,state=CASE WHEN $2 THEN 'blocked' ELSE 'active' END,updated_at=now() WHERE id=$1`, j.Account, j.Op.Blocked); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,account_id,tenant_id,job_id,metadata) VALUES('system:access-worker','account.global_access.completed',$1,$2,$3,jsonb_build_object('blocked',$4::boolean,'authVersion',$5::bigint))`, j.Account, a.TenantID, j.ID, j.Op.Blocked, j.Op.AuthVersion); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type AccessWorker struct {
	Store      *Store
	Enterprise interface {
		SetAccess(context.Context, tenancy.AccessOperation) error
	}
}

func (w AccessWorker) Once(ctx context.Context) (bool, error) {
	j, err := w.Store.claimAccess(ctx)
	if err != nil || j == nil {
		return false, err
	}
	err = w.Store.prepareAccess(ctx, j)
	if err == nil {
		err = w.Enterprise.SetAccess(ctx, j.Op)
	}
	if err == nil {
		err = w.Store.finishAccess(ctx, j)
	}
	if err == nil {
		return true, nil
	}
	code := "ENTERPRISE_ACCESS_UNCONFIRMED"
	if errors.Is(err, ErrAccessPending) {
		code = "WAITING_ACCOUNT_OPERATION"
	}
	_, err = w.Store.pool.Exec(ctx, `UPDATE platform_access_jobs SET lease_id=NULL,lease_until=NULL,error_code=$3,retry_at=clock_timestamp()+($4::integer*interval '1 second'),updated_at=now() WHERE id=$1 AND lease_id=$2 AND state<>'completed'`, j.ID, j.Lease, code, min(300, 1<<min(j.Attempts, 8)))
	return true, err
}
func (w AccessWorker) Run(ctx context.Context) {
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
