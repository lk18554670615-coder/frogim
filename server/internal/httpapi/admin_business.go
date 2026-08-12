package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/linli/im/server/internal/store"
)

type adminMutationConfirmation struct {
	Reason    string `json:"reason"`
	Confirmed bool   `json:"confirmed"`
}

func validAdminMutation(input adminMutationConfirmation) bool {
	return input.Confirmed && strings.TrimSpace(input.Reason) != "" && len(strings.TrimSpace(input.Reason)) <= 1000
}

func (x *API) registerBusinessAdminRoutes(prefix string) {
	x.mux.Handle("GET "+prefix+"/channels", x.requireAdmin(http.HandlerFunc(x.adminBusinessChannels)))
	x.mux.Handle("POST "+prefix+"/channels", x.requireAdmin(http.HandlerFunc(x.adminCreateBusinessChannel)))
	x.mux.Handle("PATCH "+prefix+"/channels/{id}", x.requireAdmin(http.HandlerFunc(x.adminUpdateBusinessChannel)))
	x.mux.Handle("GET "+prefix+"/channels/{id}/members", x.requireAdmin(http.HandlerFunc(x.adminBusinessChannelMembers)))
	x.mux.Handle("PUT "+prefix+"/channels/{id}/members/{userId}", x.requireAdmin(http.HandlerFunc(x.adminAddBusinessChannelMember)))
	x.mux.Handle("PATCH "+prefix+"/channels/{id}/members/{userId}", x.requireAdmin(http.HandlerFunc(x.adminUpdateBusinessChannelMember)))
	x.mux.Handle("DELETE "+prefix+"/channels/{id}/members/{userId}", x.requireAdmin(http.HandlerFunc(x.adminRemoveBusinessChannelMember)))
	x.mux.Handle("GET "+prefix+"/channels/{id}/access", x.requireAdmin(http.HandlerFunc(x.adminBusinessChannelAccess)))
	x.mux.Handle("PUT "+prefix+"/channels/{id}/access/{accessType}/{userId}", x.requireAdmin(http.HandlerFunc(x.adminSetBusinessChannelAccess)))
	x.mux.Handle("DELETE "+prefix+"/channels/{id}/access/{accessType}/{userId}", x.requireAdmin(http.HandlerFunc(x.adminSetBusinessChannelAccess)))

	x.mux.Handle("GET "+prefix+"/support/skills", x.requireAdmin(http.HandlerFunc(x.adminSupportSkills)))
	x.mux.Handle("POST "+prefix+"/support/skills", x.requireAdmin(http.HandlerFunc(x.adminSaveSupportSkill)))
	x.mux.Handle("PUT "+prefix+"/support/skills/{id}", x.requireAdmin(http.HandlerFunc(x.adminSaveSupportSkill)))
	x.mux.Handle("GET "+prefix+"/support/agents", x.requireAdmin(http.HandlerFunc(x.adminSupportAgents)))
	x.mux.Handle("PUT "+prefix+"/support/agents/{userId}", x.requireAdmin(http.HandlerFunc(x.adminSaveSupportAgent)))
	x.mux.Handle("GET "+prefix+"/support/sessions", x.requireAdmin(http.HandlerFunc(x.adminSupportSessions)))
	x.mux.Handle("POST "+prefix+"/support/sessions/{id}/claim", x.requireAdmin(http.HandlerFunc(x.adminClaimSupportSession)))
	x.mux.Handle("POST "+prefix+"/support/sessions/{id}/transfer", x.requireAdmin(http.HandlerFunc(x.adminTransferSupportSession)))
	x.mux.Handle("POST "+prefix+"/support/sessions/{id}/end", x.requireAdmin(http.HandlerFunc(x.adminEndSupportSession)))
}

func (x *API) adminBusinessChannels(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	channelType := 0
	if raw := strings.TrimSpace(query.Get("channelType")); raw != "" {
		var err error
		channelType, err = strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid channelType")
			return
		}
	}
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, total, next, err := x.app.AdminBusinessChannelsPage(r.Context(), query.Get("q"), query.Get("category"), channelType, query.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, businessChannelJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result, "total": total, "nextCursor": next})
}

