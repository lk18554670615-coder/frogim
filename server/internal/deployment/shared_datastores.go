package deployment

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/linli/im/server/internal/tenancy"
)

// SharedDatastores binds only the default enterprise. Restore creates a fresh
// database and chooses a previously unused logical Redis database (1..15).
type SharedDatastores struct {
	Database string `json:"database"`
	RedisDB  int    `json:"redisDb"`
}

func (s *SharedDatastores) valid(tenant string) bool {
	return s != nil && tenant == "default" && tenancy.ValidSharedDatabase(s.Database) && s.RedisDB >= 1 && s.RedisDB <= 15
}
func sharedBundle(b Bundle) bool {
	return b.Services["enterprise-api"].Environment["IM_DATASTORE_MODE"] == tenancy.SharedDatastoreMode
}
func validateSharedBundle(b Bundle, r Release) error {
	api := b.Services["enterprise-api"]
	n := b.Networks["shared-data"]
	if r.TenantID != "default" || api.Environment["IM_TENANT_ID"] != "default" || len(b.Services) != 7 || len(b.Volumes) != 4 || len(b.Networks) != 3 || !n.External || n.Internal || n.Name != tenancy.SharedDataNetwork || tenancy.DeploymentDatastores(api.Environment["IM_DATABASE_URL"], api.Environment["IM_REDIS_URL"], "enterprise", r.TenantID, tenancy.SharedDatastoreMode) != nil {
		return ErrBundle
	}
	attached := 0
	for name, s := range b.Services {
		if name == "enterprise-db" || name == "enterprise-redis" {
			return ErrBundle
		}
		for _, network := range s.Networks {
			if network == "shared-data" {
				if name != "enterprise-api" {
					return ErrBundle
				}
				attached++
			}
		}
	}
	if attached != 1 {
		return ErrBundle
	}
	_, err := coldVolumeSources(b)
	return err
}
func sharedDatabase(b Bundle) string {
	u, e := url.Parse(b.Services["enterprise-api"].Environment["IM_DATABASE_URL"])
	if e != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}
func sharedRedisDB(b Bundle) int {
	u, e := url.Parse(b.Services["enterprise-api"].Environment["IM_REDIS_URL"])
	if e != nil {
		return -1
	}
	n, e := strconv.Atoi(strings.TrimPrefix(u.Path, "/"))
	if e != nil {
		return -1
	}
	return n
}
func attachSharedEnterprise(b *Bundle, c EnterpriseConfig) {
	delete(b.Services, "enterprise-db")
	delete(b.Services, "enterprise-redis")
	delete(b.Volumes, "postgres")
	delete(b.Volumes, "redis")
	b.Networks["shared-data"] = LocalNetwork{External: true, Name: tenancy.SharedDataNetwork}
	s := b.Services["enterprise-api"]
	s.Networks = append(s.Networks, "shared-data")
	delete(s.DependsOn, "enterprise-db")
	delete(s.DependsOn, "enterprise-redis")
	b.Services["enterprise-api"] = s
}
func (r *ComposeRunner) sharedNetwork(ctx context.Context) error {
	out, e := r.command(ctx, nil, "network", "inspect", "--format", "{{json .Labels}}", tenancy.SharedDataNetwork)
	var labels map[string]string
	if e != nil || json.Unmarshal(out, &labels) != nil || labels["com.docker.compose.project"] != tenancy.SharedDataProject || labels["io.frogim.scope"] != "shared-default" || labels["io.frogim.server"] != r.Server || r.Tenant != "default" {
		return ErrUnconfirmed
	}
	return nil
}

// Resolve shared stores separately; they never belong to the enterprise's
// lifecycle or its volume set. No enterprise operation starts or stops them.
func (r *ComposeRunner) sharedStore(ctx context.Context, service string) (string, error) {
	if (service != "shared-postgres" && service != "shared-redis") || r.sharedNetwork(ctx) != nil {
		return "", ErrUnconfirmed
	}
	out, e := r.command(ctx, nil, "ps", "--filter", "label=com.docker.compose.project="+tenancy.SharedDataProject, "--filter", "label=com.docker.compose.service="+service, "--format", "{{.ID}}")
	ids := strings.Fields(string(out))
	if e != nil || len(ids) != 1 {
		return "", ErrUnconfirmed
	}
	out, e = r.command(ctx, nil, "inspect", "--format", `{"id":{{json .Id}},"labels":{{json .Config.Labels}},"running":{{.State.Running}},"health":{{with .State.Health}}{{json .Status}}{{else}}""{{end}},"networks":{{json .NetworkSettings.Networks}}}`, ids[0])
	var state struct {
		ID       string
		Labels   map[string]string
		Running  bool
		Health   string
		Networks map[string]json.RawMessage
	}
	if e != nil || json.Unmarshal(out, &state) != nil || !state.Running || state.Health != "healthy" || state.Labels["io.frogim.scope"] != "shared-default" || state.Labels["io.frogim.server"] != r.Server || state.Networks[tenancy.SharedDataNetwork] == nil {
		return "", ErrUnconfirmed
	}
	return state.ID, nil
}
