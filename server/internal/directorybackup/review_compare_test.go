package directorybackup

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
)

func reviewFixtures() (reviewSource, []ReviewEvidence) {
	a := reviewAccount{ID: "account", Phone: "19911112222", TenantID: "default", LocalUserID: "user", AssignmentVersion: 1, AuthVersion: 1, State: "active"}
	s := reviewSource{Accounts: []reviewAccount{a}, Tenants: []reviewTenant{{ID: "default", HTTPBaseURL: "https://default.example.test", State: "suspended", RealmVersion: 2}}, Pending: map[string]int{}}
	i := tenancy.RecoveryIdentity{Identity: tenancy.Identity{AccountID: a.ID, TenantID: a.TenantID, LocalUserID: a.LocalUserID, AssignmentVersion: 1}, Phone: a.Phone, AuthVersion: 1, State: "active"}
	r := tenancy.RecoveryInventory{Version: 1, TenantID: "default", HTTPBaseURL: s.Tenants[0].HTTPBaseURL, Realm: tenancy.RealmSnapshot{Version: 2, SuspensionConfirmed: true}, Users: []tenancy.RecoveryIdentity{i}}
	d, _ := r.Digest()
	return s, []ReviewEvidence{{r, d}}
}
func TestRecoveryReviewComparison(t *testing.T) {
	for _, scenario := range []string{"equal", "new-account", "moved", "new-auth", "restricted", "missing", "new-tenant", "missing-tenant", "pending", "collision", "duplicate-live", "retired-history", "phone-change"} {
		t.Run(scenario, func(t *testing.T) {
			s, e := reviewFixtures()
			want := "CREDENTIAL_REVERIFICATION_REQUIRED"
			switch scenario {
			case "new-account":
				e[0].Inventory.Users[0].Identity.AccountID = "unknown"
				want = "ACCOUNT_NOT_IN_SNAPSHOT"
			case "moved":
				e[0].Inventory.Users[0].Identity.AssignmentVersion = 2
				want = "ASSIGNMENT_CHANGED"
			case "new-auth":
				e[0].Inventory.Users[0].AuthVersion = 2
				want = "AUTH_VERSION_CHANGED"
			case "restricted":
				e[0].Inventory.Users[0].State = "platform_blocked"
				want = "IDENTITY_RESTRICTED_OR_UNSETTLED"
			case "missing":
				e[0].Inventory.Users = nil
				want = "SNAPSHOT_IDENTITY_NOT_FOUND"
			case "new-tenant":
				s.Tenants = nil
				want = "TENANT_NOT_IN_SNAPSHOT"
			case "missing-tenant":
				e = nil
				want = "ENTERPRISE_EVIDENCE_MISSING"
			case "pending":
				s.Pending["IDENTITY_JOBS"] = 1
				e[0].Inventory.Pending.Media = 1
				want = "ENTERPRISE_PENDING_MEDIA"
			case "collision":
				s.Accounts[0].ID = "other"
				want = "PHONE_ACCOUNT_COLLISION"
			case "duplicate-live":
				u := e[0].Inventory.Users[0]
				u.Identity.LocalUserID = "user2"
				e[0].Inventory.Users = append(e[0].Inventory.Users, u)
				want = "MULTIPLE_CURRENT_IDENTITIES"
			case "retired-history":
				s.Accounts[0].AssignmentVersion = 2
				e[0].Inventory.Users[0].Identity.AssignmentVersion = 2
				u := e[0].Inventory.Users[0]
				u.Identity.LocalUserID = "old"
				u.Identity.AssignmentVersion = 1
				u.State = "retired"
				e[0].Inventory.Users = append(e[0].Inventory.Users, u)
			case "phone-change":
				e[0].Inventory.Users[0].Phone = "19922223333"
				want = "PHONE_CHANGED"
			}
			issues := compareRecovery(s, e)
			found := false
			for _, i := range issues {
				if i.Code == want {
					found = true
				}
			}
			if !found {
				t.Fatal("missing required issue", want)
			}
			if (scenario == "equal" || scenario == "retired-history") && len(issues) != 2 {
				t.Fatal("unexpected findings", issues)
			}
			raw, _ := json.Marshal(issues)
			if strings.Contains(string(raw), "19911112222") || strings.Contains(string(raw), "19922223333") {
				t.Fatal("report leaks phone")
			}
			if reviewHash(issues) != reviewHash(compareRecovery(s, e)) {
				t.Fatal("nondeterministic evidence")
			}
		})
	}
}
