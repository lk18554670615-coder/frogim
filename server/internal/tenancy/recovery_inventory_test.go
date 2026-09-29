package tenancy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func recoveryFixture(n int) RecoveryInventory {
	r := RecoveryInventory{Version: 1, TenantID: "tenant-a", HTTPBaseURL: "https://a.example.test", Realm: RealmSnapshot{Version: 2, SuspensionConfirmed: true}}
	for i := range n {
		r.Users = append(r.Users, RecoveryIdentity{Identity: Identity{AccountID: fmt.Sprintf("account-%06d", i), TenantID: r.TenantID, LocalUserID: fmt.Sprintf("user-%06d", i), AssignmentVersion: 1}, AuthVersion: 1, Phone: "19911112222", State: "active"})
	}
	return r
}

func TestRecoveryInventoryPaging(t *testing.T) {
	for _, n := range []int{0, 1, 499, 500, 501, 1101} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			r := recoveryFixture(n)
			calls := 0
			result, digest, e := CollectRecoveryInventory(t.Context(), r.TenantID, r.HTTPBaseURL, 2, func(_ context.Context, request RecoveryInventoryRequest) (RecoveryInventoryPage, error) {
				calls++
				return r.Page(request)
			})
			want, _ := r.Digest()
			if e != nil || len(result.Users) != n || digest != want || calls != max(1, (n+499)/500)+1 {
				t.Fatal("collection", e, calls, len(result.Users))
			}
		})
	}
}

func TestRecoveryInventoryRejectsDriftOrForgedPage(t *testing.T) {
	for _, scenario := range []string{"nonce", "tenant", "address", "realm", "digest", "total", "cursor", "duplicate", "truncated", "final-drift", "unpaused", "unconfirmed", "negative", "oversize", "missing-cursor", "stale-digest"} {
		t.Run(scenario, func(t *testing.T) {
			r := recoveryFixture(501)
			calls := 0
			_, _, e := CollectRecoveryInventory(t.Context(), r.TenantID, r.HTTPBaseURL, 2, func(_ context.Context, request RecoveryInventoryRequest) (RecoveryInventoryPage, error) {
				calls++
				if scenario == "final-drift" && calls == 3 {
					r.Users[0].AuthVersion++
				}
				if scenario == "missing-cursor" {
					request.After = "unknown"
					request.ExpectedDigest, _ = r.Digest()
				}
				if scenario == "stale-digest" {
					request.ExpectedDigest = strings.Repeat("0", 64)
				}
				page, err := r.Page(request)
				if calls == 1 {
					switch scenario {
					case "nonce":
						page.Nonce = "stale"
					case "tenant":
						page.Inventory.TenantID = "b"
					case "address":
						page.Inventory.HTTPBaseURL = "https://b.example.test"
					case "realm":
						page.Inventory.Realm.Version++
					case "digest":
						page.Digest = strings.Repeat("0", 64)
					case "total":
						page.Total++
					case "cursor":
						page.NextAfter = "wrong"
					case "duplicate":
						page.Inventory.Users[1] = page.Inventory.Users[0]
					case "truncated":
						page.NextAfter = ""
					case "unpaused":
						page.Inventory.Realm.Enabled = true
					case "unconfirmed":
						page.Inventory.Realm.SuspensionConfirmed = false
					case "negative":
						page.Inventory.Pending.Media = -1
					case "oversize":
						page.Total = RecoveryInventoryMaximum + 1
					}
				}
				return page, err
			})
			if e == nil {
				t.Fatal("accepted inconsistent inventory")
			}
		})
	}
}

func TestRecoveryInventoryRejectsUnsafeIdentity(t *testing.T) {
	for _, change := range []func(*RecoveryInventory){
		func(r *RecoveryInventory) { r.Users[0].State = "unknown" },
		func(r *RecoveryInventory) { r.Users[0].Identity.TenantID = "other" },
		func(r *RecoveryInventory) { r.Users[0].Identity.AssignmentVersion = 0 },
		func(r *RecoveryInventory) { r.Users[0].AuthVersion = 0 },
		func(r *RecoveryInventory) { r.Users[0].Phone = strings.Repeat("0", 129) },
		func(r *RecoveryInventory) { r.Users = append(r.Users, r.Users[0]) },
	} {
		r := recoveryFixture(1)
		change(&r)
		if _, e := r.Digest(); e == nil {
			t.Fatal("unsafe identity accepted")
		}
	}
	raw, _ := json.Marshal(recoveryFixture(1))
	for _, forbidden := range []string{"password", "token", "nickname", "message", "contacts"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("unexpected sensitive field", forbidden)
		}
	}
}
