// Package platform owns global authentication and assignments, not enterprise
// business data. No enterprise repository is imported here.
package platform

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

//go:embed schema.sql
var schema string

// SchemaVersion is the current platform directory schema, not an enterprise
// schema. Migration/adoption tooling must reject newer platform databases.
const SchemaVersion = tenancy.PlatformSchemaVersion

var (
	ErrDenied         = errors.New("platform access denied")
	ErrUnavailable    = errors.New("platform temporarily unavailable")
	ErrConflict       = errors.New("account cannot be created")
	ErrRequestChanged = errors.New("request id was already used for different input")
)

type Store struct {
	pool              *pgxpool.Pool
	recoveryAuthority string
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	return OpenWithAuthority(ctx, dsn, "")
}

// A recovered directory is bound to the replacement control certificate.
func OpenWithAuthority(ctx context.Context, dsn, authority string) (*Store, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.ConnConfig.RuntimeParams["application_name"] = "frogim-platform"
	config.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	config.ConnConfig.RuntimeParams["lock_timeout"] = "5000"
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		var ok bool
		if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock_shared($1)`, tenancy.PlatformRecoveryLock).Scan(&ok); err != nil || !ok {
			return errors.New("platform directory is under offline recovery")
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	s := &Store{pool: pool, recoveryAuthority: authority}
	if err = s.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, tenancy.PlatformMigrationLock); err != nil {
		return err
	}
	var quarantined bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, tenancy.PlatformRecoverySchema).Scan(&quarantined); err != nil {
		return err
	}
	if quarantined {
		var activated bool
		if len(s.recoveryAuthority) != 64 || tx.QueryRow(ctx, `SELECT phase='activated' AND authority_sha256=$1 AND EXISTS(SELECT 1 FROM frogim_recovery.activations a WHERE a.id=activation_id AND a.state='activated') FROM frogim_recovery.guard WHERE singleton`, s.recoveryAuthority).Scan(&activated) != nil || !activated {
			return errors.New("platform recovery is quarantined or control authority differs")
		}
	}
	var business bool
	// Check the whole database, not only search_path: sharing a PG instance is
	// an operational restriction; sharing this database is also rejected here.
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_tables WHERE tablename IN ('im_users','im_tenant_identity'))`).Scan(&business); err != nil {
		return err
	}
	if business {
		return errors.New("platform database contains enterprise tables")
	}
	var hasVersion bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass('platform_schema_migrations') IS NOT NULL`).Scan(&hasVersion); err != nil {
		return err
	}
	if hasVersion {
		var version int
		if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0) FROM platform_schema_migrations`).Scan(&version); err != nil {
			return err
		}
		if version > SchemaVersion {
			return errors.New("platform database requires a newer server")
		}
	}
	if _, err = tx.Exec(ctx, schema); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type LoginResult struct {
	TenantContext tenancy.Context `json:"tenantContext"`
	SessionTicket string          `json:"sessionTicket"`
	RefreshToken  string          `json:"refreshToken"`
	ExpiresIn     int             `json:"expiresIn"`
}

type account struct {
	tenancy.Identity
	Phone, PasswordHash, State string
	AuthVersion                int64
	CredentialsPending         bool
	GloballyBlocked            bool
}

func readAccount(ctx context.Context, tx pgx.Tx, id string) (account, error) {
	var a account
	err := tx.QueryRow(ctx, `SELECT id,tenant_id,local_user_id,assignment_version,phone,password_hash,state,auth_version,credentials_pending,globally_blocked FROM platform_accounts WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM platform_recovery_holds WHERE kind='account' AND object_id=$1) FOR UPDATE`, id).Scan(&a.AccountID, &a.TenantID, &a.LocalUserID, &a.AssignmentVersion, &a.Phone, &a.PasswordHash, &a.State, &a.AuthVersion, &a.CredentialsPending, &a.GloballyBlocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrDenied
	}
	return a, err
}

func readContext(ctx context.Context, tx pgx.Tx, a account) (tenancy.Context, error) {
	c := tenancy.Context{TenantID: a.TenantID, AssignmentVersion: a.AssignmentVersion}
	err := tx.QueryRow(ctx, `SELECT display_name,http_base_url,config_version FROM platform_tenants WHERE id=$1 AND status='active' FOR SHARE`, a.TenantID).Scan(&c.DisplayName, &c.HTTPBaseURL, &c.ConfigVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrDenied
	}
	return c, err
}

