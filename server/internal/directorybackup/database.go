// Package directorybackup backs up only the independent platform directory.
// Restore is deliberately staged: it never activates tenants or old jobs.
package directorybackup

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalid     = errors.New("platform backup configuration or archive rejected")
	ErrUnconfirmed = errors.New("platform backup or recovery unconfirmed; retain staged resources")
	ErrQuarantined = errors.New("platform recovery quarantined; reconciliation required")
)

// Tools is operator-owned, never supplied by a public/admin HTTP request. The
// native PostgreSQL programs receive credentials in a restricted environment,
// not their argument list. Neither SQL output nor stderr is logged.
type Tools struct{ Dump, Restore string }
type Database struct {
	DSN   string
	Tools Tools
}

var databaseIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func connectionConfig(dsn string) (*pgx.ConnConfig, []string, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.Fragment != "" || u.Hostname() == "" || strings.ContainsAny(u.Hostname(), ", \\/") || strings.Count(u.Path, "/") != 1 || len(u.Path) < 2 {
		return nil, nil, ErrInvalid
	}
	password, ok := u.User.Password()
	if !ok || password == "" || u.User.Username() == "" {
		return nil, nil, ErrInvalid
	}
	if !databaseIdentifier.MatchString(strings.TrimPrefix(u.Path, "/")) {
		return nil, nil, ErrInvalid
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, nil, ErrInvalid
	}
	mode := query.Get("sslmode")
	if mode != "disable" && mode != "verify-full" {
		return nil, nil, ErrInvalid
	}
	env := []string{"PGHOST=" + u.Hostname(), "PGUSER=" + u.User.Username(), "PGPASSWORD=" + password, "PGDATABASE=" + strings.TrimPrefix(u.Path, "/"), "PGSSLMODE=" + mode, "PGCONNECT_TIMEOUT=10", "PGAPPNAME=frogim-directory-backup", "PGOPTIONS=-c search_path=public", "PGPASSFILE=" + os.DevNull}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, nil, ErrInvalid
	}
	env = append(env, "PGPORT="+port)
	for k, values := range query {
		if len(values) != 1 {
			return nil, nil, ErrInvalid
		}
		switch k {
		case "sslmode":
		case "sslrootcert", "sslcert", "sslkey":
			if mode != "verify-full" || !filepath.IsAbs(values[0]) {
				return nil, nil, ErrInvalid
			}
			env = append(env, "PG"+strings.ToUpper(k)+"="+values[0])
		default:
			return nil, nil, ErrInvalid
		}
	}
	if mode == "verify-full" && query.Get("sslrootcert") == "" {
		return nil, nil, ErrInvalid
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, nil, ErrInvalid
	}
	config.RuntimeParams = map[string]string{"search_path": "public", "application_name": "frogim-directory-backup", "lock_timeout": "5000"}
	config.ConnectTimeout = 10 * time.Second
	return config, env, nil
}

func (d Database) connect(ctx context.Context) (*pgx.Conn, error) {
	c, _, e := connectionConfig(d.DSN)
	if e != nil {
		return nil, e
	}
	conn, e := pgx.ConnectConfig(ctx, c)
	if e != nil {
		return nil, ErrUnconfirmed
	}
	return conn, nil
}
func (d Database) run(ctx context.Context, restore bool, input io.Reader, output io.Writer, args ...string) error {
	_, env, err := connectionConfig(d.DSN)
	if err != nil {
		return err
	}
	binary := d.Tools.Dump
	if restore {
		binary = d.Tools.Restore
	}
	info, err := os.Stat(binary)
	if err != nil || !filepath.IsAbs(binary) || !info.Mode().IsRegular() || strings.HasSuffix(strings.ToLower(binary), ".cmd") || strings.HasSuffix(strings.ToLower(binary), ".bat") {
		return ErrInvalid
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = 2 * time.Second
	for _, k := range []string{"PATH", "SYSTEMROOT", "WINDIR", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+value)
		}
	}
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, input, output, io.Discard
	if cmd.Run() != nil {
		return ErrUnconfirmed
	}
	return nil
}

// No other code may use conn during work. Losing the migration/backup-lock
// connection cancels the dump/restore and prevents final confirmation.
func watched(ctx context.Context, conn *pgx.Conn, work func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop, done := make(chan struct{}), make(chan error, 1)
	go func() {
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				done <- nil
				return
			case <-ctx.Done():
				done <- ErrUnconfirmed
				return
			case <-tick.C:
				check, end := context.WithTimeout(ctx, 2*time.Second)
				e := conn.Ping(check)
				end()
				if e != nil {
					cancel()
					done <- ErrUnconfirmed
					return
				}
			}
		}
	}()
	e := work(ctx)
	close(stop)
	if <-done != nil || e != nil || conn.Ping(ctx) != nil {
		return ErrUnconfirmed
	}
	return nil
}
