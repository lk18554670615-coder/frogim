package deployment

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

func TestSharedEnterpriseBundleLocalDocker(t *testing.T) {
	if os.Getenv("TENANCY_SHARED_BUNDLE_TEST") != "local" {
		t.Skip("shared Docker fixture not explicitly enabled")
	}
	enterpriseBundleDocker(t, true)
}

// The shared project has a fixed name: refuse to run over ANY existing local
// shared installation. Cleanup owns only the new, synthetic fixture resources.
func startSharedFixture(t *testing.T, r *ComposeRunner, c EnterpriseConfig) {
	t.Helper()
	ctx := t.Context()
	for _, args := range [][]string{
		{"ps", "-aq", "--filter", "label=com.docker.compose.project=" + tenancy.SharedDataProject},
		{"volume", "ls", "-q", "--filter", "label=com.docker.compose.project=" + tenancy.SharedDataProject},
		{"network", "ls", "-q", "--filter", "name=" + tenancy.SharedDataNetwork},
		{"ps", "-aq", "--filter", "label=com.docker.compose.project=" + r.Project},
		{"volume", "ls", "-q", "--filter", "label=com.docker.compose.project=" + r.Project},
	} {
		out, err := r.command(ctx, nil, args...)
		if err != nil || strings.TrimSpace(string(out)) != "" {
			t.Fatal("shared fixture requires absent projects; existing data preserved")
		}
	}
	admin, _ := tenancy.Secret()
	platform, _ := tenancy.Secret()
	raw, err := BuildSharedDatastoreBundle(SharedHostConfig{ServerID: c.ServerID, AdminSecret: admin, PlatformDatabaseSecret: platform, EnterpriseDatabaseSecret: c.Secrets.Database, RedisSecret: c.Secrets.Redis})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "shared.json")
	if writePrivate(file, raw) != nil {
		t.Fatal("fixture config")
	}
	// Register first so enterprise and restored-volume cleanup runs before this.
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if r.sharedNetwork(cleanup) != nil {
			t.Error("refusing foreign shared cleanup")
			return
		}
		for _, service := range []string{"shared-postgres", "shared-redis"} {
			if _, err := r.sharedStore(cleanup, service); err != nil {
				t.Error("shared fixture identity changed")
				return
			}
		}
		if _, err := r.command(cleanup, nil, "compose", "-f", file, "down", "--volumes", "--timeout", "2"); err != nil {
			t.Error("shared fixture cleanup")
		}
	})
	if _, err := r.command(ctx, nil, "compose", "-f", file, "up", "-d", "--pull", "never", "--wait", "--wait-timeout", "90"); err != nil {
		t.Fatal("shared infrastructure fixture start")
	}
	pg, err := r.sharedStore(ctx, "shared-postgres")
	if err != nil {
		t.Fatal(err)
	}
	cache, err := r.sharedStore(ctx, "shared-redis")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.command(ctx, nil, "exec", pg, "psql", "-X", "-U", "platform", "-d", "platform", "-v", "ON_ERROR_STOP=1", "-c", "CREATE TABLE shared_sentinel(value text); INSERT INTO shared_sentinel VALUES('platform-kept');"); err != nil {
		t.Fatal("platform sentinel")
	}
	if _, err = r.command(ctx, nil, "exec", cache, "sh", "-c", `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli -n 0 SET shared-sentinel platform-kept`); err != nil {
		t.Fatal("platform redis sentinel")
	}
	if _, err = r.command(ctx, nil, "exec", cache, "sh", "-c", `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli -n 1 SET shared-sentinel enterprise-kept`); err != nil {
		t.Fatal("enterprise redis sentinel")
	}
	// Registered after infrastructure cleanup, before enterprise fixture cleanup.
	// It also catches accidentally stopped shared Redis after enterprise backup.
	t.Cleanup(func() {
		check, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for service, want := range map[string]string{"shared-postgres": pg, "shared-redis": cache} {
			got, err := r.sharedStore(check, service)
			if err != nil || got != want {
				t.Error("enterprise maintenance changed shared instance")
			}
		}
		out, err := r.command(check, nil, "exec", pg, "psql", "-X", "-U", "platform", "-d", "platform", "-Atc", "SELECT value FROM shared_sentinel")
		if err != nil || strings.TrimSpace(string(out)) != "platform-kept" {
			t.Error("platform PG changed")
		}
		out, err = r.command(check, nil, "exec", cache, "sh", "-c", `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli -n 0 GET shared-sentinel`)
		if err != nil || strings.TrimSpace(string(out)) != "platform-kept" {
			t.Error("platform Redis DB0 changed")
		}
		if !t.Failed() && os.Getenv("TENANCY_COLD_BACKUP_TEST") == "local" {
			for _, db := range []string{"enterprise", "enterprise_r_restored"} {
				out, err := r.command(check, nil, "exec", pg, "psql", "-X", "-U", "enterprise", "-d", db, "-Atc", "SELECT count(*) FROM im_users WHERE id='local-fixture'")
				if err != nil || strings.TrimSpace(string(out)) != "1" {
					t.Error("original/restored PG identity missing", db)
				}
			}
			for _, db := range []string{"1", "2"} {
				out, err := r.command(check, nil, "exec", cache, "sh", "-c", `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli -n "$1" GET shared-sentinel`, "--", db)
				if err != nil || strings.TrimSpace(string(out)) != "enterprise-kept" {
					t.Error("original/restored Redis missing", db)
				}
			}
		}
	})
}