func realmVersion(ctx context.Context, tx pgx.Tx, tenant string) (int64, error) {
	var version int64
	err := tx.QueryRow(ctx, `SELECT access_version FROM platform_tenants WHERE id=$1 AND status='active' FOR SHARE`, tenant).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrDenied
	}
	return version, err
}

// Login deliberately returns the same denial for nonexistent, blocked,
// provisioning and transferring accounts. It never reveals tenant ownership.
func (s *Store) Login(ctx context.Context, phone, password string) (LoginResult, error) {
	phone, normalErr := NormalizePhone(phone)
	if normalErr != nil {
		return LoginResult{}, ErrDenied
	}
	var id, hash string
	err := s.pool.QueryRow(ctx, `SELECT id,password_hash FROM platform_accounts WHERE phone=$1`, strings.TrimSpace(phone)).Scan(&id, &hash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return LoginResult{}, err
	}
	if hash == "" {
		hash = "$2a$12$zBHkYZDKBOCMhbEBxpfsFeqYHyCxLKKS.XpmGcAEv9HsjfkH09hQe"
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || id == "" {
		return LoginResult{}, ErrDenied
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LoginResult{}, err
	}
	defer tx.Rollback(ctx)
	a, err := readAccount(ctx, tx, id)
	if err != nil {
		return LoginResult{}, err
	}
	if a.State != "active" || a.PasswordHash != hash {
		return LoginResult{}, ErrDenied
	}
	result, err := issue(ctx, tx, a)
	if err != nil {
		return LoginResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,account_id,tenant_id) VALUES($1,'auth.login',$1,$2)`, a.AccountID, a.TenantID); err != nil {
		return LoginResult{}, err
	}
	return result, tx.Commit(ctx)
}

// LoginVerified may only follow successful verification by a real OTP provider.
// found=true never triggers registration, regardless of supplied enterprise code.
func (s *Store) LoginVerified(ctx context.Context, phone string) (LoginResult, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LoginResult{}, false, err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `SELECT id FROM platform_accounts WHERE phone=$1`, phone).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return LoginResult{}, false, nil
	}
	if err != nil {
		return LoginResult{}, false, err
	}
	a, err := readAccount(ctx, tx, id)
	if err != nil {
		return LoginResult{}, true, err
	}
	if a.State != "active" {
		return LoginResult{}, true, ErrDenied
	}
	result, err := issue(ctx, tx, a)
	if err != nil {
		return LoginResult{}, true, err
	}
	return result, true, tx.Commit(ctx)
}

func issue(ctx context.Context, tx pgx.Tx, a account) (LoginResult, error) {
	if a.CredentialsPending || a.GloballyBlocked || a.State != "active" {
		return LoginResult{}, ErrDenied
	}
	c, err := readContext(ctx, tx, a)
	if err != nil {
		return LoginResult{}, err
	}
	realm, err := realmVersion(ctx, tx, a.TenantID)
	if err != nil {
		return LoginResult{}, err
	}
	refresh, err := tenancy.Secret()
	if err != nil {
		return LoginResult{}, err
	}
	ticket, err := tenancy.Secret()
	if err != nil {
		return LoginResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_sessions(token_hash,account_id,assignment_version,auth_version,realm_version,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '30 days')`, tenancy.Hash(refresh), a.AccountID, a.AssignmentVersion, a.AuthVersion, realm); err != nil {
		return LoginResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_tickets(ticket_hash,session_hash,account_id,tenant_id,local_user_id,assignment_version,expires_at) VALUES($1,$2,$3,$4,$5,$6,now()+interval '60 seconds')`, tenancy.Hash(ticket), tenancy.Hash(refresh), a.AccountID, a.TenantID, a.LocalUserID, a.AssignmentVersion); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{TenantContext: c, SessionTicket: ticket, RefreshToken: refresh, ExpiresIn: int(tenancy.TicketTTL.Seconds())}, nil
}

func (s *Store) Refresh(ctx context.Context, token string) (LoginResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LoginResult{}, err
	}
	defer tx.Rollback(ctx)
	var id string
	if err = tx.QueryRow(ctx, `SELECT account_id FROM platform_sessions WHERE token_hash=$1`, tenancy.Hash(token)).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrDenied
		}
		return LoginResult{}, err
	}
	a, err := readAccount(ctx, tx, id)
	if err != nil {
		return LoginResult{}, err
	}
	if a.State != "active" {
		return LoginResult{}, ErrDenied
	}
	realm, err := realmVersion(ctx, tx, a.TenantID)
	if err != nil {
		return LoginResult{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE platform_sessions SET revoked_at=clock_timestamp() WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>clock_timestamp() AND auth_version=$2 AND assignment_version=$3 AND realm_version=$4`, tenancy.Hash(token), a.AuthVersion, a.AssignmentVersion, realm)
	if err != nil {
		return LoginResult{}, err
	}
	if tag.RowsAffected() != 1 {
		return LoginResult{}, ErrDenied
	}
	result, err := issue(ctx, tx, a)
	if err != nil {
		return LoginResult{}, err
	}
	// Preserve this installation's binding during refresh, not across a new
	// login. Logout and revoked/expired sessions cannot reactivate a binding.
	if _, err = tx.Exec(ctx, `UPDATE platform_push_devices SET session_hash=$2 WHERE session_hash=$1 AND revoked_at IS NULL`, tenancy.Hash(token), tenancy.Hash(result.RefreshToken)); err != nil {
		return LoginResult{}, err
	}
	return result, tx.Commit(ctx)
}

