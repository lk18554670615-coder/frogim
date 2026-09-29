package tenancy

import (
	"strings"
	"testing"
)

func TestSharedDatastoreBindings(t *testing.T) {
	db := "postgres://enterprise:" + strings.Repeat("a", 43) + "@shared-postgres:5432/enterprise?sslmode=disable"
	cache := "redis://:" + strings.Repeat("b", 43) + "@shared-redis:6379/1"
	if DeploymentDatastores(db, cache, "enterprise", "default", SharedDatastoreMode) != nil {
		t.Fatal("valid default binding")
	}
	if DeploymentDatastores(strings.Replace(db, "/enterprise?", "/enterprise_r_restore_one?", 1), strings.Replace(cache, "/1", "/2", 1), "enterprise", "default", SharedDatastoreMode) != nil {
		t.Fatal("restore binding")
	}
	for _, tc := range []struct{ db, cache, role, tenant, mode string }{
		{db, cache, "enterprise", "other", SharedDatastoreMode},
		{db, strings.Replace(cache, "/1", "/0", 1), "enterprise", "default", SharedDatastoreMode},
		{strings.Replace(db, "/enterprise?", "/platform?", 1), cache, "enterprise", "default", SharedDatastoreMode},
		{db, cache, "enterprise", "default", "unknown"},
		{strings.Replace(db, "shared-postgres", "remote-db", 1), cache, "enterprise", "default", SharedDatastoreMode},
		{db, strings.Replace(cache, "/1", "/16", 1), "enterprise", "default", SharedDatastoreMode},
		{db, strings.Replace(cache, "redis://:", "redis://tenant:", 1), "enterprise", "default", SharedDatastoreMode},
	} {
		if DeploymentDatastores(tc.db, tc.cache, tc.role, tc.tenant, tc.mode) == nil {
			t.Fatal("invalid shared profile accepted")
		}
	}
	pdb := strings.ReplaceAll(db, "enterprise", "platform")
	if DeploymentDatastores(pdb, strings.Replace(cache, "/1", "/0", 1), "platform", "", SharedDatastoreMode) != nil {
		t.Fatal("platform binding")
	}
	if DeploymentDatastores(pdb, cache, "platform", "", SharedDatastoreMode) == nil {
		t.Fatal("platform selected enterprise Redis DB")
	}
}
