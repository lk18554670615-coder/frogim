package deployment

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/tenancy"
)

func backupVolumes(b Bundle) []string {
	if sharedBundle(b) {
		return []string{"im", "im-logs", "media", "plugins"}
	}
	return coldVolumes
}
func (r *ComposeRunner) bundleDatabase(ctx context.Context, b Bundle, id string, binding backup.Binding, allow bool) error {
	name, user := "enterprise", "enterprise"
	if sharedBundle(b) {
		name, user = sharedDatabase(b), "shared_admin"
	}
	// The non-superuser business role cannot see other roles' backend_type in
	// pg_stat_activity. Operator checks must count ALL clients of this DB.
	return r.suspendedDatabaseAt(ctx, id, binding, allow, name, user)
}

func (r *ComposeRunner) redisDatabaseHelper(ctx context.Context, b Bundle, mode string, in io.Reader, out io.Writer) error {
	if !sharedBundle(b) || (mode != "export" && mode != "import") || r.sharedNetwork(ctx) != nil || sharedRedisDB(b) < 1 {
		return ErrBundle
	}
	raw := b.Services["enterprise-api"].Environment["IM_REDIS_URL"]
	if strings.ContainsAny(raw, "\r\n") {
		return ErrBundle
	}
	dir, e := os.MkdirTemp("", "frogim-redis-private-")
	if e != nil {
		return ErrUnconfirmed
	}
	path := filepath.Join(dir, "redis.env")
	defer os.Remove(dir)
	defer os.Remove(path)
	if writePrivate(path, []byte("REDIS_DATABASE_URL="+raw+"\n")) != nil {
		return ErrUnconfirmed
	}
	args := []string{"create", "--pull=never", "--network=" + tenancy.SharedDataNetwork, "--read-only", "--user=10001:10001", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--interactive", "--tmpfs=/tmp:mode=1777", "--env-file", path}
	args = append(args, r.helperLabels("redis-"+mode)...)
	if marker, ok := ctx.Value(backupHelperKey{}).(backupHelperMarker); ok && mode == "export" {
		args = append(args, "--label", "io.frogim.backup-job="+marker.Job, "--label", "io.frogim.backup-attempt="+marker.Attempt)
	}
	args = append(args, "--entrypoint", "/opt/frogim/redis-database", b.Services["enterprise-api"].Image, "-mode", mode)
	if mode == "import" {
		args = append(args, "-file", "-")
	}
	data, e := r.command(ctx, nil, args...)
	id := strings.TrimSpace(string(data))
	if e != nil || !fingerprint.MatchString(id) {
		return ErrUnconfirmed
	}
	defer r.removeHelper(id, "redis-"+mode)
	if r.stream(ctx, in, out, "start", "--attach", "--interactive", id) != nil {
		return ErrUnconfirmed
	}
	data, e = r.command(ctx, nil, "inspect", "--format", "{{.State.Status}}:{{.State.ExitCode}}", id)
	if e != nil || strings.TrimSpace(string(data)) != "exited:0" {
		return ErrUnconfirmed
	}
	return nil
}

// Restore into a new PG database and an unused Redis DB. The previous default
// enterprise AND the platform's datasets remain untouched, even on failure.
func (r *ComposeRunner) restoreSharedDatabases(ctx context.Context, b *Bundle, a *backup.Reader, expected backup.Binding, id, dir string) error {
	pg, e := r.sharedStore(ctx, "shared-postgres")
	if e != nil {
		return e
	}
	cache, e := r.sharedStore(ctx, "shared-redis")
	if e != nil {
		return e
	}
	name := "enterprise_r_" + strings.ReplaceAll(id, "-", "_")
	if !tenancy.ValidSharedDatabase(name) {
		return ErrBundle
	}
	if _, e = r.command(ctx, nil, "exec", pg, "createdb", "-U", "shared_admin", "--owner=enterprise", name); e != nil {
		return e
	}
	var index int
	for n := 2; n <= 15; n++ {
		out, e := r.command(ctx, nil, "exec", cache, "sh", "-c", `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli -n "$1" DBSIZE`, "--", strconv.Itoa(n))
		if e != nil {
			return e
		}
		if strings.TrimSpace(string(out)) != "0" {
			continue
		}
		query := "INSERT INTO frogim_shared.redis_reservations(db,database_name) VALUES (" + strconv.Itoa(n) + ",'" + name + "') ON CONFLICT DO NOTHING RETURNING db"
		out, e = r.command(ctx, nil, "exec", pg, "psql", "-X", "-qAt", "-U", "shared_admin", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c", query)
		if e != nil {
			return e
		}
		if strings.TrimSpace(string(out)) == strconv.Itoa(n) {
			index = n
			break
		}
	}
	if index == 0 {
		return ErrUnconfirmed
	}
	if writePrivate(filepath.Join(dir, "shared-restore-target.json"), []byte(`{"database":"`+name+`","redisDb":`+strconv.Itoa(index)+`}`)) != nil {
		return ErrUnconfirmed
	}
	query := "REVOKE ALL ON DATABASE " + name + " FROM PUBLIC"
	if _, e = r.command(ctx, nil, "exec", pg, "psql", "-X", "-U", "shared_admin", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c", query); e != nil {
		return e
	}
	if _, e = r.command(ctx, nil, "exec", pg, "psql", "-X", "-U", "shared_admin", "-d", name, "-v", "ON_ERROR_STOP=1", "-c", "CREATE EXTENSION IF NOT EXISTS pg_trgm; CREATE EXTENSION IF NOT EXISTS pg_stat_statements;"); e != nil {
		return e
	}
	if e = consumeArchive(a, "database", func(in io.Reader) error {
		return r.stream(ctx, in, io.Discard, "exec", "-i", pg, "pg_restore", "-U", "shared_admin", "--role=enterprise", "-d", name, "--single-transaction", "--exit-on-error", "--no-owner", "--no-acl", "--no-comments")
	}); e != nil {
		return e
	}
	if r.suspendedDatabaseAt(ctx, pg, expected, false, name, "shared_admin") != nil {
		return ErrUnconfirmed
	}
	api := b.Services["enterprise-api"]
	dbURL, e := url.Parse(api.Environment["IM_DATABASE_URL"])
	if e != nil {
		return ErrBundle
	}
	dbURL.Path = "/" + name
	redisURL, e := url.Parse(api.Environment["IM_REDIS_URL"])
	if e != nil {
		return ErrBundle
	}
	redisURL.Path = "/" + strconv.Itoa(index)
	api.Environment["IM_DATABASE_URL"], api.Environment["IM_REDIS_URL"] = dbURL.String(), redisURL.String()
	b.Services["enterprise-api"] = api
	return consumeArchive(a, "redis", func(in io.Reader) error { return r.redisDatabaseHelper(ctx, *b, "import", in, io.Discard) })
}