// Consume serializes against account transfer/ban before atomically claiming a
// ticket. The authenticated service identity supplies tenantID, not the body.
func (s *Store) Consume(ctx context.Context, tenantID, ticket string) (tenancy.Grant, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return tenancy.Grant{}, err
	}
	defer tx.Rollback(ctx)
	var accountID string
	if err = tx.QueryRow(ctx, `SELECT account_id FROM platform_tickets WHERE ticket_hash=$1 AND tenant_id=$2`, tenancy.Hash(ticket), tenantID).Scan(&accountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrDenied
		}
		return tenancy.Grant{}, err
	}
	a, err := readAccount(ctx, tx, accountID)
	if err != nil {
		return tenancy.Grant{}, err
	}
	if a.State != "active" || a.GloballyBlocked || a.TenantID != tenantID || a.CredentialsPending {
		return tenancy.Grant{}, ErrDenied
	}
	if _, err = readContext(ctx, tx, a); err != nil {
		return tenancy.Grant{}, err
	}
	realm, err := realmVersion(ctx, tx, a.TenantID)
	if err != nil {
		return tenancy.Grant{}, err
	}
	var expires time.Time
	err = tx.QueryRow(ctx, `UPDATE platform_tickets t SET consumed_at=clock_timestamp() FROM platform_sessions s WHERE t.ticket_hash=$1 AND t.session_hash=s.token_hash AND t.consumed_at IS NULL AND t.expires_at>clock_timestamp() AND t.assignment_version=$2 AND t.local_user_id=$3 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND s.auth_version=$4 AND s.realm_version=$5 RETURNING t.expires_at`, tenancy.Hash(ticket), a.AssignmentVersion, a.LocalUserID, a.AuthVersion, realm).Scan(&expires)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrDenied
	}
	if err != nil {
		return tenancy.Grant{}, err
	}
	return tenancy.Grant{Identity: a.Identity, AuthVersion: a.AuthVersion, RealmVersion: realm, ExpiresAt: expires}, tx.Commit(ctx)
}

func (s *Store) Logout(ctx context.Context, token string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `SELECT account_id FROM platform_sessions WHERE token_hash=$1`, tenancy.Hash(token)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = readAccount(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_sessions SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE token_hash=$1`, tenancy.Hash(token)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_push_devices SET revoked_at=clock_timestamp(),token_cipher=''::bytea,revision=revision+1 WHERE session_hash=$1 AND revoked_at IS NULL`, tenancy.Hash(token)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PutTenant is an administrative provisioning primitive, never a public login
// parameter. Activation must be performed by a separate verified health flow.
func (s *Store) PutTenant(ctx context.Context, id, name, address, actor, reason string, isDefault bool) error {
	if !tenancy.ValidID(id) || strings.TrimSpace(name) == "" || actor == "" || strings.TrimSpace(reason) == "" || tenancy.ValidateBaseURL(address, false) != nil {
		return tenancy.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO platform_tenants(id,display_name,http_base_url,is_default) VALUES($1,$2,$3,$4)`, id, name, strings.TrimRight(address, "/"), isDefault); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,reason) VALUES($1,'tenant.created',$2,$3)`, actor, id, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func newID(prefix string) (string, error) {
	raw, err := tenancy.Secret()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s", prefix, raw), nil
}
