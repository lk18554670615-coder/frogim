package deployment

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
)

func TestSharedEnterpriseBundleBoundaries(t *testing.T) {
	c := bundleTestConfig(t)
	c.TenantID = "default"
	c.ControlTLS = bundleTestPKI(t)(tenancy.EnterpriseIdentity("default"))
	c.SharedDatastores = &SharedDatastores{Database: "enterprise", RedisDB: 1}
	raw, r, err := BuildEnterpriseBundle(c, Release{ID: "shared-one", Sequence: 1, Runtime: "linux/amd64", SchemaVersion: 79})
	if err != nil {
		t.Fatal(err)
	}
	var b Bundle
	if json.Unmarshal(raw, &b) != nil || validateBundle(b, r) != nil || len(b.Services) != 7 || len(b.Volumes) != 4 || r.DatastoreMode != tenancy.SharedDatastoreMode {
		t.Fatal("shared graph")
	}
	if len(backupVolumes(b)) != 4 {
		t.Fatal("shared Redis volume exposed to cold backup")
	}
	for _, password := range []string{c.Secrets.Database, c.Secrets.Redis} {
		key, _ := base64.RawURLEncoding.DecodeString(password)
		if independentBackupKey(b, key) {
			t.Fatal("shared DSN password accepted as archive key")
		}
	}
	for _, mutate := range []func(*Bundle){
		func(b *Bundle) {
			s := b.Services["enterprise-minio"]
			s.Networks = append(s.Networks, "shared-data")
			b.Services["enterprise-minio"] = s
		},
		func(b *Bundle) { delete(b.Networks, "shared-data") },
		func(b *Bundle) {
			s := b.Services["enterprise-api"]
			s.Environment["IM_REDIS_URL"] = "redis://:invalid@shared-redis:6379/0"
			b.Services["enterprise-api"] = s
		},
	} {
		var bad Bundle
		_ = json.Unmarshal(raw, &bad)
		mutate(&bad)
		if validateBundle(bad, r) == nil {
			t.Fatal("invalid shared bundle accepted")
		}
	}
	c.TenantID = "other"
	if c.Validate() == nil {
		t.Fatal("remote tenant may share platform stores")
	}
}

func TestSharedPlatformAndInfrastructureGraph(t *testing.T) {
	c := platformBundleFixture(t)
	c.SharedDatastores = true
	raw, _, err := BuildPlatformBundle(c, "shared-platform")
	var b Bundle
	if err != nil || json.Unmarshal(bytes.ReplaceAll(raw, []byte("$$"), []byte("$")), &b) != nil || len(b.Services) != 2 || len(b.Volumes) != 0 || !b.Networks["shared-data"].External {
		t.Fatal("shared platform graph", err)
	}
	admin, _ := tenancy.Secret()
	enterprise, _ := tenancy.Secret()
	raw, err = BuildSharedDatastoreBundle(SharedHostConfig{ServerID: "default-host", AdminSecret: admin, PlatformDatabaseSecret: c.DatabaseSecret, EnterpriseDatabaseSecret: enterprise, RedisSecret: c.RedisSecret})
	if err != nil {
		t.Fatal(err)
	}
	var infra struct {
		Services map[string]struct {
			Ports    []any
			Networks []string
		}
		Volumes  map[string]any
		Networks map[string]struct{ Internal bool }
	}
	if json.Unmarshal(raw, &infra) != nil || len(infra.Services) != 2 || len(infra.Volumes) != 2 || !infra.Networks["data"].Internal {
		t.Fatal("shared infra graph")
	}
	for _, s := range infra.Services {
		if len(s.Ports) != 0 || len(s.Networks) != 1 || s.Networks[0] != "data" {
			t.Fatal("shared store published")
		}
	}
}

func TestSharedProductionProfile(t *testing.T) {
	c := productionBundleFixture(t)
	c.TenantID = "default"
	c.ControlTLS = bundleTestPKI(t, "tenant-a.control.example.test")(tenancy.EnterpriseIdentity("default"))
	c.SharedDatastores = &SharedDatastores{Database: "enterprise", RedisDB: 1}
	raw, r, err := BuildEnterpriseBundle(c, Release{ID: "shared-production", Sequence: 1, Runtime: "linux/amd64", SchemaVersion: 79})
	if err != nil {
		t.Fatal(err)
	}
	var b Bundle
	if json.Unmarshal(raw, &b) != nil || r.IsolationMode != "dedicated_host" || validateProductionBundle(b, r) != nil || validateBundle(b, r) != nil {
		t.Fatal("shared production profile")
	}
	api := b.Services["enterprise-api"].Environment
	if tenancy.DeploymentDatastores(api["IM_DATABASE_URL"], api["IM_REDIS_URL"], "enterprise", "default", api["IM_DATASTORE_MODE"]) != nil {
		t.Fatal("shared production binding")
	}
}
