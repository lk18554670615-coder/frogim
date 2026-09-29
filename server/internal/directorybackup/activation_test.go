package directorybackup

import "testing"

func TestRecoveryAccountRequiresCurrentSettledIdentity(t *testing.T) {
	for _, scenario := range []string{"verified", "password-after-snapshot", "transfer-after-snapshot", "ban-after-snapshot", "enterprise-ban", "duplicate-active", "revocation-pending", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			_, evidence := reviewFixtures()
			old := evidence[0].Inventory.Users[0]
			r := ActivationRequest{Accounts: []RecoveryAccount{{Identity: old.Identity, AuthVersion: old.AuthVersion}}}
			u := &evidence[0].Inventory.Users[0]
			switch scenario {
			case "password-after-snapshot":
				u.AuthVersion++
			case "transfer-after-snapshot":
				u.Identity.AssignmentVersion++
				u.Identity.LocalUserID = "new-user"
			case "ban-after-snapshot":
				u.State = "platform_blocked"
			case "enterprise-ban":
				u.EnterpriseBanned = true
			case "duplicate-active":
				copy := *u
				copy.Identity.LocalUserID = "duplicate"
				evidence[0].Inventory.Users = append(evidence[0].Inventory.Users, copy)
			case "revocation-pending":
				evidence[0].Inventory.Pending.Revocations = 1
			case "deleted":
				u.Deleted = true
			}
			_, err := approvedRecoveryAccounts(r, evidence)
			if (err == nil) != (scenario == "verified") {
				t.Fatal("stale/unconfirmed recovery approval accepted", err)
			}
		})
	}
}
