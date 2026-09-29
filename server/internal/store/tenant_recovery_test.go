package store

import (
	"context"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
)

func testSuspendedRecoveryInventory(t *testing.T, p *Postgres) {
	t.Helper()
	read := func(ctx context.Context, r tenancy.RecoveryInventoryRequest) (tenancy.RecoveryInventoryPage, error) {
		return p.TenantRecoveryInventory(ctx, "https://a.example.test", r)
	}
	r, digest, e := tenancy.CollectRecoveryInventory(t.Context(), "a", "https://a.example.test", 2, read)
	if e != nil || len(r.Users) < 12 || digest == "" || r.Realm.Enabled || !r.Realm.SuspensionConfirmed {
		t.Fatal("inventory", e)
	}
	foundBanned, foundBlocked := false, false
	for _, u := range r.Users {
		if u.Identity.LocalUserID == "realm-fixture-00" {
			foundBanned = u.EnterpriseBanned
		}
		if u.Identity.LocalUserID == "realm-fixture-01" {
			foundBlocked = u.State == "platform_blocked"
		}
	}
	if !foundBanned || !foundBlocked {
		t.Fatal("inventory omitted restrictions")
	}
	nonce, _ := tenancy.Secret()
	for _, req := range []tenancy.RecoveryInventoryRequest{
		{Nonce: nonce, TenantID: "b", RealmVersion: 2},
		{Nonce: nonce, TenantID: "a", RealmVersion: 1},
	} {
		if _, e := read(t.Context(), req); e == nil {
			t.Fatal("foreign or stale realm accepted")
		}
	}
	if _, e := p.pool.Exec(t.Context(), `UPDATE im_users SET platform_auth_version=platform_auth_version+1 WHERE id='realm-fixture-00'`); e != nil {
		t.Fatal(e)
	}
	if _, e := read(t.Context(), tenancy.RecoveryInventoryRequest{Nonce: nonce, TenantID: "a", RealmVersion: 2, ExpectedDigest: digest}); e == nil {
		t.Fatal("changed inventory matched old digest")
	}
}
