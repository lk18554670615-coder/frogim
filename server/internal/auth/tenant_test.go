package auth

import (
	"github.com/linli/im/server/internal/tenancy"
	"testing"
	"time"
)

func TestTenantTokenCannotCrossRealmEvenWithSameKey(t *testing.T) {
	i := tenancy.Identity{AccountID: "account", TenantID: "a", LocalUserID: "local", AssignmentVersion: 2}
	a := Manager{Secret: []byte("01234567890123456789012345678901"), AccessTTL: time.Minute, RefreshTTL: time.Hour, TenantID: "a", Identity: i, TenantAuthVersion: 3, TenantRealmVersion: 1}
	access, _, _, err := a.IssueDeviceSession("local", "web")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := a.ParseClaims(access, "access")
	if err != nil || claims.AssignmentVersion != 2 || claims.PlatformAccountID != "account" || claims.AuthVersion != 3 || claims.RealmVersion != 1 {
		t.Fatal(claims, err)
	}
	for _, tenant := range []string{"b", ""} {
		b := a
		b.TenantID = tenant
		if _, err = b.ParseClaims(access, "access"); err == nil {
			t.Fatalf("accepted tenant a token in %q", tenant)
		}
	}
	standalone := a
	standalone.TenantID = ""
	standalone.Identity = tenancy.Identity{}
	legacy, _, err := standalone.Issue("local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.ParseClaims(legacy, "access"); err == nil {
		t.Fatal("legacy token bypassed enterprise binding")
	}
	missing := a
	missing.TenantAuthVersion = 0
	if _, _, _, err = missing.IssueDeviceSession("local", "web"); err == nil {
		t.Fatal("signed tenant token without authentication version")
	}
	missing = a
	missing.Identity = tenancy.Identity{}
	if _, _, _, err = missing.IssueDeviceSession("local", "web"); err == nil {
		t.Fatal("signed token without binding")
	}
}
