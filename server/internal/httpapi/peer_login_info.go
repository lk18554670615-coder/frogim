package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/ipregion"
)

// Deliberately separate from the administrator's access-profile DTO and User.
type peerLoginInfo struct {
	UserID      string          `json:"userId"`
	LastLoginIP string          `json:"lastLoginIp"`
	Region      ipregion.Region `json:"region"`
}

func (x *API) conversationPeerLoginInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	peer, ip, err := x.app.ConversationPeerLoginIP(ctx, uid(r), r.PathValue("id"))
	if errors.Is(err, app.ErrForbidden) || errors.Is(err, app.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "direct conversation not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "LOGIN_IP_UNAVAILABLE", "login IP is temporarily unavailable")
		return
	}
	write(w, http.StatusOK, peerLoginInfo{UserID: peer, LastLoginIP: ip, Region: x.ipRegion.Lookup(ip)})
}
