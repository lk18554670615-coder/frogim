package legacyimport

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
)

var (
	ErrConfig = errors.New("PREFLIGHT_CONFIG_INVALID")
	ErrRead   = errors.New("PREFLIGHT_READ_UNAVAILABLE")
	ErrRealm  = errors.New("PREFLIGHT_DATABASE_REALM_MISMATCH")
	ErrSchema = errors.New("PREFLIGHT_SCHEMA_UNSUPPORTED")
	ErrTarget = errors.New("PREFLIGHT_DEFAULT_TENANT_MISMATCH")
	ErrLimit  = errors.New("PREFLIGHT_ROW_LIMIT_EXCEEDED")
)

// The current migration tooling is restricted to explicit loopback PostgreSQL
// URLs. Neither DSNs nor raw driver errors may be returned to command output.
// There is no automatic schema migration or fallback to production settings.
func OpenLocalReadOnly(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return openLocal(ctx, dsn, true)
}

// OpenLocalForImport is only for the explicit offline operator command. It does
// not adopt/migrate either database; normal server code never calls it.
func OpenLocalForImport(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return openLocal(ctx, dsn, false)
}
func openLocal(ctx context.Context, dsn string, readOnly bool) (*pgxpool.Pool, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" || u.Fragment != "" || u.Path == "" || strings.TrimSpace(dsn) != dsn {
		return nil, ErrConfig
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, ErrConfig
	}
	// Disallow query-host/socket/service overrides of the explicit URL authority.
	for key := range u.Query() {
		switch strings.ToLower(key) {
		case "host", "hostaddr", "port", "service", "servicefile", "passfile":
			return nil, ErrConfig
		}
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, ErrConfig
	}
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, e := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		if e != nil || ip == nil || !ip.IsLoopback() || (network != "tcp" && network != "tcp4" && network != "tcp6") {
			return nil, ErrConfig
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["application_name"] = "frogim-legacy-preflight"
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	if !readOnly {
		cfg.ConnConfig.RuntimeParams["application_name"] = "frogim-legacy-import"
		cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "off"
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "2000"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, ErrConfig
	}
	if pool.Ping(ctx) != nil {
		pool.Close()
		return nil, ErrRead
	}
	return pool, nil
}

func Preflight(ctx context.Context, source, target *pgxpool.Pool, tenant string, at time.Time) (Report, error) {
	var empty Report
	if source == nil || target == nil || !tenancy.ValidID(tenant) || at.IsZero() {
		return empty, ErrConfig
	}
	s, err := source.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return empty, ErrRead
	}
	defer s.Rollback(ctx)
	t, err := target.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return empty, ErrRead
	}
	defer t.Rollback(ctx)
	in := inventory{tenant: tenant}
	if err = readSource(ctx, s, &in); err != nil {
		return empty, err
	}
	if err = readTarget(ctx, t, &in); err != nil {
		return empty, err
	}
	result := analyze(in, at)
	// Roll back the read-only snapshots explicitly; never commit a write or hold
	// database locks while an operator is considering a report.
	if s.Rollback(ctx) != nil || t.Rollback(ctx) != nil {
		return empty, ErrRead
	}
	return result, nil
}

func readSource(ctx context.Context, tx pgx.Tx, in *inventory) error {
	var alien, users, migrations, identity bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_tables WHERE tablename='platform_accounts'),to_regclass('im_users') IS NOT NULL,to_regclass('im_schema_migrations') IS NOT NULL,to_regclass('im_tenant_identity') IS NOT NULL`).Scan(&alien, &users, &migrations, &identity)
	if err != nil {
		return ErrRead
	}
	if alien || !users || !migrations {
		return ErrRealm
	}
	if tx.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM im_schema_migrations`).Scan(&in.sourceVersion) != nil {
		return ErrRead
	}
	if in.sourceVersion < 72 || in.sourceVersion > 79 || identity != (in.sourceVersion >= 73) {
		return ErrSchema
	}
	if identity {
		err = tx.QueryRow(ctx, `SELECT tenant_id FROM im_tenant_identity WHERE singleton`).Scan(&in.boundTenant)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return ErrRead
		}
	}
	columns := `id,phone,CASE WHEN deleted_at IS NULL THEN password_hash ELSE '' END,banned,banned_until,deleted_at IS NOT NULL,''::text,1::bigint,1::bigint,'active'::text`
	if in.sourceVersion >= 73 {
		auth := `1::bigint`
		if in.sourceVersion >= 74 {
			auth = `platform_auth_version`
		}
		columns = `id,phone,CASE WHEN deleted_at IS NULL AND platform_account_id IS NULL AND local_identity_state<>'retired' THEN password_hash ELSE '' END,banned,banned_until,deleted_at IS NOT NULL,COALESCE(platform_account_id,''),assignment_version,` + auth + `,local_identity_state`
	}
	rows, err := tx.Query(ctx, `SELECT `+columns+` FROM im_users ORDER BY id COLLATE "C" LIMIT $1`, MaxRows+1)
	if err != nil {
		return ErrSchema
	}
	defer rows.Close()
	for rows.Next() {
		if len(in.source) >= MaxRows {
			return ErrLimit
		}
		var u sourceUser
		if rows.Scan(&u.id, &u.phone, &u.passwordHash, &u.banned, &u.bannedUntil, &u.deleted, &u.platformID, &u.assignment, &u.auth, &u.state) != nil {
			return ErrRead
		}
		if u.bannedUntil != nil {
			utc := u.bannedUntil.UTC()
			u.bannedUntil = &utc
		}
		in.source = append(in.source, u)
	}
	if rows.Err() != nil {
		return ErrRead
	}
	return nil
}
func readTarget(ctx context.Context, tx pgx.Tx, in *inventory) error {
	var alien, accounts, migrations bool
	if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_tables WHERE tablename IN ('im_users','im_tenant_identity')),to_regclass('platform_accounts') IS NOT NULL,to_regclass('platform_schema_migrations') IS NOT NULL`).Scan(&alien, &accounts, &migrations) != nil {
		return ErrRead
	}
	if alien || !accounts || !migrations {
		return ErrRealm
	}
	if tx.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM platform_schema_migrations`).Scan(&in.targetVersion) != nil {
		return ErrRead
	}
	if in.targetVersion < 1 || in.targetVersion > platform.SchemaVersion {
		return ErrSchema
	}
	var isDefault bool
	err := tx.QueryRow(ctx, `SELECT is_default,status FROM platform_tenants WHERE id=$1`, in.tenant).Scan(&isDefault, &in.targetState)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !isDefault {
		return ErrTarget
	}
	if err != nil {
		return ErrRead
	}
	rows, err := tx.Query(ctx, `SELECT id,phone,tenant_id,local_user_id,state,assignment_version,auth_version FROM platform_accounts ORDER BY id COLLATE "C" LIMIT $1`, MaxRows+1)
	if err != nil {
		return ErrSchema
	}
	defer rows.Close()
	for rows.Next() {
		if len(in.target) >= MaxRows {
			return ErrLimit
		}
		var a targetAccount
		if rows.Scan(&a.id, &a.phone, &a.tenant, &a.localID, &a.state, &a.assignment, &a.auth) != nil {
			return ErrRead
		}
		in.target = append(in.target, a)
	}
	if rows.Err() != nil {
		return ErrRead
	}
	return nil
}
