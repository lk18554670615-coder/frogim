package deployment

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/linli/im/server/internal/backup"
)

func TestColdBackupDataMappingAndIndependentKey(t *testing.T) {
	c := bundleTestConfig(t)
	raw, _, e := BuildEnterpriseBundle(c, Release{ID: "one", Sequence: 1, Runtime: "linux/amd64", SchemaVersion: 79})
	if e != nil {
		t.Fatal(e)
	}
	var b Bundle
	if json.Unmarshal(raw, &b) != nil {
		t.Fatal("bundle")
	}
	sources, e := coldVolumeSources(b)
	if e != nil || len(sources) != 6 {
		t.Fatal("missing dataset")
	}
	for _, value := range []string{c.Secrets.JWT, c.Secrets.MediaSigning, c.Secrets.Database, c.Secrets.IMToken} {
		key, _ := base64.RawURLEncoding.DecodeString(value)
		if independentBackupKey(b, key) {
			t.Fatal("authentication/media key reused for backup")
		}
	}
	if !independentBackupKey(b, bytes.Repeat([]byte{37}, 32)) || independentBackupKey(b, []byte("short")) {
		t.Fatal("backup key validation")
	}
	s := b.Services["enterprise-minio"]
	s.Volumes[0].Source = sources["redis"]
	b.Services["enterprise-minio"] = s
	if _, e = coldVolumeSources(b); e == nil {
		t.Fatal("shared datasets accepted")
	}
}

func TestColdBackupCannotAdoptFreshOrActiveJournal(t *testing.T) {
	x := &Executor{runner: &ComposeRunner{}}
	if _, _, _, _, e := x.coldSource(context.Background(), backup.Binding{Generation: 1}); e == nil {
		t.Fatal("empty journal adopted")
	}
	x.state.Generation = 1
	x.state.ActiveOperationID = "working"
	if _, _, _, _, e := x.coldSource(context.Background(), backup.Binding{Generation: 1}); e == nil {
		t.Fatal("active deployment bypassed")
	}
}

func TestColdBackupSupportsBothExplicitProfiles(t *testing.T) {
	for _, production := range []bool{false, true} {
		c := bundleTestConfig(t)
		if production {
			c = productionBundleFixture(t)
		}
		raw, prior, e := BuildEnterpriseBundle(c, Release{ID: "one", Sequence: 1, Runtime: "linux/amd64", SchemaVersion: 79})
		if e != nil {
			t.Fatal(e)
		}
		var b Bundle
		if json.Unmarshal(raw, &b) != nil {
			t.Fatal("decode")
		}
		expected := backup.Binding{TenantID: c.TenantID, SchemaVersion: 79}
		if validateColdBundle(b, expected) != nil {
			t.Fatal("valid profile rejected", production)
		}
		prior.RollbackTo = []string{"old-volumes"}
		restored := restoredRelease(prior, "restored", 2, c.TenantID, c.ServerID)
		restored.ComposeSHA256 = prior.ComposeSHA256
		if !restored.Valid() || restored.IsolationMode != prior.IsolationMode || len(restored.RollbackTo) != 0 || validateBundle(b, restored) != nil {
			t.Fatal("restore lost production profile or reused old rollback volumes")
		}
		env := b.Services["enterprise-api"].Environment
		env["IM_TENANCY_PREVIEW"] = "false"
		env["IM_ENV"] = "development"
		if validateColdBundle(b, expected) == nil {
			t.Fatal("mixed production/preview accepted")
		}
	}
}
