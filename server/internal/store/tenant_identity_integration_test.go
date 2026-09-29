package store

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
)

func TestTenantPostgresIdentityLifecycle(t *testing.T) {
	dsn := os.Getenv("IM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated enterprise PostgreSQL required")
	}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	schema := fmt.Sprintf("tenant_test_%d", time.Now().UnixNano())
	if _, err = conn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("test DSN must be a URL")
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	p, err := NewPostgresWithOptions(ctx, u.String(), PostgresOptions{TenantID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.migrate(ctx); err != nil {
		t.Fatal("repeat migration", err)
	}
	if schemaVersion != 79 {
		t.Fatal(schemaVersion)
	}
	if err = p.BindTenant(ctx, "b", true); err == nil {
		t.Fatal("rebound another enterprise")
	}
	if err = p.verifyDatabaseRealm(ctx, ""); err == nil {
		t.Fatal("allowed missing identity on bound database")
	}
	i := tenancy.Identity{AccountID: "account", TenantID: "a", LocalUserID: "original", AssignmentVersion: 1}
	in := TenantProvision{OperationID: "registration", Identity: i, Phone: "13812345678", Name: "test", Method: "password", PasswordRuneCount: 12}
	if err = p.PrepareTenantIdentity(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err = p.PrepareTenantIdentity(ctx, in); err != nil {
		t.Fatal("prepare not idempotent", err)
	}
	if _, err = p.TenantIdentity(ctx, "a", "original"); err == nil {
		t.Fatal("prepared identity usable")
	}
	if err = p.ActivateTenantIdentity(ctx, i); err != nil {
		t.Fatal(err)
	}
	if _, err = p.TenantIdentity(ctx, "a", "original"); err != nil {
		t.Fatal(err)
	}
	foreign := i
	foreign.TenantID = "b"
	if err = p.ActivateTenantIdentity(ctx, foreign); err == nil {
		t.Fatal("foreign activation")
	}
	attempt, err := p.BeginTenantMediaAttempt(ctx, i, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.BeginTenantMediaAttempt(ctx, i, 1, 1); err != ErrTenantMediaUnconfirmed {
		t.Fatal("duplicate ambiguous join not blocked", err)
	}
	if err = p.BeginTenantRevocation(ctx, "transfer", i); err != nil {
		t.Fatal(err)
	}
	if err = p.FinishTenantRevocation(ctx, "transfer", i); err == nil {
		t.Fatal("unsettled call did not prevent transfer acknowledgement")
	}
	if _, err = p.BeginTenantMediaAttempt(ctx, i, 1, 1); err == nil {
		t.Fatal("late join started after freeze")
	}
	if err = p.CompleteTenantMediaAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	if err = p.ActivateTenantIdentity(ctx, i); err == nil {
		t.Fatal("resurrected retired identity")
	}
	if err = p.FinishTenantRevocation(ctx, "transfer", i); err != nil {
		t.Fatal(err)
	}
	if err = p.BeginTenantRevocation(ctx, "transfer", i); err != nil {
		t.Fatal("retry revoke", err)
	}
	back := i
	back.LocalUserID = "returned"
	back.AssignmentVersion = 3
	in = TenantProvision{OperationID: "transfer_back", Identity: back, Phone: "13812345678", Name: "new identity", Method: "transfer"}
	if err = p.PrepareTenantIdentity(ctx, in); err != nil {
		t.Fatal("historical phone prevented re-entry", err)
	}
	if err = p.ActivateTenantIdentity(ctx, back); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_users WHERE phone='13812345678'`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if _, err = p.TenantIdentity(ctx, "a", i.LocalUserID); err == nil {
		t.Fatal("old identity remained accessible")
	}
	grant := tenancy.Grant{Identity: back, AuthVersion: 3, RealmVersion: 1}
	if err = p.ActivateTenantGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	op := tenancy.CredentialOperation{OperationID: "password-change", Identity: back, AuthVersion: 4}
	attempt, err = p.BeginTenantMediaAttempt(ctx, back, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if done, e := p.BeginTenantCredentialRevocation(ctx, op); e != nil || done {
		t.Fatal(done, e)
	}
	if err = p.FinishTenantCredentialRevocation(ctx, op); err == nil {
		t.Fatal("unsettled call did not prevent password acknowledgement")
	}
	if err = p.CheckTenantMediaSettled(ctx, back.LocalUserID); err != ErrTenantMediaUnconfirmed {
		t.Fatal("lost durable barrier", err)
	}
	if err = p.CompleteTenantMediaAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	if _, _, e := p.TenantAuthIdentity(ctx, "a", back.LocalUserID); e == nil {
		t.Fatal("resetting identity accessible")
	}
	if err = p.ActivateTenantGrant(ctx, grant); err == nil {
		t.Fatal("delayed grant restored resetting identity")
	}
	if done, e := p.BeginTenantCredentialRevocation(ctx, op); e != nil || done {
		t.Fatal("pending replay", done, e)
	}
	if err = p.FinishTenantCredentialRevocation(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err = p.ActivateTenantGrant(ctx, grant); err == nil {
		t.Fatal("old grant restored credentials")
	}
	grant.AuthVersion = 4
	if err = p.ActivateTenantGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if done, e := p.BeginTenantCredentialRevocation(ctx, op); e != nil || !done {
		t.Fatal("completed replay revoked new login", done, e)
	}
	if got, v, e := p.TenantAuthIdentity(ctx, "a", back.LocalUserID); e != nil || v != 4 || got != back {
		t.Fatal("password reset replaced identity", v, e)
	}
	t.Run("global access is separate from local ban", func(t *testing.T) {
		ban := tenancy.AccessOperation{CredentialOperation: tenancy.CredentialOperation{OperationID: "global-block", Identity: back, AuthVersion: 5}, Blocked: true}
		pending, e := p.BeginTenantMediaAttempt(ctx, back, 4, 1)
		if e != nil {
			t.Fatal(e)
		}
		if done, e := p.BeginTenantAccess(ctx, ban); e != nil || done {
			t.Fatal(done, e)
		}
		if e := p.FinishTenantAccess(ctx, ban); e == nil {
			t.Fatal("unconfirmed media permitted ban completion")
		}
		if e := p.ActivateTenantGrant(ctx, grant); e == nil {
			t.Fatal("delayed grant unblocked identity")
		}
		if _, _, e := p.TenantAuthIdentity(ctx, "a", back.LocalUserID); e == nil {
			t.Fatal("blocked identity accessible")
		}
		if _, e := p.BeginTenantMediaAttempt(ctx, back, 5, 1); e == nil {
			t.Fatal("blocked identity started a call")
		}
		credential := tenancy.CredentialOperation{OperationID: "forged-later-reset", Identity: back, AuthVersion: 6}
		if _, e := p.BeginTenantCredentialRevocation(ctx, credential); e == nil {
			t.Fatal("credential task overrode global block")
		}
		if e := p.CompleteTenantMediaAttempt(ctx, pending); e != nil {
			t.Fatal(e)
		}
		if e := p.FinishTenantAccess(ctx, ban); e != nil {
			t.Fatal(e)
		}
		if done, e := p.BeginTenantAccess(ctx, ban); e != nil || !done {
			t.Fatal("ban replay", done, e)
		}
		bad := ban
		bad.Blocked = false
		if _, e := p.BeginTenantAccess(ctx, bad); e == nil {
			t.Fatal("changed operation accepted")
		}
		if _, e := p.pool.Exec(ctx, `UPDATE im_users SET banned=true WHERE id=$1`, back.LocalUserID); e != nil {
			t.Fatal(e)
		}
		unban := tenancy.AccessOperation{CredentialOperation: tenancy.CredentialOperation{OperationID: "global-unblock", Identity: back, AuthVersion: 6}}
		if done, e := p.BeginTenantAccess(ctx, unban); e != nil || done {
			t.Fatal(done, e)
		}
		if e := p.FinishTenantAccess(ctx, unban); e != nil {
			t.Fatal(e)
		}
		if _, _, e := p.TenantAuthIdentity(ctx, "a", back.LocalUserID); e == nil {
			t.Fatal("global unban cleared local ban")
		}
		if _, e := p.pool.Exec(ctx, `UPDATE im_users SET banned=false WHERE id=$1`, back.LocalUserID); e != nil {
			t.Fatal(e)
		}
		if e := p.ActivateTenantGrant(ctx, grant); e == nil {
			t.Fatal("unban revived old auth version")
		}
		grant.AuthVersion = 6
		if e := p.ActivateTenantGrant(ctx, grant); e != nil {
			t.Fatal(e)
		}
		if done, e := p.BeginTenantAccess(ctx, ban); e != nil || !done {
			t.Fatal("old completed ban replay", done, e)
		}
		if got, v, e := p.TenantAuthIdentity(ctx, "a", back.LocalUserID); e != nil || v != 6 || got != back {
			t.Fatal("old ban removed later valid session", e)
		}
	})
	// Admin provisioning must not bypass the local password policy even though
	t.Run("realm pause and resume", func(t *testing.T) { testTenantRealmLifecycle(t, p, back, grant) })
	// Admin provisioning must not bypass the local password policy even though
	// it is exempt from public registration and invitation-code requirements.
	admin := TenantProvision{OperationID: "admin_policy", Identity: tenancy.Identity{AccountID: "admin-created-account", TenantID: "a", LocalUserID: "admin-created-user", AssignmentVersion: 1}, Phone: "02800000003", Name: "admin created", Gender: "female", Method: "admin", PasswordRuneCount: 8}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_settings(key,value) VALUES('passwordMinLength','16'),('registrationEnabled','false'),('inviteRegistrationMode','"required"') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		t.Fatal(err)
	}
	if err = p.PrepareTenantIdentity(ctx, admin); err == nil {
		t.Fatal("admin bypassed password minimum")
	}
	admin.PasswordRuneCount = 16
	if err = p.PrepareTenantIdentity(ctx, admin); err != nil {
		t.Fatal("admin should bypass public registration policy", err)
	}
}
