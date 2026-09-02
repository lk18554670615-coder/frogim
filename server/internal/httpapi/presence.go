package httpapi

import (
	"github.com/linli/im/server/internal/wukong"
	"net/http"
	"strings"
	"time"
)

func (x *API) userPresence(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserIDs []string `json:"userIds"`
		GroupID string   `json:"groupId"`
	}
	if decode(r, &body) != nil || len(body.UserIDs) == 0 || len(body.UserIDs) > 200 || len(body.GroupID) > 200 {
		writeError(w, 400, "INVALID_ARGUMENT", "userIds must contain 1–200 users")
		return
	}
	ids := make([]string, 0, len(body.UserIDs))
	seen := map[string]bool{}
	for _, id := range body.UserIDs {
		if strings.TrimSpace(id) != id || id == "" || len(id) > 200 {
			writeError(w, 400, "INVALID_ARGUMENT", "invalid user ID")
			return
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	allowed, err := x.app.AllowedPresenceTargets(r.Context(), uid(r), ids, body.GroupID)
	if err != nil {
		handleErr(w, err)
		return
	}
	targets := []string{}
	for _, id := range ids {
		if allowed[id] {
			targets = append(targets, id)
		}
	}
	values := x.presence.Query(r.Context(), targets)
	// A relationship or role may have changed while waiting for WuKongIM.
	allowed, err = x.app.AllowedPresenceTargets(r.Context(), uid(r), ids, body.GroupID)
	if err != nil {
		handleErr(w, err)
		return
	}
	items := make([]wukong.Presence, 0, len(ids))
	for _, id := range ids {
		value := wukong.Presence{UserID: id, Status: "hidden", CheckedAt: time.Now().UTC()}
		if allowed[id] {
			if v, ok := values[id]; ok {
				value = v
			} else {
				value.Status = "unknown"
			}
		}
		items = append(items, value)
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, 200, map[string]any{"items": items})
}
