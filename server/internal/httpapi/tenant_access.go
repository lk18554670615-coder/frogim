package httpapi

import (
	"net/http"

	"github.com/linli/im/server/internal/tenancy"
)

func (x *API) setTenantAccess(w http.ResponseWriter, r *http.Request) {
	var op tenancy.AccessOperation
	if decode(r, &op) != nil || op.Identity.Validate() != nil || op.Identity.TenantID != x.cfg.TenantID || !tenancy.ValidID(op.OperationID) || op.AuthVersion < 2 {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid access operation")
		return
	}
	err := x.tenantStore.WithTenantSessionFence(r.Context(), op.Identity.LocalUserID, func() error {
		done, err := x.tenantStore.BeginTenantAccess(r.Context(), op)
		if err != nil || done {
			return err
		}
		if err = x.disconnectTenantSessions(r.Context(), op.Identity.LocalUserID); err != nil {
			return err
		}
		return x.tenantStore.FinishTenantAccess(r.Context(), op)
	})
	if err != nil {
		writeError(w, 503, "TENANT_ACCESS_UNCONFIRMED", "enterprise access change not confirmed")
		return
	}
	write(w, 200, tenancy.AccessAck{AccessOperation: op, State: "completed"})
}
