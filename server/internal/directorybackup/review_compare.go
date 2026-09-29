package directorybackup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/linli/im/server/internal/tenancy"
)

type reviewAccount struct {
	ID                 string `json:"id"`
	Phone              string `json:"phone"`
	TenantID           string `json:"tenantId"`
	LocalUserID        string `json:"localUserId"`
	AssignmentVersion  int64  `json:"assignmentVersion"`
	AuthVersion        int64  `json:"authVersion"`
	State              string `json:"state"`
	CredentialsPending bool   `json:"credentialsPending"`
	GloballyBlocked    bool   `json:"globallyBlocked"`
}
type reviewTenant struct {
	ID           string `json:"id"`
	HTTPBaseURL  string `json:"httpBaseUrl"`
	State        string `json:"state"`
	RealmVersion int64  `json:"realmVersion"`
}
type reviewSource struct {
	Accounts []reviewAccount `json:"accounts"`
	Tenants  []reviewTenant  `json:"tenants"`
	Pending  map[string]int  `json:"pending"`
}

// Issues deliberately omit phone numbers, passwords, nicknames and business
// data. Full identity evidence is kept only in the private quarantined DB.
type ReviewIssue struct {
	Code        string `json:"code"`
	TenantID    string `json:"tenantId,omitempty"`
	AccountID   string `json:"accountId,omitempty"`
	LocalUserID string `json:"localUserId,omitempty"`
	Count       int    `json:"count,omitempty"`
}
type ReviewEvidence struct {
	Inventory tenancy.RecoveryInventory `json:"inventory"`
	Digest    string                    `json:"digest"`
}

func reviewHash(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func compareRecovery(source reviewSource, evidence []ReviewEvidence) []ReviewIssue {
	issues := []ReviewIssue{}
	add := func(code, tenant, account, local string, count int) {
		issues = append(issues, ReviewIssue{code, tenant, account, local, count})
	}
	// Even equal enterprise versions cannot prove that a platform-only password
	// change or ban was not committed after the snapshot and lost in the disaster.
	add("CREDENTIAL_REVERIFICATION_REQUIRED", "", "", "", len(source.Accounts))
	add("OLD_AUTHORITY_FENCING_REQUIRED", "", "", "", 0)
	for kind, count := range source.Pending {
		if count > 0 {
			add("PLATFORM_PENDING_"+kind, "", "", "", count)
		}
	}
	tenants := map[string]reviewTenant{}
	accounts := map[string]reviewAccount{}
	phones := map[string]string{}
	for _, a := range source.Accounts {
		accounts[a.ID] = a
		phones[a.Phone] = a.ID
	}
	for _, tenant := range source.Tenants {
		tenants[tenant.ID] = tenant
	}
	seenTenant := map[string]bool{}
	seenExact := map[string]bool{}
	liveAccounts := map[string][]tenancy.RecoveryIdentity{}
	livePhones := map[string]map[string]bool{}
	for _, entry := range evidence {
		r := entry.Inventory
		seenTenant[r.TenantID] = true
		tenant, known := tenants[r.TenantID]
		if !known {
			add("TENANT_NOT_IN_SNAPSHOT", r.TenantID, "", "", 0)
		} else {
			if tenant.HTTPBaseURL != r.HTTPBaseURL {
				add("TENANT_ADDRESS_CHANGED", r.TenantID, "", "", 0)
			}
			if tenant.RealmVersion != r.Realm.Version || tenant.State != "suspended" {
				add("TENANT_REALM_CHANGED", r.TenantID, "", "", 0)
			}
		}
		if r.UnlinkedUsers > 0 {
			add("UNLINKED_ENTERPRISE_IDENTITIES", r.TenantID, "", "", r.UnlinkedUsers)
		}
		for kind, count := range map[string]int{"REVOCATIONS": r.Pending.Revocations, "CREDENTIALS": r.Pending.Credentials, "ACCESS": r.Pending.Access, "REALM": r.Pending.Realm, "IMPORTS": r.Pending.Imports, "MEDIA": r.Pending.Media} {
			if count > 0 {
				add("ENTERPRISE_PENDING_"+kind, r.TenantID, "", "", count)
			}
		}
		for _, u := range r.Users {
			i := u.Identity
			if u.State != "retired" && !u.Deleted {
				liveAccounts[i.AccountID] = append(liveAccounts[i.AccountID], u)
				if livePhones[u.Phone] == nil {
					livePhones[u.Phone] = map[string]bool{}
				}
				livePhones[u.Phone][i.AccountID] = true
			}
			a, ok := accounts[i.AccountID]
			if !ok {
				add("ACCOUNT_NOT_IN_SNAPSHOT", r.TenantID, i.AccountID, i.LocalUserID, 0)
			} else {
				if a.TenantID == r.TenantID && a.LocalUserID == i.LocalUserID {
					seenExact[a.ID] = true
				}
				if a.TenantID != r.TenantID || a.LocalUserID != i.LocalUserID || a.AssignmentVersion != i.AssignmentVersion {
					// Old retired identities are expected history, not live grants.
					if u.State != "retired" || a.AssignmentVersion <= i.AssignmentVersion {
						add("ASSIGNMENT_CHANGED", r.TenantID, i.AccountID, i.LocalUserID, 0)
					}
				} else {
					if a.AuthVersion != u.AuthVersion {
						add("AUTH_VERSION_CHANGED", r.TenantID, i.AccountID, i.LocalUserID, 0)
					}
					if a.Phone != u.Phone {
						add("PHONE_CHANGED", r.TenantID, i.AccountID, i.LocalUserID, 0)
					}
					if u.State != "active" || u.Deleted || u.EnterpriseBanned || a.State != "active" || a.CredentialsPending || a.GloballyBlocked {
						add("IDENTITY_RESTRICTED_OR_UNSETTLED", r.TenantID, i.AccountID, i.LocalUserID, 0)
					}
				}
			}
			if other, ok := phones[u.Phone]; ok && other != i.AccountID && u.State != "retired" && !u.Deleted {
				add("PHONE_ACCOUNT_COLLISION", r.TenantID, i.AccountID, i.LocalUserID, 0)
			}
		}
	}
	for _, t := range source.Tenants {
		if !seenTenant[t.ID] {
			add("ENTERPRISE_EVIDENCE_MISSING", t.ID, "", "", 0)
		}
	}
	for _, a := range source.Accounts {
		if !seenExact[a.ID] {
			add("SNAPSHOT_IDENTITY_NOT_FOUND", a.TenantID, a.ID, a.LocalUserID, 0)
		}
	}
	for id, users := range liveAccounts {
		if len(users) > 1 {
			add("MULTIPLE_CURRENT_IDENTITIES", "", id, "", len(users))
		}
		for _, u := range users {
			if len(livePhones[u.Phone]) > 1 {
				add("LIVE_PHONE_ACCOUNT_COLLISION", u.Identity.TenantID, id, u.Identity.LocalUserID, 0)
			}
		}
	}
	sort.Slice(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		if a.AccountID != b.AccountID {
			return a.AccountID < b.AccountID
		}
		return a.LocalUserID < b.LocalUserID
	})
	return issues
}