func (x *API) adminCreateBusinessChannel(w http.ResponseWriter, r *http.Request) {
	var request struct {
		adminMutationConfirmation
		OwnerID       string         `json:"ownerId"`
		ChannelType   int            `json:"channelType"`
		Name          string         `json:"name"`
		AvatarURL     string         `json:"avatarUrl"`
		ParentID      string         `json:"parentId"`
		Description   string         `json:"description"`
		Visibility    string         `json:"visibility"`
		JoinPolicy    string         `json:"joinPolicy"`
		PostingPolicy string         `json:"postingPolicy"`
		SlowMode      int            `json:"slowModeSeconds"`
		Metadata      map[string]any `json:"metadata"`
	}
	if decode(r, &request) != nil || !validAdminMutation(request.adminMutationConfirmation) || strings.TrimSpace(request.OwnerID) == "" {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed, reason and ownerId are required")
		return
	}
	item, err := x.app.CreateBusinessChannel(r.Context(), request.OwnerID, store.BusinessChannelCreate{
		ChannelType: request.ChannelType, Name: request.Name, AvatarURL: request.AvatarURL,
		ParentID: request.ParentID, Description: request.Description, Visibility: request.Visibility,
		JoinPolicy: request.JoinPolicy, PostingPolicy: request.PostingPolicy,
		SlowModeSeconds: request.SlowMode, Metadata: request.Metadata,
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "channel.create", "business_channel", item.ID, "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason), "channelType": item.ChannelType, "ownerId": request.OwnerID})
	write(w, http.StatusCreated, map[string]any{"item": businessChannelJSON(item)})
}

func (x *API) adminUpdateBusinessChannel(w http.ResponseWriter, r *http.Request) {
	channelType, ok := requestChannelType(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "channelType is required")
		return
	}
	var request struct {
		adminMutationConfirmation
		Name            *string         `json:"name"`
		AvatarURL       *string         `json:"avatarUrl"`
		Description     *string         `json:"description"`
		Visibility      *string         `json:"visibility"`
		JoinPolicy      *string         `json:"joinPolicy"`
		PostingPolicy   *string         `json:"postingPolicy"`
		SlowModeSeconds *int            `json:"slowModeSeconds"`
		Ban             *bool           `json:"ban"`
		Disband         *bool           `json:"disband"`
		SendBan         *bool           `json:"sendBan"`
		AllowStranger   *bool           `json:"allowStranger"`
		Metadata        *map[string]any `json:"metadata"`
	}
	if decode(r, &request) != nil || !validAdminMutation(request.adminMutationConfirmation) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	ownerID, err := x.app.AdminBusinessChannelOwner(r.Context(), r.PathValue("id"), channelType)
	if err != nil {
		handleErr(w, err)
		return
	}
	update := store.BusinessChannelUpdate{
		Name: request.Name, AvatarURL: request.AvatarURL, Description: request.Description,
		Visibility: request.Visibility, JoinPolicy: request.JoinPolicy, PostingPolicy: request.PostingPolicy,
		SlowModeSeconds: request.SlowModeSeconds, Ban: request.Ban, Disband: request.Disband,
		SendBan: request.SendBan, AllowStranger: request.AllowStranger,
	}
	if request.Metadata != nil {
		update.Metadata, update.MetadataSet = *request.Metadata, true
	}
	item, err := x.app.UpdateBusinessChannel(r.Context(), ownerID, r.PathValue("id"), channelType, update)
	if err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "channel.update", "business_channel", item.ID, "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason), "channelType": channelType})
	write(w, http.StatusOK, map[string]any{"item": businessChannelJSON(item)})
}

func (x *API) adminBusinessChannelMembers(w http.ResponseWriter, r *http.Request) {
	channelType, ok := requestChannelType(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "channelType is required")
		return
	}
	ownerID, err := x.app.AdminBusinessChannelOwner(r.Context(), r.PathValue("id"), channelType)
	if err != nil {
		handleErr(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, err := x.app.BusinessChannelMembers(r.Context(), ownerID, r.PathValue("id"), channelType, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, businessChannelMemberJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result, "nextCursor": next})
}

func (x *API) adminAddBusinessChannelMember(w http.ResponseWriter, r *http.Request) {
	channelType, ok := requestChannelType(r)
	var request struct {
		adminMutationConfirmation
		MutedUntil *time.Time `json:"mutedUntil"`
		ExpiresAt  *time.Time `json:"expiresAt"`
	}
	if !ok || decode(r, &request) != nil || !validAdminMutation(request.adminMutationConfirmation) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "valid channelType, confirmed and reason are required")
		return
	}
	ownerID, err := x.app.AdminBusinessChannelOwner(r.Context(), r.PathValue("id"), channelType)
	if err == nil {
		err = x.app.ApplyBusinessChannelMember(r.Context(), store.BusinessChannelMemberAction{ActorID: ownerID, ChannelID: r.PathValue("id"), ChannelType: channelType, TargetID: r.PathValue("userId"), Action: "add", MutedUntil: request.MutedUntil, ExpiresAt: request.ExpiresAt})
	}
	if err != nil {
		handleErr(w, err)
		return
	}
	x.auditChannelMember(r, "channel.member.add", channelType, request.Reason)
	write(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (x *API) adminRemoveBusinessChannelMember(w http.ResponseWriter, r *http.Request) {
	channelType, ok := requestChannelType(r)
	var request adminMutationConfirmation
	if !ok || decode(r, &request) != nil || !validAdminMutation(request) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "valid channelType, confirmed and reason are required")
		return
	}
	ownerID, err := x.app.AdminBusinessChannelOwner(r.Context(), r.PathValue("id"), channelType)
	if err == nil {
		err = x.app.ApplyBusinessChannelMember(r.Context(), store.BusinessChannelMemberAction{ActorID: ownerID, ChannelID: r.PathValue("id"), ChannelType: channelType, TargetID: r.PathValue("userId"), Action: "remove"})
	}
	if err != nil {
		handleErr(w, err)
		return
	}
	x.auditChannelMember(r, "channel.member.remove", channelType, request.Reason)
	write(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (x *API) adminUpdateBusinessChannelMember(w http.ResponseWriter, r *http.Request) {
	channelType, ok := requestChannelType(r)
	var request struct {
		adminMutationConfirmation
		Role        *string    `json:"role"`
		MutedUntil  *time.Time `json:"mutedUntil"`
		ClearMute   bool       `json:"clearMute"`
		ExpiresAt   *time.Time `json:"expiresAt"`
		ClearExpiry bool       `json:"clearExpiry"`
	}
	if !ok || decode(r, &request) != nil || !validAdminMutation(request.adminMutationConfirmation) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "valid channelType, confirmed and reason are required")
		return
	}
	actionCount := 0
	if request.Role != nil {
		actionCount++
	}
	if request.MutedUntil != nil || request.ClearMute {
		actionCount++
	}
	if request.ExpiresAt != nil || request.ClearExpiry {
		actionCount++
	}
	if actionCount != 1 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "exactly one member update is required")
		return
	}
	ownerID, err := x.app.AdminBusinessChannelOwner(r.Context(), r.PathValue("id"), channelType)
	if err != nil {
		handleErr(w, err)
		return
	}
	action := store.BusinessChannelMemberAction{ActorID: ownerID, ChannelID: r.PathValue("id"), ChannelType: channelType, TargetID: r.PathValue("userId")}
	switch {
	case request.Role != nil:
		action.Action, action.Role = "role", *request.Role
	case request.MutedUntil != nil || request.ClearMute:
		action.Action, action.MutedUntil = "mute", request.MutedUntil
	default:
		action.Action, action.ExpiresAt = "expiry", request.ExpiresAt
	}
	if err = x.app.ApplyBusinessChannelMember(r.Context(), action); err != nil {
		handleErr(w, err)
		return
	}
	x.auditChannelMember(r, "channel.member.update", channelType, request.Reason)
	write(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (x *API) auditChannelMember(r *http.Request, action string, channelType int, reason string) {
	x.app.RecordAdminAudit(uid(r), action, "business_channel_member", r.PathValue("id")+"/"+r.PathValue("userId"), "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(reason), "channelType": channelType})
}

func (x *API) adminBusinessChannelAccess(w http.ResponseWriter, r *http.Request) {
	channelType, ok := requestChannelType(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "channelType is required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, err := x.app.AdminBusinessChannelAccess(r.Context(), r.PathValue("id"), channelType, r.URL.Query().Get("accessType"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{"channelId": item.ChannelID, "userId": item.UserID,
			"name": item.Name, "handle": item.Handle, "avatarUrl": item.AvatarURL,
			"accessType": item.AccessType, "reason": item.Reason, "createdBy": item.CreatedBy, "createdAt": item.CreatedAt})
	}
	write(w, http.StatusOK, map[string]any{"items": result, "nextCursor": next})
}

