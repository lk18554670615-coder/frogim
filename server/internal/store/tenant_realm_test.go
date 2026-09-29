package store

import (
	"context"
	"fmt"
	"github.com/linli/im/server/internal/tenancy"
	"testing"
	"time"
)

func testTenantRealmLifecycle(t *testing.T, p *Postgres, identity tenancy.Identity, grant tenancy.Grant) {
	ctx := t.Context()
	for index := range 12 {
		user := fmt.Sprintf("realm-fixture-%02d", index)
		i := tenancy.Identity{TenantID: "a", AccountID: user, LocalUserID: user, AssignmentVersion: 1}
		if e := p.PrepareTenantIdentity(ctx, TenantProvision{OperationID: user, Identity: i, Phone: user, Name: "realm fixture", Method: "transfer"}); e != nil {
			t.Fatal(e)
		}
		if e := p.ActivateTenantIdentity(ctx, i); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := p.pool.Exec(ctx, `UPDATE im_users SET banned=true WHERE id='realm-fixture-00';UPDATE im_users SET local_identity_state='platform_blocked' WHERE id='realm-fixture-01'`); e != nil {
		t.Fatal(e)
	}
	// A still-unconfirmed handshake must prevent declaring enterprise revocation complete.
	pending, e := p.BeginTenantMediaAttempt(ctx, identity, grant.AuthVersion, 1)
	if e != nil {
		t.Fatal(e)
	}
	pause := tenancy.RealmOperation{OperationID: "realm-pause", TenantID: "a", Version: 2}
	if e = p.WithTenantSessionFence(ctx, identity.LocalUserID, func() error {
		short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		called := false
		err := p.WithTenantRealmFence(short, func() error { called = true; return nil })
		if err == nil || called {
			t.Error("exclusive pause overtook shared issuance fence")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = p.WithTenantRealmFence(ctx, func() error {
		done, e := p.BeginTenantRealm(ctx, pause)
		if done {
			t.Error("new pause already complete")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if done, e := p.BeginTenantRealm(ctx, pause); e != nil || done {
		t.Fatal("pending replay", done, e)
	}
	changed := pause
	changed.Enabled = true
	if _, e := p.BeginTenantRealm(ctx, changed); e == nil {
		t.Fatal("mutated operation accepted")
	}
	if _, e := p.TenantRealmVersion(ctx, "a"); e == nil {
		t.Fatal("disabled realm active")
	}
	if _, _, e := p.TenantAuthIdentity(ctx, "a", identity.LocalUserID); e == nil {
		t.Fatal("paused identity usable")
	}
	if e := p.ActivateTenantGrant(ctx, grant); e == nil {
		t.Fatal("old grant activated during pause")
	}
	if _, e := p.BeginTenantMediaAttempt(ctx, identity, grant.AuthVersion, 1); e == nil {
		t.Fatal("paused media handshake")
	}
	if e := p.CompleteTenantRealmTarget(ctx, pause, identity.LocalUserID); e == nil {
		t.Fatal("unknown media marked settled")
	}
	if remaining, e := p.FinishTenantRealm(ctx, pause); e != nil || remaining < 12 {
		t.Fatal("unfinished targets ignored", remaining, e)
	}
	// Model the original transport receiving its confirmed 101, not timeout cleanup.
	if e := p.CompleteTenantMediaAttempt(ctx, pending); e != nil {
		t.Fatal(e)
	}
	drain := func(op tenancy.RealmOperation) {
		t.Helper()
		batches := 0
		for {
			users, e := p.TenantRealmTargets(ctx, op)
			if e != nil {
				t.Fatal(e)
			}
			if len(users) == 0 {
				break
			}
			if len(users) > 10 {
				t.Fatal("unbounded batch")
			}
			batches++
			for _, user := range users {
				if e = p.CompleteTenantRealmTarget(ctx, op, user); e != nil {
					t.Fatal(e)
				}
			}
		}
		if batches < 2 {
			t.Fatal("batch fixture too small")
		}
		if n, e := p.FinishTenantRealm(ctx, op); e != nil || n != 0 {
			t.Fatal(n, e)
		}
	}
	drain(pause)
	testSuspendedRecoveryInventory(t, p)
	resume := tenancy.RealmOperation{OperationID: "realm-resume", TenantID: "a", Version: 3, Enabled: true}
	if done, e := p.BeginTenantRealm(ctx, resume); e != nil || done {
		t.Fatal(done, e)
	}
	if e := p.ActivateTenantGrant(ctx, tenancy.Grant{Identity: identity, AuthVersion: grant.AuthVersion, RealmVersion: 3}); e == nil {
		t.Fatal("resume opened before targets")
	}
	drain(resume)
	nonce, _ := tenancy.Secret()
	if _, e := p.TenantRecoveryInventory(ctx, "https://a.example.test", tenancy.RecoveryInventoryRequest{Nonce: nonce, TenantID: "a", RealmVersion: 2}); e == nil {
		t.Fatal("resumed realm exposed stale recovery inventory")
	}
	if done, e := p.BeginTenantRealm(ctx, pause); e != nil || !done {
		t.Fatal("completed replay", done, e)
	}
	if v, e := p.TenantRealmVersion(ctx, "a"); e != nil || v != 3 {
		t.Fatal("old job disabled new realm", v, e)
	}
	if e := p.ActivateTenantGrant(ctx, grant); e == nil {
		t.Fatal("resume revived pre-pause grant")
	}
	if _, e := p.BeginTenantMediaAttempt(ctx, identity, grant.AuthVersion, 1); e == nil {
		t.Fatal("resume revived old media credential")
	}
	grant.RealmVersion = 3
	if e := p.ActivateTenantGrant(ctx, grant); e != nil {
		t.Fatal("fresh grant rejected", e)
	}
	for _, user := range []string{"realm-fixture-00", "realm-fixture-01"} {
		if _, _, e := p.TenantAuthIdentity(ctx, "a", user); e == nil {
			t.Fatal("resume cleared account ban", user)
		}
	}
	if e := p.migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if v, e := p.TenantRealmVersion(ctx, "a"); e != nil || v != 3 {
		t.Fatal("migration reset epoch", e)
	}
	var count int
	if e := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE action IN ('tenant.access.started','tenant.access.completed')`).Scan(&count); e != nil || count != 4 {
		t.Fatal("audit duplicated", count, e)
	}
}
