package deployment

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"

	"github.com/linli/im/server/internal/tenancy"
)

//go:embed shared_init.sh
var SharedPostgresInit string

type SharedHostConfig struct {
	ServerID                 string `json:"serverId"`
	AdminSecret              string `json:"adminSecret"`
	PlatformDatabaseSecret   string `json:"platformDatabaseSecret"`
	EnterpriseDatabaseSecret string `json:"enterpriseDatabaseSecret"`
	RedisSecret              string `json:"redisSecret"`
}

// The infrastructure is explicitly outside both application lifecycles.
// Fixed private network, no published datastore ports, no tenant-supplied URLs.
func BuildSharedDatastoreBundle(c SharedHostConfig) ([]byte, error) {
	if !tenancy.ValidID(c.ServerID) {
		return nil, ErrBundle
	}
	seen := map[string]bool{}
	for _, v := range []string{c.AdminSecret, c.PlatformDatabaseSecret, c.EnterpriseDatabaseSecret, c.RedisSecret} {
		b, e := base64.RawURLEncoding.DecodeString(v)
		if e != nil || len(b) != 32 || seen[v] {
			return nil, ErrBundle
		}
		seen[v] = true
	}
	labels := map[string]string{"io.frogim.scope": "shared-default", "io.frogim.server": c.ServerID}
	b := map[string]any{
		"name":     tenancy.SharedDataProject,
		"networks": map[string]any{"data": map[string]any{"internal": true, "labels": labels}},
		"volumes":  map[string]any{"postgres": map[string]any{"labels": labels}, "redis": map[string]any{"labels": labels}},
		"services": map[string]any{
			"shared-postgres": map[string]any{
				"image":    "postgres@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193",
				"platform": "linux/amd64", "restart": "unless-stopped", "networks": []string{"data"}, "labels": labels,
				"volumes":     []string{"postgres:/var/lib/postgresql/data"},
				"environment": map[string]string{"POSTGRES_USER": "shared_admin", "POSTGRES_DB": "postgres", "POSTGRES_PASSWORD": c.AdminSecret, "PLATFORM_DATABASE_PASSWORD": c.PlatformDatabaseSecret, "ENTERPRISE_DATABASE_PASSWORD": c.EnterpriseDatabaseSecret, "FROGIM_SHARED_INIT": SharedPostgresInit},
				"entrypoint":  []string{"sh", "-c", `printf '%s' "$FROGIM_SHARED_INIT" > /docker-entrypoint-initdb.d/10-frogim.sh && exec /usr/local/bin/docker-entrypoint.sh postgres`},
				"healthcheck": map[string]any{"test": []string{"CMD-SHELL", `pg_isready -h 127.0.0.1 -U shared_admin -d postgres && psql -X -U shared_admin -d postgres -Atc "SELECT count(*)=2 FROM pg_database WHERE datname IN ('platform','enterprise')" | grep -qx t`}, "interval": "2s", "timeout": "3s", "retries": 40},
			},
			"shared-redis": map[string]any{
				"image":    "redis@sha256:978f0e01593e65eed801f2402944efcd936d43b5027e4908a7897baf88ed6241",
				"platform": "linux/amd64", "restart": "unless-stopped", "networks": []string{"data"}, "labels": labels,
				"volumes": []string{"redis:/data"}, "environment": map[string]string{"REDIS_PASSWORD": c.RedisSecret},
				"command":     []string{"sh", "-c", `exec redis-server --appendonly yes --databases 16 --requirepass "$REDIS_PASSWORD"`},
				"healthcheck": map[string]any{"test": []string{"CMD-SHELL", `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli ping | grep -qx PONG`}, "interval": "2s", "timeout": "3s", "retries": 40},
			},
		},
	}
	raw, e := json.MarshalIndent(b, "", "  ")
	return bytes.ReplaceAll(raw, []byte("$"), []byte("$$")), e
}
