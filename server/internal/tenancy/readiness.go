package tenancy

// Readiness is returned only over the authenticated control listener. It is
// evidence of runtime readiness, not proof that two hosts are physically isolated.
type Readiness struct {
	Nonce         string          `json:"nonce"`
	TenantID      string          `json:"tenantId"`
	HTTPBaseURL   string          `json:"httpBaseUrl"`
	SchemaVersion int             `json:"schemaVersion"`
	Checks        map[string]bool `json:"checks"`
	Realm         *RealmSnapshot  `json:"realm,omitempty"`
}

// Absent on older enterprise binaries; deployment verification fails closed.
type RealmSnapshot struct {
	Version             int64 `json:"version"`
	Enabled             bool  `json:"enabled"`
	SuspensionConfirmed bool  `json:"suspensionConfirmed"`
}

func (r Readiness) Valid(nonce, tenant, address string) bool {
	if r.Nonce != nonce || r.TenantID != tenant || r.HTTPBaseURL != address || r.SchemaVersion < 73 {
		return false
	}
	for _, key := range []string{"databaseBinding", "databaseAndCache", "im", "media", "calls"} {
		if !r.Checks[key] {
			return false
		}
	}
	return true
}
