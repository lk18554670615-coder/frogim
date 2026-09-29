package platform

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
)

type credentialPeer struct {
	failure bool
	last    tenancy.CredentialOperation
}

func (p *credentialPeer) CheckPassword(_ context.Context, _ tenancy.Identity, n int) error {
	if n < 12 {
		return &tenancy.OperationRejected{Code: "TENANT_PASSWORD_POLICY_REJECTED"}
	}
	return nil
}
func (p *credentialPeer) RevokeCredentials(_ context.Context, op tenancy.CredentialOperation) error {
	p.last = op
	if p.failure {
		return errors.New("simulated transport outage")
	}
	return nil
}

func TestPlatformPostgresCredentialLifecycle(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(ctx, Registration{Phone: "13800000601", Name: "password task", Password: "OriginalPassword123!", Method: "password"}); err != nil {
		t.Fatal(err)
	}
	iw := Worker{Store: s, Enterprise: &fakeEnterprise{}}
	for range 2 {
		if _, err := iw.Once(ctx); err != nil {
			t.Fatal(err)
		}
	}
	l, err := s.Login(ctx, "13800000601", "OriginalPassword123!")
	if err != nil {
		t.Fatal(err)
	}
	peer := &credentialPeer{}
	if status, e := s.PasswordChangeStatus(ctx, "change-1", l.RefreshToken); e != nil || status.Status != "unconfirmed" {
		t.Fatal("unknown result should remain unconfirmed", e)
	}
	if _, err = s.ChangePassword(ctx, "wrong-old", l.RefreshToken, "wrong", "ChangedPassword123!", peer); !errors.Is(err, ErrDenied) {
		t.Fatal("old password bypass", err)
	}
	if _, err = s.ChangePassword(ctx, "short", l.RefreshToken, "OriginalPassword123!", "Eight123", peer); err == nil {
		t.Fatal("policy bypass")
	}
	var wg sync.WaitGroup
	results := make(chan CredentialJob, 3)
	errs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, e := s.ChangePassword(ctx, "change-1", l.RefreshToken, "OriginalPassword123!", "ChangedPassword123!", peer)
			results <- j
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var job CredentialJob
	for j := range results {
		if job.ID != "" && job.ID != j.ID {
			t.Fatal("duplicate password jobs")
		}
		job = j
	}
	if status, e := s.PasswordChangeStatus(ctx, "change-1", l.RefreshToken); e != nil || status.ID != job.ID || status.Status != "pending" {
		t.Fatal("lost acceptance cannot reconcile", e)
	}
	if _, e := s.PasswordChangeStatus(ctx, "change-1", strings.Repeat("z", 43)); !errors.Is(e, ErrDenied) {
		t.Fatal("foreign recovery credential", e)
	}
	if _, err = s.ChangePassword(ctx, "change-1", l.RefreshToken, "OriginalPassword123!", "OtherPassword123!", peer); !errors.Is(err, ErrRequestChanged) {
		t.Fatal("changed replay", err)
	}
	if _, err = s.ChangePassword(ctx, "change-2", l.RefreshToken, "OriginalPassword123!", "ChangedPassword123!", peer); !errors.Is(err, ErrDenied) {
		t.Fatal("revoked credential authorized new task", err)
	}
	if _, err = s.Login(ctx, "13800000601", "OriginalPassword123!"); !errors.Is(err, ErrDenied) {
		t.Fatal("login during revocation", err)
	}
	if _, err = s.Refresh(ctx, l.RefreshToken); !errors.Is(err, ErrDenied) {
		t.Fatal("refresh during revocation", err)
	}
	if status, e := s.PasswordChangeStatus(ctx, "never-received", l.RefreshToken); e != nil || status.Status != "unconfirmed" {
		t.Fatal("recent logout must not claim a lost attempt failed", e)
	}
	if _, err = s.Consume(ctx, "a", l.SessionTicket); !errors.Is(err, ErrDenied) {
		t.Fatal("pre-reset ticket usable", err)
	}
	var accountID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM platform_accounts WHERE phone='13800000601'`).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.transfer(ctx, accountID, "b", "test", "pending password", true, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("transfer raced password task", err)
	}
	peer.failure = true
	worker := CredentialWorker{Store: s, Enterprise: peer}
	if worked, e := worker.Once(ctx); e != nil || !worked {
		t.Fatal(worked, e)
	}
	j, err := s.CredentialStatus(ctx, job.ID, l.RefreshToken)
	if err != nil || j.Status != "pending" || j.ErrorCode != "ENTERPRISE_CREDENTIALS_UNCONFIRMED" {
		t.Fatal(j, err)
	}
	if _, err = s.CredentialStatus(ctx, job.ID, strings.Repeat("x", 43)); !errors.Is(err, ErrDenied) {
		t.Fatal("foreign poll")
	}
	if _, err = s.pool.Exec(ctx, `UPDATE platform_credential_jobs SET retry_at=now() WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	peer.failure = false
	if worked, e := worker.Once(ctx); e != nil || !worked {
		t.Fatal(worked, e)
	}
	if peer.last.AuthVersion != 2 || peer.last.Identity.AssignmentVersion != 1 {
		t.Fatal("reset changed business identity")
	}
	replay, err := s.ChangePassword(ctx, "change-1", l.RefreshToken, "OriginalPassword123!", "ChangedPassword123!", peer)
	if err != nil || replay.ID != job.ID || replay.Status != "completed" {
		t.Fatal("lost success cannot resume", err)
	}
	if _, err = s.Login(ctx, "13800000601", "OriginalPassword123!"); !errors.Is(err, ErrDenied) {
		t.Fatal("old password works", err)
	}
	newLogin, err := s.Login(ctx, "13800000601", "ChangedPassword123!")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := s.Consume(ctx, "a", newLogin.SessionTicket)
	if err != nil || grant.AuthVersion != 2 || grant.Identity != peer.last.Identity {
		t.Fatal("grant auth fence", err)
	}
	if _, err = s.AdminResetPassword(ctx, "b", grant.LocalUserID, "ops", "admin-foreign", "AdminPassword123!", "test reset", true, peer); !errors.Is(err, ErrDenied) {
		t.Fatal("cross tenant reset", err)
	}
	if _, err = s.AdminResetPassword(ctx, "a", grant.LocalUserID, "ops", "admin-no-confirm", "AdminPassword123!", "test reset", false, peer); err == nil {
		t.Fatal("unconfirmed reset")
	}
	adminJob, err := s.AdminResetPassword(ctx, "a", grant.LocalUserID, "ops", "admin-reset", "AdminPassword123!", "test reset", true, peer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TenantCredentialStatus(ctx, "b", grant.LocalUserID, adminJob.ID); !errors.Is(err, ErrDenied) {
		t.Fatal("foreign admin poll")
	}
	if _, err = s.pool.Exec(ctx, `UPDATE platform_accounts SET state='blocked' WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Login(ctx, "13800000601", "AdminPassword123!"); !errors.Is(err, ErrDenied) {
		t.Fatal("password reset unbanned user")
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_audits WHERE action='auth.password.requested' AND job_id=$1`, job.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("non-idempotent audit", count, err)
	}
	var leaked bool
	if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_audits WHERE metadata::text LIKE '%Password123%' OR metadata::text LIKE '%$2a$%')`).Scan(&leaked); err != nil || leaked {
		t.Fatal("audit leaked credential", err)
	}
}
