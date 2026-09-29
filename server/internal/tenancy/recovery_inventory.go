package tenancy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
)

const RecoveryInventoryPageSize = 500
const RecoveryInventoryMaximum = 100000

type RecoveryIdentity struct {
	Identity         Identity `json:"identity"`
	AuthVersion      int64    `json:"authVersion"`
	Phone            string   `json:"phone"`
	State            string   `json:"state"`
	EnterpriseBanned bool     `json:"enterpriseBanned"`
	Deleted          bool     `json:"deleted"`
}
type RecoveryPending struct {
	Revocations int `json:"revocations"`
	Credentials int `json:"credentials"`
	Access      int `json:"access"`
	Realm       int `json:"realm"`
	Imports     int `json:"imports"`
	Media       int `json:"media"`
}

func (p RecoveryPending) Valid() bool {
	return p.Revocations >= 0 && p.Credentials >= 0 && p.Access >= 0 && p.Realm >= 0 && p.Imports >= 0 && p.Media >= 0
}
func (p RecoveryPending) Settled() bool { return p == (RecoveryPending{}) }

// Contains only identity/authorization evidence. No password hash, tokens,
// nickname, contact graph, messages or media are exported to the platform.
type RecoveryInventory struct {
	Version       int                `json:"version"`
	TenantID      string             `json:"tenantId"`
	HTTPBaseURL   string             `json:"httpBaseUrl"`
	Realm         RealmSnapshot      `json:"realm"`
	UnlinkedUsers int                `json:"unlinkedUsers"`
	Pending       RecoveryPending    `json:"pending"`
	Users         []RecoveryIdentity `json:"users"`
}

