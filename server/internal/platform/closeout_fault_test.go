package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
)

type closeoutPeer struct{ run func() error }

func (p closeoutPeer) PrepareIdentity(context.Context, string, tenancy.Identity, ProvisionInput) error {
	return p.run()
}
func (p closeoutPeer) RevokeIdentity(context.Context, string, tenancy.Identity) error { return p.run() }
func (p closeoutPeer) RevokeCredentials(context.Context, tenancy.CredentialOperation) error {
	return p.run()
}
func (p closeoutPeer) SetAccess(context.Context, tenancy.AccessOperation) error { return p.run() }
func (p closeoutPeer) SetRealm(_ context.Context, op tenancy.RealmOperation) (tenancy.RealmAck, error) {
	return tenancy.RealmAck{RealmOperation: op, State: "completed"}, p.run()
}
func (p closeoutPeer) CheckReadiness(_ context.Context, id, nonce string) (tenancy.Readiness, error) {
	return (&realmPeer{}).CheckReadiness(context.Background(), id, nonce)
}

// Existing transport fixtures verify actual enterprise revocation. This matrix
// targets the directory checkpoint after a remote operation: stale owners and
// lost database connections must not publish completion or restore old access.
func TestPlatformPostgresCloseoutFaultMatrix(t *testing.T) {
	for _, kind := range []string{"registration", "transfer", "password", "ban", "realm"} {
		for _, fault := range []string{"lease", "connection", "restart"} {
			t.Run(kind+"/"+fault, func(t *testing.T) {
				s := isolatedPlatform(t)
				ctx := t.Context()
				dsn := s.pool.Config().ConnString()
				if _, e := s.Reserve(ctx, Registration{Phone: "13800000889", Name: "fault matrix", Password: "FaultPassword123!", Method: "password"}); e != nil {
					t.Fatal(e)
				}
				good := closeoutPeer{func() error { return nil }}
				if kind != "registration" {
					for range 2 {
						if _, e := (Worker{s, good}).Once(ctx); e != nil {
							t.Fatal(e)
						}
					}
				}
				a := accessAccount(t, s, "13800000889")
				table, column := "platform_jobs", "step"
				switch kind {
				case "transfer":
					if _, e := s.transfer(ctx, a.AccountID, "b", "operator", "fault test", true, nil); e != nil {
						t.Fatal(e)
					}
				case "password":
					l, e := s.Login(ctx, "13800000889", "FaultPassword123!")
					if e != nil {
						t.Fatal(e)
					}
					if _, e = s.ChangePassword(ctx, "fault-password", l.RefreshToken, "FaultPassword123!", "ChangedPassword123!", &credentialPeer{}); e != nil {
						t.Fatal(e)
					}
					table, column = "platform_credential_jobs", "state"
				case "ban":
					if _, e := s.RequestAccess(ctx, a.AccountID, "fault-ban", "operator", "test", a.AuthVersion, true, true); e != nil {
						t.Fatal(e)
					}
					table, column = "platform_access_jobs", "state"
				case "realm":
					if _, e := s.RequestRealm(ctx, "a", "fault-realm", "operator", "test", 1, false, true); e != nil {
						t.Fatal(e)
					}
					table, column = "platform_realm_jobs", "state"
				}
				once := func(db *Store, peer closeoutPeer) (bool, error) {
					switch kind {
					case "password":
						return (CredentialWorker{db, peer}).Once(ctx)
					case "ban":
						return (AccessWorker{db, peer}).Once(ctx)
					case "realm":
						return (RealmWorker{db, peer}).Once(ctx)
					default:
						return (Worker{db, peer}).Once(ctx)
					}
				}
				injected := false
				bad := closeoutPeer{func() error {
					injected = true
					if fault == "connection" {
						// Terminate only connections acquired from THIS fixture pool. Other
						// test schemas and the persistent local stack are never selected.
						admin, e := pgx.Connect(ctx, dsn)
						if e != nil {
							t.Fatal(e)
						}
						defer admin.Close(context.Background())
						idle := s.pool.AcquireAllIdle(ctx)
						for _, c := range idle {
							var stopped bool
							if admin.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, c.Conn().PgConn().PID()).Scan(&stopped) != nil || !stopped {
								t.Fatal("fixture terminate")
							}
							c.Release()
						}
						return errors.New("remote ACK lost with database disconnect")
					}
					if _, e := s.pool.Exec(ctx, `UPDATE `+table+` SET lease_id='replacement-owner',lease_until=clock_timestamp()-interval '1 second' WHERE `+column+`<>'completed'`); e != nil {
						t.Fatal(e)
					}
					if fault == "restart" {
						return errors.New("worker interrupted before acknowledgement")
					}
					return nil
				}}
				_, _ = once(s, bad)
				if !injected {
					t.Fatal("fault hook not reached")
				}
				// Reopen the repository and worker from durable state (no in-memory job).
				s.Close()
				fresh, e := Open(ctx, dsn)
				if e != nil {
					t.Fatal(e)
				}
				defer fresh.Close()
				var remaining int
				if fresh.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE `+column+`<>'completed'`).Scan(&remaining) != nil || remaining != 1 {
					t.Fatal("unknown/stale result completed")
				}
				if _, e = fresh.Login(ctx, "13800000889", "FaultPassword123!"); e == nil {
					t.Fatal("old access resurrected")
				}
				if _, e = fresh.pool.Exec(ctx, `UPDATE `+table+` SET retry_at=now(),lease_until=now()-interval '1 second' WHERE `+column+`<>'completed'`); e != nil {
					t.Fatal(e)
				}
				for range 4 {
					if _, e = once(fresh, good); e != nil {
						t.Fatal(e)
					}
				}
				if fresh.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE `+column+`<>'completed'`).Scan(&remaining) != nil || remaining != 0 {
					t.Fatal("original operation could not resume")
				}
			})
		}
	}
}
