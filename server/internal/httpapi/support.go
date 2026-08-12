package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/linli/im/server/internal/store"
)

func supportSkillJSON(item *store.SupportSkillGroup) map[string]any {
	return map[string]any{
		"id": item.ID, "name": item.Name, "description": item.Description,
		"routingStrategy": item.RoutingStrategy, "maxConcurrentPerAgent": item.MaxConcurrentPerAgent,
		"enabled": item.Enabled, "queueCount": item.QueueCount, "availableAgents": item.AvailableAgents,
		"createdAt": item.CreatedAt, "updatedAt": item.UpdatedAt,
	}
}

func supportAgentJSON(item *store.SupportAgent) map[string]any {
	return map[string]any{
		"userId": item.UserID, "name": item.Name, "handle": item.Handle, "avatarUrl": item.AvatarURL,
		"status": item.Status, "maxConcurrent": item.MaxConcurrent, "activeSessions": item.ActiveSessions,
		"skillGroupIds": item.SkillGroupIDs, "lastAssignedAt": item.LastAssignedAt,
		"createdAt": item.CreatedAt, "updatedAt": item.UpdatedAt,
	}
}

func supportSessionJSON(item *store.SupportSession) map[string]any {
	return map[string]any{
		"id": item.ID, "visitorId": item.VisitorID, "visitorName": item.VisitorName,
		"skillGroupId": item.SkillGroupID, "skillGroupName": item.SkillGroupName,
		"channelId": item.ChannelID, "channelType": item.ChannelType,
		"subject": item.Subject, "status": item.Status, "queuePosition": item.QueuePosition,
		"assignedAgentId": item.AssignedAgentID, "agentName": item.AgentName,
		"transferCount": item.TransferCount, "metadata": item.Metadata,
		"rating": item.Rating, "ratingComment": item.RatingComment, "ratedAt": item.RatedAt,
		"queueEnteredAt": item.QueueEnteredAt, "assignedAt": item.AssignedAt,
		"endedAt": item.EndedAt, "endedBy": item.EndedBy,
		"createdAt": item.CreatedAt, "updatedAt": item.UpdatedAt,
	}
}

func (x *API) supportSkills(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.SupportSkillGroups(r.Context(), false)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, supportSkillJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func (x *API) createSupportSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SkillGroupID string         `json:"skillGroupId"`
		Subject      string         `json:"subject"`
		ChannelType  int            `json:"channelType"`
		Metadata     map[string]any `json:"metadata"`
	}
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid support session")
		return
	}
	item, created, err := x.app.CreateSupportSession(r.Context(), uid(r), input.SkillGroupID, input.Subject, input.ChannelType, input.Metadata)
	if err != nil {
		handleErr(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	write(w, status, map[string]any{"item": supportSessionJSON(item), "created": created})
}

func (x *API) supportSessions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := x.app.SupportSessions(r.Context(), uid(r), r.URL.Query().Get("status"), r.URL.Query().Get("skillGroupId"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, supportSessionJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func (x *API) supportSession(w http.ResponseWriter, r *http.Request) {
	item, err := x.app.SupportSession(r.Context(), uid(r), r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": supportSessionJSON(item)})
}

func (x *API) supportAgentStatus(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status string `json:"status"`
	}
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid support agent status")
		return
	}
	agent, assigned, err := x.app.SetSupportAgentStatus(r.Context(), uid(r), input.Status)
	if err != nil {
		handleErr(w, err)
		return
	}
	output := map[string]any{"agent": supportAgentJSON(agent)}
	if assigned != nil {
		output["assignedSession"] = supportSessionJSON(assigned)
	}
	write(w, http.StatusOK, output)
}

func (x *API) supportAgents(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.SupportAgents(r.Context(), r.URL.Query().Get("skillGroupId"))
	if err != nil {
		handleErr(w, err)
		return
	}
	allowed := false
	for _, item := range items {
		allowed = allowed || item.UserID == uid(r)
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "support agent access required")
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, supportAgentJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func (x *API) claimSupportSession(w http.ResponseWriter, r *http.Request) {
	item, err := x.app.ClaimSupportSession(r.Context(), uid(r), r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": supportSessionJSON(item)})
}

func (x *API) transferSupportSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TargetAgentID string `json:"targetAgentId"`
	}
	if decode(r, &input) != nil || strings.TrimSpace(input.TargetAgentID) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "targetAgentId is required")
		return
	}
	item, err := x.app.TransferSupportSession(r.Context(), uid(r), r.PathValue("id"), input.TargetAgentID)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": supportSessionJSON(item)})
}

func (x *API) endSupportSession(w http.ResponseWriter, r *http.Request) {
	item, err := x.app.EndSupportSession(r.Context(), uid(r), r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": supportSessionJSON(item)})
}

func (x *API) rateSupportSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Rating  int    `json:"rating"`
		Comment string `json:"comment"`
	}
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid support rating")
		return
	}
	item, err := x.app.RateSupportSession(r.Context(), uid(r), r.PathValue("id"), input.Rating, input.Comment)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": supportSessionJSON(item)})
}
