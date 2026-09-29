package platform

import (
	"context"
	"sync"
	"testing"
)

func seedLegacyExpiry(t *testing.T, s *Store) string {
	t.Helper()
	if _, e := s.pool.Exec(t.Context(), `INSERT INTO platform_accounts(id,phone,password_hash,state,tenant_id,local_user_id,auth_version,globally_blocked) VALUES('legacy-expiry-account','19900000661','','blocked','a','historical',2,true);
INSERT INTO platform_legacy_ban_expiries(account_id,request_id,expires_at,expected_auth_version) VALUES('legacy-expiry-account','legacy-expiry',now()-interval '1 second',2)`); e != nil {
		t.Fatal(e)
	}
	return "legacy-expiry-account"
}
func readyLegacyExpiry(t *testing.T, s *Store) {
	t.Helper()
	if _, e := s.pool.Exec(t.Context(), `UPDATE platform_legacy_ban_expiries SET retry_at=now()`); e != nil {
		t.Fatal(e)
	}
}
func TestPlatformPostgresLegacyBanExpiryUsesAcknowledgedAccess(t *testing.T) {
	s := isolatedPlatform(t)
	id := seedLegacyExpiry(t, s)
	ctx := t.Context()
	w := LegacyBanExpiryWorker{Store: s}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := w.Once(context.Background()); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var n int
	if s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_access_jobs WHERE account_id=$1`, id).Scan(&n) != nil || n != 1 {
		t.Fatal("expiry created duplicate access tasks")
	}
	p := &accessPeer{fail: true}
	aw := AccessWorker{Store: s, Enterprise: p}
	if _, e := aw.Once(ctx); e != nil {
		t.Fatal(e)
	}
	a := accessAccount(t, s, "19900000661")
	if !a.GloballyBlocked || a.State != "blocked" {
		t.Fatal("failed enterprise unblock released account")
	}
	p.fail = false
	readyAccessRetry(t, s)
	if _, e := aw.Once(ctx); e != nil {
		t.Fatal(e)
	}
	readyLegacyExpiry(t, s)
	if _, e := w.Once(ctx); e != nil {
		t.Fatal(e)
	}
	a = accessAccount(t, s, "19900000661")
	if a.GloballyBlocked || a.State != "active" || a.AuthVersion != 3 {
		t.Fatal("expiry did not finish acknowledged unblock")
	}
	var state string
	if s.pool.QueryRow(ctx, `SELECT state FROM platform_legacy_ban_expiries`).Scan(&state) != nil || state != "completed" {
		t.Fatal("expiry did not settle")
	}
}
func TestPlatformPostgresLegacyBanExpiryDoesNotLiftLaterBan(t *testing.T) {
	s := isolatedPlatform(t)
	id := seedLegacyExpiry(t, s)
	ctx := t.Context()
	if _, e := s.RequestAccess(ctx, id, "new-ban", "operator", "new independent ban", 2, true, true); e != nil {
		t.Fatal(e)
	}
	w := LegacyBanExpiryWorker{Store: s}
	if _, e := w.Once(ctx); e != nil {
		t.Fatal(e)
	} // original generation, but operator task pending
	var count int
	if s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_access_jobs WHERE NOT blocked`).Scan(&count) != nil || count != 0 {
		t.Fatal("expiry overtook later ban")
	}
	if _, e := (AccessWorker{Store: s, Enterprise: &accessPeer{}}).Once(ctx); e != nil {
		t.Fatal(e)
	}
	readyLegacyExpiry(t, s)
	if _, e := w.Once(ctx); e != nil {
		t.Fatal(e)
	}
	var state string
	if s.pool.QueryRow(ctx, `SELECT state FROM platform_legacy_ban_expiries`).Scan(&state) != nil || state != "superseded" {
		t.Fatal("later auth generation not respected")
	}
	a := accessAccount(t, s, "19900000661")
	if !a.GloballyBlocked || a.State != "blocked" {
		t.Fatal("later ban was lifted")
	}
}
