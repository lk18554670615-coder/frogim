package push

import (
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

// Only complete, typed context is carried. Legacy single-enterprise payloads
// remain unchanged. No credentials or arbitrary caller data are copied.
func copyTenantScope(to, from map[string]any) {
	tenant, _ := from["tenantId"].(string)
	local, _ := from["localUserId"].(string)
	version, auth, realm := scopeVersion(from["assignmentVersion"]), scopeVersion(from["authVersion"]), scopeVersion(from["realmVersion"])
	expires, _ := from["expiresAt"].(string)
	expiry, err := time.Parse(time.RFC3339Nano, expires)
	if !tenancy.ValidID(tenant) || !tenancy.ValidID(local) || version < 1 || auth < 1 || realm < 1 || err != nil || expiry.IsZero() {
		return
	}
	to["tenantId"], to["localUserId"], to["assignmentVersion"] = tenant, local, version
	to["authVersion"], to["realmVersion"], to["expiresAt"] = auth, realm, expires
	if binding, ok := from["pushBindingId"].(string); ok && tenancy.ValidID(binding) {
		if revision := scopeVersion(from["pushBindingRevision"]); revision > 0 {
			to["pushBindingId"], to["pushBindingRevision"] = binding, revision
		}
	}
}

func scopeVersion(input any) int64 {
	var version int64
	switch value := input.(type) {
	case int64:
		version = value
	case int:
		version = int64(value)
	case float64:
		if value >= 1 && value <= 9007199254740991 && float64(int64(value)) == value {
			version = int64(value)
		}
	}
	if version < 1 || version > 9007199254740991 {
		return 0
	}
	return version
}
