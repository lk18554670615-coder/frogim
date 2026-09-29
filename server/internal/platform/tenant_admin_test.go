package platform

import (
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestTenantAdminControlRefusesUnverifiedIdentity(t *testing.T) {
	a := &API{}
	for _, path := range []string{"/internal/tenancy/admin/accounts", "/internal/tenancy/admin/account-jobs", "/internal/tenancy/admin/password-reset", "/internal/tenancy/admin/credential-job"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		r.Header.Set("X-Tenant-ID", "a")
		r.Header.Set("X-SSL-Client-Verify", "SUCCESS")
		w := httptest.NewRecorder()
		a.InternalHandler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("trusted forwarded identity", w.Code)
		}
	}
}

func TestPlatformPostgresTenantAdminIdempotencyScopeAndAudit(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	in := Registration{Phone: "02800000001", Name: "enterprise admin created", Gender: "female", Password: "InitialPassword123!", Method: "admin", ForcedTenantID: "a", Actor: "tenant:a:admin:ops", RequestID: "request-1", Reason: "test admin request"}
	var wg sync.WaitGroup
	results := make(chan Reservation, 4)
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); j, err := s.Reserve(ctx, in); results <- j; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var jobID string
	for job := range results {
		if job.PollToken != "" {
			t.Fatal("admin got an end-user capability")
		}
		if jobID != "" && jobID != job.JobID {
			t.Fatal("duplicate jobs")
		}
		jobID = job.JobID
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_audits WHERE job_id=$1 AND action='account.reserved' AND reason=$2`, jobID, in.Reason).Scan(&count); err != nil || count != 1 {
		t.Fatal("reservation audit not atomic/idempotent", count, err)
	}
	var leaked bool
	if err := s.pool.QueryRow(ctx, `SELECT input::text LIKE '%'||$2||'%' OR poll_hash IS NOT NULL FROM platform_jobs WHERE id=$1`, jobID, in.Password).Scan(&leaked); err != nil || leaked {
		t.Fatal("credential persisted in task", err)
	}
	for _, tenant := range []string{"a", "b"} {
		items, err := s.TenantAccountJobs(ctx, tenant, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if tenant == "a" && (len(items) != 1 || items[0].Status != "pending") {
			t.Fatal("pending job missing")
		}
		if tenant == "b" && len(items) != 0 {
			t.Fatal("foreign task disclosed")
		}
	}
	changed := in
	changed.Name = "changed"
	if _, err := s.Reserve(ctx, changed); !errors.Is(err, ErrRequestChanged) {
		t.Fatal("request reused with new input", err)
	}
	changed = in
	changed.Actor = "tenant:a:admin:other"
	if _, err := s.Reserve(ctx, changed); !errors.Is(err, ErrRequestChanged) {
		t.Fatal("request hijacked by other admin", err)
	}
	changed = in
	changed.ForcedTenantID = "b"
	changed.Actor = "tenant:b:admin:ops"
	if _, err := s.Reserve(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("cross enterprise phone reopened", err)
	}
	worker := Worker{Store: s, Enterprise: &fakeEnterprise{}}
	for range 2 {
		if _, err := worker.Once(ctx); err != nil {
			t.Fatal(err)
		}
	}
	job, err := s.Reserve(ctx, in)
	if err != nil || job.JobID != jobID || job.Status != "completed" {
		t.Fatal("completed request not recoverable", err)
	}
	if _, err = s.CompleteRegistration(ctx, jobID, ""); !errors.Is(err, ErrDenied) {
		t.Fatal("admin task minted user session", err)
	}
	login, err := s.Login(ctx, in.Phone, in.Password)
	if err != nil || login.TenantContext.TenantID != "a" {
		t.Fatal("account not activated in correct enterprise", err)
	}
}