func (x *API) adminSetBusinessChannelAccess(w http.ResponseWriter, r *http.Request) {
	channelType, ok := requestChannelType(r)
	accessType := strings.TrimSpace(r.PathValue("accessType"))
	var request adminMutationConfirmation
	if !ok || (accessType != "allow" && accessType != "deny") || decode(r, &request) != nil || !validAdminMutation(request) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "valid access request, confirmed and reason are required")
		return
	}
	ownerID, err := x.app.AdminBusinessChannelOwner(r.Context(), r.PathValue("id"), channelType)
	if err == nil {
		err = x.app.ApplyBusinessChannelAccess(r.Context(), store.BusinessChannelAccessAction{
			ActorID: ownerID, ChannelID: r.PathValue("id"), ChannelType: channelType,
			TargetID: r.PathValue("userId"), AccessType: accessType, Reason: request.Reason,
			Enabled: r.Method == http.MethodPut,
		})
	}
	if err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "channel.access.update", "business_channel_access", r.PathValue("id")+"/"+r.PathValue("userId"), "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason), "channelType": channelType, "accessType": accessType, "enabled": r.Method == http.MethodPut})
	write(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (x *API) adminSupportSkills(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.SupportSkillGroups(r.Context(), true)
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

func (x *API) adminSaveSupportSkill(w http.ResponseWriter, r *http.Request) {
	var request struct {
		adminMutationConfirmation
		ID                    string `json:"id"`
		Name                  string `json:"name"`
		Description           string `json:"description"`
		RoutingStrategy       string `json:"routingStrategy"`
		MaxConcurrentPerAgent int    `json:"maxConcurrentPerAgent"`
		Enabled               bool   `json:"enabled"`
	}
	if decode(r, &request) != nil || !validAdminMutation(request.adminMutationConfirmation) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	if pathID := strings.TrimSpace(r.PathValue("id")); pathID != "" {
		request.ID = pathID
	}
	item, err := x.app.SaveSupportSkillGroup(r.Context(), uid(r), store.SupportSkillGroupInput{ID: request.ID, Name: request.Name, Description: request.Description, RoutingStrategy: request.RoutingStrategy, MaxConcurrentPerAgent: request.MaxConcurrentPerAgent, Enabled: request.Enabled})
	if err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "support.skill.save", "support_skill", item.ID, "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason)})
	write(w, http.StatusOK, map[string]any{"item": supportSkillJSON(item)})
}

func (x *API) adminSupportAgents(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.SupportAgents(r.Context(), r.URL.Query().Get("skillGroupId"))
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, supportAgentJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func (x *API) adminSaveSupportAgent(w http.ResponseWriter, r *http.Request) {
	var request struct {
		adminMutationConfirmation
		Status        string   `json:"status"`
		MaxConcurrent int      `json:"maxConcurrent"`
		SkillGroupIDs []string `json:"skillGroupIds"`
	}
	if decode(r, &request) != nil || !validAdminMutation(request.adminMutationConfirmation) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	item, err := x.app.SaveSupportAgent(r.Context(), store.SupportAgentInput{UserID: r.PathValue("userId"), Status: request.Status, MaxConcurrent: request.MaxConcurrent, SkillGroupIDs: request.SkillGroupIDs})
	if err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "support.agent.save", "support_agent", item.UserID, "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason)})
	write(w, http.StatusOK, map[string]any{"item": supportAgentJSON(item)})
}

func (x *API) adminSupportSessions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, total, next, err := x.app.AdminSupportSessionsPage(r.Context(), query.Get("q"), query.Get("status"), query.Get("skillGroupId"), query.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, supportSessionJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result, "total": total, "nextCursor": next})
}

func (x *API) adminClaimSupportSession(w http.ResponseWriter, r *http.Request) {
	var request struct {
		adminMutationConfirmation
		AgentID string `json:"agentId"`
	}
	if decode(r, &request) != nil || !validAdminMutation(request.adminMutationConfirmation) || strings.TrimSpace(request.AgentID) == "" {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed, reason and agentId are required")
		return
	}
	item, err := x.app.ClaimSupportSession(r.Context(), request.AgentID, r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	x.auditSupportSession(r, "support.session.claim", request.Reason, map[string]any{"agentId": request.AgentID})
	write(w, http.StatusOK, map[string]any{"item": supportSessionJSON(item)})
}

func (x *API) adminTransferSupportSession(w http.ResponseWriter, r *http.Request) {
	var request struct {
		adminMutationConfirmation
		TargetAgentID string `json:"targetAgentId"`
	}
	if decode(r, &request) != nil || !validAdminMutation(request.adminMutationConfirmation) || strings.TrimSpace(request.TargetAgentID) == "" {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed, reason and targetAgentId are required")
		return
	}
	current, err := x.app.AdminSupportSession(r.Context(), r.PathValue("id"))
	if err == nil && current.AssignedAgentID == "" {
		writeError(w, http.StatusConflict, "CONFLICT", "queued session must be claimed before transfer")
		return
	}
	var item *store.SupportSession
	if err == nil {
		item, err = x.app.TransferSupportSession(r.Context(), current.AssignedAgentID, current.ID, request.TargetAgentID)
	}
	if err != nil {
		handleErr(w, err)
		return
	}
	x.auditSupportSession(r, "support.session.transfer", request.Reason, map[string]any{"targetAgentId": request.TargetAgentID})
	write(w, http.StatusOK, map[string]any{"item": supportSessionJSON(item)})
}

func (x *API) adminEndSupportSession(w http.ResponseWriter, r *http.Request) {
	var request adminMutationConfirmation
	if decode(r, &request) != nil || !validAdminMutation(request) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	current, err := x.app.AdminSupportSession(r.Context(), r.PathValue("id"))
	actorID := ""
	if err == nil {
		actorID = current.AssignedAgentID
		if actorID == "" {
			actorID = current.VisitorID
		}
	}
	var item *store.SupportSession
	if err == nil {
		item, err = x.app.EndSupportSession(r.Context(), actorID, current.ID)
	}
	if err != nil {
		handleErr(w, err)
		return
	}
	x.auditSupportSession(r, "support.session.end", request.Reason, nil)
	write(w, http.StatusOK, map[string]any{"item": supportSessionJSON(item)})
}

func (x *API) auditSupportSession(r *http.Request, action, reason string, metadata map[string]any) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["reason"] = strings.TrimSpace(reason)
	x.app.RecordAdminAudit(uid(r), action, "support_session", r.PathValue("id"), "success", x.clientIP(r), metadata)
}
