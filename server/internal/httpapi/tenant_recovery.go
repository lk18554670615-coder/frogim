package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

func (x *API) ConfigureRecoveryAuthorities(authorities string) { x.recoveryAuthorities = authorities }

func (x *API) tenantRecoveryAuthority(w http.ResponseWriter, r *http.Request) {
	var request tenancy.RecoveryInventoryRequest
	if decode(r, &request) != nil || !request.Valid() || request.TenantID != x.cfg.TenantID || x.recoveryAuthorities == "" || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		writeError(w, 409, "RECOVERY_AUTHORITY_UNCONFIRMED", "running trust configuration unavailable")
		return
	}
	if _, e := x.tenantStore.TenantRecoveryInventory(r.Context(), x.cfg.TenantPublicURL, request); e != nil {
		writeError(w, 409, "RECOVERY_AUTHORITY_UNCONFIRMED", "enterprise suspension not confirmed")
		return
	}
	h := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	write(w, 200, tenancy.RecoveryAuthority{TenantID: x.cfg.TenantID, PlatformControlURL: x.cfg.PlatformControlURL, AuthoritiesPEM: x.recoveryAuthorities, ClientCertificateSHA256: hex.EncodeToString(h[:])})
}

func (x *API) tenantRecoveryInventory(w http.ResponseWriter, r *http.Request) {
	var request tenancy.RecoveryInventoryRequest
	if decode(r, &request) != nil || !request.Valid() || request.TenantID != x.cfg.TenantID {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid recovery inventory request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	page, e := x.tenantStore.TenantRecoveryInventory(ctx, x.cfg.TenantPublicURL, request)
	if e != nil {
		writeError(w, 409, "RECOVERY_INVENTORY_UNCONFIRMED", "suspended enterprise inventory could not be confirmed")
		return
	}
	write(w, 200, page)
}
