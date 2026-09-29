package httpapi

import (
	"github.com/linli/im/server/internal/tenancy"
	"net/http"
)

func (x *API) setTenantRealm(w http.ResponseWriter, r *http.Request) {
	var op tenancy.RealmOperation
	if decode(r, &op) != nil || !tenancy.ValidID(op.OperationID) || op.TenantID != x.cfg.TenantID || op.Version < 2 {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid enterprise access operation")
		return
	}
	done := false
	err := x.tenantStore.WithTenantRealmFence(r.Context(), func() error {
		var e error
		done, e = x.tenantStore.BeginTenantRealm(r.Context(), op)
		return e
	})
	if err != nil {
		writeError(w, 503, "TENANT_REALM_UNCONFIRMED", "enterprise access change not confirmed")
		return
	}
	if done {
		write(w, 200, tenancy.RealmAck{RealmOperation: op, State: "completed"})
		return
	}
	users, err := x.tenantStore.TenantRealmTargets(r.Context(), op)
	for _, user := range users {
		if err != nil {
			break
		}
		err = x.tenantStore.WithTenantSessionFence(r.Context(), user, func() error {
			if e := x.disconnectTenantSessions(r.Context(), user); e != nil {
				return e
			}
			return x.tenantStore.CompleteTenantRealmTarget(r.Context(), op, user)
		})
	}
	remaining := 0
	if err == nil {
		remaining, err = x.tenantStore.FinishTenantRealm(r.Context(), op)
	}
	if err != nil {
		writeError(w, 503, "TENANT_REALM_UNCONFIRMED", "enterprise access change not confirmed")
		return
	}
	state := "pending"
	if remaining == 0 {
		state = "completed"
	}
	write(w, 200, tenancy.RealmAck{RealmOperation: op, State: state, Remaining: remaining})
}
