package platform

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
)

// Requires a dedicated disposable platform database. Business integration tests
// use a different database; their DSN must never be supplied here.
func isolatedPlatform(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("PLATFORM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated platform PostgreSQL not configured")
	}
	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	name, err := newID("test")
	if err != nil {
		t.Fatal(err)
	}
	name = "platform_test_" + stringHex(tenancy.Hash(name)[:8])
	if _, err = conn.Exec(t.Context(), `CREATE SCHEMA `+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, err := pgx.Connect(context.Background(), dsn)
		if err == nil {
			defer cleanup.Close(context.Background())
			_, _ = cleanup.Exec(context.Background(), `DROP SCHEMA `+pgx.Identifier{name}.Sanitize()+` CASCADE`)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("test DSN must be a PostgreSQL URL")
	}
	query := u.Query()
	query.Set("search_path", name)
	u.RawQuery = query.Encode()
	s, err := Open(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	for _, tenant := range []string{"a", "b"} {
		if err = s.PutTenant(t.Context(), tenant, tenant, "https://"+tenant+".example", "test", "isolated test", tenant == "a"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE platform_tenants SET status='active'`); err != nil {
		t.Fatal(err)
	}
	return s
}
func stringHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[2*i] = digits[v>>4]
		out[2*i+1] = digits[v&15]
	}
	return string(out)
}

func TestPlatformPostgresRegistrationTicketsTransfer(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal("repeat migration", err)
	}
	reservation, err := s.Reserve(ctx, Registration{Phone: "13812345678", Name: "test", Password: "Password123!", Method: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Login(ctx, "13812345678", "Password123!"); !errors.Is(err, ErrDenied) {
		t.Fatal("unprepared identity logged in", err)
	}
	peer := &fakeEnterprise{}
	worker := Worker{Store: s, Enterprise: peer}
	for range 2 {
		if _, err = worker.Once(ctx); err != nil {
			t.Fatal(err)
		}
	}
	step, _, err := s.Poll(ctx, reservation.JobID, reservation.PollToken)
	if err != nil || step != "completed" {
		t.Fatal(step, err)
	}
	login, err := s.Login(ctx, "+8613812345678", "Password123!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Consume(ctx, "b", login.SessionTicket); !errors.Is(err, ErrDenied) {
		t.Fatal("cross enterprise ticket", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.Consume(ctx, "a", login.SessionTicket); results <- e }()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if !errors.Is(e, ErrDenied) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal("ticket consumption count", success)
	}
	refresh, err := s.Refresh(ctx, login.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Refresh(ctx, login.RefreshToken); !errors.Is(err, ErrDenied) {
		t.Fatal("refresh replay", err)
	}
	grant, err := s.Consume(ctx, "a", refresh.SessionTicket)
	if err != nil {
		t.Fatal(err)
	}
	peer.failure = true
	if _, err = s.RequestTransfer(ctx, grant.AccountID, "b", "operator", "failed preflight", true, peer); err == nil {
		t.Fatal("source preflight failure ignored")
	}
	if _, err = s.Login(ctx, "13812345678", "Password123!"); err != nil {
		t.Fatal("preflight failure disabled login", err)
	}
	peer.failure = false
	if _, err = s.RequestTransfer(ctx, grant.AccountID, "b", "operator", "test transfer", true, peer); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Refresh(ctx, refresh.RefreshToken); !errors.Is(err, ErrDenied) {
		t.Fatal("transfer retained refresh", err)
	}
	peer.failure = true
	if _, err = worker.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Login(ctx, "13812345678", "Password123!"); !errors.Is(err, ErrDenied) {
		t.Fatal("unconfirmed revoke activated target", err)
	}
	peer.failure = false
	if _, err = s.pool.Exec(ctx, `UPDATE platform_jobs SET retry_at=now()`); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err = worker.Once(ctx); err != nil {
			t.Fatal(err)
		}
	}
	moved, err := s.Login(ctx, "13812345678", "Password123!")
	if err != nil {
		t.Fatal(err)
	}
	if moved.TenantContext.TenantID != "b" || moved.TenantContext.AssignmentVersion != 2 {
		t.Fatal(moved.TenantContext)
	}
	newGrant, err := s.Consume(ctx, "b", moved.SessionTicket)
	if err != nil {
		t.Fatal(err)
	}
	if newGrant.LocalUserID == grant.LocalUserID {
		t.Fatal("transfer reused local identity")
	}
}

func TestPlatformPostgresPhoneReservationUnique(t *testing.T) {
	s := isolatedPlatform(t)
	var wg sync.WaitGroup
	results := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Reserve(t.Context(), Registration{Phone: "13800000000", Name: "test", Password: "Password123!", Method: "password"})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("duplicate global phone", success)
	}
}

func TestPlatformPostgresRegistrationCompletionIsSingleUseAndNotAdminImpersonation(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	worker := Worker{Store: s, Enterprise: &fakeEnterprise{}}
	for index, method := range []string{"password", "admin"} {
		r := Registration{Phone: fmt.Sprintf("1380000090%d", index), Name: "completed", Password: "Password123!", Method: method}
		if method == "admin" {
			r.ForcedTenantID = "a"
			r.Actor = "test:admin"
		}
		reservation, err := s.Reserve(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.CompleteRegistration(ctx, reservation.JobID, reservation.PollToken); !errors.Is(err, ErrDenied) {
			t.Fatal("incomplete task returned session", err)
		}
		for range 2 {
			if _, err = worker.Once(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if method == "admin" {
			if _, err = s.CompleteRegistration(ctx, reservation.JobID, reservation.PollToken); !errors.Is(err, ErrDenied) {
				t.Fatal("admin provisioning credential impersonated user", err)
			}
			continue
		}
		var wg sync.WaitGroup
		results := make(chan error, 6)
		for range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := s.CompleteRegistration(ctx, reservation.JobID, reservation.PollToken)
				results <- e
			}()
		}
		wg.Wait()
		close(results)
		ok := 0
		for err := range results {
			if err == nil {
				ok++
			} else if !errors.Is(err, ErrDenied) {
				t.Fatal(err)
			}
		}
		if ok != 1 {
			t.Fatal("registration capability reused", ok)
		}
	}
}
