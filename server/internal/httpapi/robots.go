package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/linli/im/server/internal/store"
)

func robotMenusJSON(items []store.RobotMenu) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"cmd": item.Command, "remark": item.Remark, "type": item.Type,
		})
	}
	return result
}

func robotProfileJSON(item *store.RobotProfile) map[string]any {
	status := 0
	if item.Enabled {
		status = 1
	}
	inlineOn := 0
	if item.InlineOn {
		inlineOn = 1
	}
	return map[string]any{
		"robot_id": item.UserID, "name": item.Name, "username": item.Username,
		"placeholder": item.Placeholder, "status": status, "inline_on": inlineOn,
		"version": item.Version, "menus": robotMenusJSON(item.Menus),
		"updated_at": item.UpdatedAt,
	}
}

func adminRobotProfileJSON(item *store.RobotProfile) map[string]any {
	return map[string]any{
		"userId": item.UserID, "name": item.Name, "username": item.Username,
		"placeholder": item.Placeholder, "enabled": item.Enabled, "inlineOn": item.InlineOn,
		"version": item.Version, "menus": robotMenusJSON(item.Menus),
		"updatedBy": item.UpdatedBy, "reason": item.Reason, "updatedAt": item.UpdatedAt,
	}
}

func (x *API) conversationRobots(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.RobotProfilesForConversation(r.Context(), uid(r), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, robotProfileJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func (x *API) adminRobotProfiles(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.RobotProfiles(r.Context())
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, adminRobotProfileJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func (x *API) configureRobotProfile(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Enabled     bool              `json:"enabled"`
		Username    string            `json:"username"`
		Placeholder string            `json:"placeholder"`
		InlineOn    bool              `json:"inlineOn"`
		Menus       []store.RobotMenu `json:"menus"`
		Reason      string            `json:"reason"`
		Confirmed   bool              `json:"confirmed"`
	}
	userID := strings.TrimSpace(r.PathValue("uid"))
	if decode(r, &request) != nil || userID == "" || !confirmedReason(request.Confirmed, request.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "uid, confirmed and reason are required")
		return
	}
	item, err := x.app.ConfigureRobotProfile(r.Context(), store.RobotProfile{
		UserID: userID, Username: request.Username, Placeholder: request.Placeholder,
		Enabled: request.Enabled, InlineOn: request.InlineOn, Menus: request.Menus,
	}, uid(r), strings.TrimSpace(request.Reason), time.Now().UTC())
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": adminRobotProfileJSON(item)})
}