func (r RecoveryInventory) Digest() (string, error) {
	if r.Version != 1 || !ValidID(r.TenantID) || ValidateBaseURL(r.HTTPBaseURL, false) != nil || r.Realm.Version < 1 || r.Realm.Enabled || !r.Realm.SuspensionConfirmed || r.UnlinkedUsers < 0 || !r.Pending.Valid() || len(r.Users) > RecoveryInventoryMaximum {
		return "", ErrInvalid
	}
	previous := ""
	for _, u := range r.Users {
		if u.Identity.Validate() != nil || u.Identity.TenantID != r.TenantID || u.Identity.LocalUserID <= previous || u.AuthVersion < 1 || len(u.Phone) > 128 {
			return "", ErrInvalid
		}
		switch u.State {
		case "prepared", "active", "credentials_resetting", "platform_blocked", "retired":
		default:
			return "", ErrInvalid
		}
		previous = u.Identity.LocalUserID
	}
	if r.Users == nil {
		r.Users = []RecoveryIdentity{}
	}
	raw, e := json.Marshal(struct {
		Domain    string            `json:"domain"`
		Inventory RecoveryInventory `json:"inventory"`
	}{"frogim-recovery-inventory-v1", r})
	if e != nil {
		return "", ErrInvalid
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}

type RecoveryInventoryRequest struct {
	Nonce          string `json:"nonce"`
	TenantID       string `json:"tenantId"`
	RealmVersion   int64  `json:"realmVersion"`
	After          string `json:"after"`
	ExpectedDigest string `json:"expectedDigest"`
}

var recoveryDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (r RecoveryInventoryRequest) Valid() bool {
	return len(r.Nonce) == 43 && ValidID(r.TenantID) && r.RealmVersion >= 1 && (r.After == "" || ValidID(r.After)) && (r.ExpectedDigest == "" || recoveryDigest.MatchString(r.ExpectedDigest)) && (r.After == "" || r.ExpectedDigest != "")
}

type RecoveryInventoryPage struct {
	Nonce     string            `json:"nonce"`
	Inventory RecoveryInventory `json:"inventory"`
	Digest    string            `json:"digest"`
	Total     int               `json:"total"`
	After     string            `json:"after"`
	NextAfter string            `json:"nextAfter"`
}

func (r RecoveryInventory) Page(request RecoveryInventoryRequest) (RecoveryInventoryPage, error) {
	var out RecoveryInventoryPage
	if !request.Valid() || request.TenantID != r.TenantID || request.RealmVersion != r.Realm.Version {
		return out, ErrInvalid
	}
	digest, e := r.Digest()
	if e != nil || request.ExpectedDigest != "" && request.ExpectedDigest != digest {
		return out, ErrInvalid
	}
	start := 0
	if request.After != "" {
		found := false
		for i, u := range r.Users {
			if u.Identity.LocalUserID == request.After {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return out, ErrInvalid
		}
	}
	end := min(start+RecoveryInventoryPageSize, len(r.Users))
	out = RecoveryInventoryPage{Nonce: request.Nonce, Inventory: r, Digest: digest, Total: len(r.Users), After: request.After}
	out.Inventory.Users = append([]RecoveryIdentity{}, r.Users[start:end]...)
	if end < len(r.Users) {
		out.NextAfter = r.Users[end-1].Identity.LocalUserID
	}
	return out, nil
}

// Strict paging binds all pages to a fresh nonce, one digest and the independently
// selected tenant/address/realm. A final read detects drift during collection.
// This is evidence at a point in time, NOT permission to activate old accounts.
func CollectRecoveryInventory(ctx context.Context, tenant, address string, realm int64, fetch func(context.Context, RecoveryInventoryRequest) (RecoveryInventoryPage, error)) (RecoveryInventory, string, error) {
	var empty RecoveryInventory
	nonce, e := Secret()
	if e != nil || !ValidID(tenant) || ValidateBaseURL(address, false) != nil || realm < 1 {
		return empty, "", ErrInvalid
	}
	request := RecoveryInventoryRequest{Nonce: nonce, TenantID: tenant, RealmVersion: realm}
	var result RecoveryInventory
	digest := ""
	total := -1
	for pageNumber := 0; pageNumber <= RecoveryInventoryMaximum/RecoveryInventoryPageSize; pageNumber++ {
		page, e := fetch(ctx, request)
		if e != nil {
			return empty, "", e
		}
		header := page.Inventory
		header.Users = nil
		if page.Nonce != nonce || page.After != request.After || page.Inventory.TenantID != tenant || page.Inventory.HTTPBaseURL != address || page.Inventory.Realm.Version != realm || page.Total < 0 || page.Total > RecoveryInventoryMaximum || len(page.Inventory.Users) > RecoveryInventoryPageSize || !recoveryDigest.MatchString(page.Digest) {
			return empty, "", ErrInvalid
		}
		if pageNumber == 0 {
			result = header
			result.Users = []RecoveryIdentity{}
			digest, total = page.Digest, page.Total
		} else {
			original := result
			original.Users = nil
			a, _ := json.Marshal(header)
			b, _ := json.Marshal(original)
			if string(a) != string(b) || page.Digest != digest || page.Total != total {
				return empty, "", ErrInvalid
			}
		}
		result.Users = append(result.Users, page.Inventory.Users...)
		if len(result.Users) > total {
			return empty, "", ErrInvalid
		}
		if page.NextAfter == "" {
			if len(result.Users) != total {
				return empty, "", ErrInvalid
			}
			got, e := result.Digest()
			if e != nil || got != digest {
				return empty, "", ErrInvalid
			}
			request.After = ""
			request.ExpectedDigest = digest
			fresh, e := fetch(ctx, request)
			first, e2 := result.Page(request)
			a, _ := json.Marshal(fresh)
			b, _ := json.Marshal(first)
			if e != nil || e2 != nil || string(a) != string(b) {
				return empty, "", ErrInvalid
			}
			return result, digest, nil
		}
		if len(page.Inventory.Users) != RecoveryInventoryPageSize || page.NextAfter != page.Inventory.Users[len(page.Inventory.Users)-1].Identity.LocalUserID || page.NextAfter <= request.After || len(result.Users) >= total {
			return empty, "", ErrInvalid
		}
		request.After = page.NextAfter
		request.ExpectedDigest = digest
	}
	return empty, "", ErrInvalid
}
