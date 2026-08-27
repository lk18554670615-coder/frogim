package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/linli/im/server/internal/store"
)

func requestChannelType(r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("channelType"))
	value, err := strconv.Atoi(raw)
	return value, err == nil && value > 0 && value <= 255
}

func businessChannelJSON(item *store.BusinessChannel) map[string]any {
	return map[string]any{
		"id": item.ID, "channelId": item.ID, "channelType": item.ChannelType,
		"category": item.Category, "name": item.Name, "avatarUrl": item.AvatarURL,
		"ownerId": item.OwnerID, "parentId": item.ParentID, "description": item.Description,
		"visibility": item.Visibility, "joinPolicy": item.JoinPolicy, "postingPolicy": item.PostingPolicy,
		"slowModeSeconds": item.SlowModeSeconds, "memberCount": item.MemberCount,
		"ban": item.Ban, "disband": item.Disband, "sendBan": item.SendBan,
		"allowStranger": item.AllowStranger, "subscribed": item.Subscribed, "role": item.Role,
		"metadata": item.Metadata, "createdAt": item.CreatedAt, "updatedAt": item.UpdatedAt,
	}
}

func businessChannelMemberJSON(item *store.BusinessChannelMember) map[string]any {
	return map[string]any{
		"channelId": item.ChannelID, "userId": item.UserID, "name": item.Name,
		"handle": item.Handle, "avatarUrl": item.AvatarURL, "role": item.Role,
		"mutedUntil": item.MutedUntil, "expiresAt": item.ExpiresAt,
		"joinedAt": item.JoinedAt, "updatedAt": item.UpdatedAt,
	}
}

func businessChannelAccessJSON(item *store.BusinessChannelAccess) map[string]any {
	return map[string]any{
		"channelId": item.ChannelID, "userId": item.UserID, "name": item.Name,
		"handle": item.Handle, "avatarUrl": item.AvatarURL, "accessType": item.AccessType,
		"reason": item.Reason, "createdBy": item.CreatedBy, "createdAt": item.CreatedAt,
	}
}

func (x *API) createBusinessChannel(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ChannelType     int            `json:"channelType"`
		Name            string         `json:"name"`
		AvatarURL       string         `json:"avatarUrl"`
		ParentID        string         `json:"parentId"`
		Description     string         `json:"description"`
		Visibility      string         `json:"visibility"`
		JoinPolicy      string         `json:"joinPolicy"`
		PostingPolicy   string         `json:"postingPolicy"`
		SlowModeSeconds int            `json:"slowModeSeconds"`
		Metadata        map[string]any `json:"metadata"`
	}
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid channel request")
		return
	}
	item, err := x.app.CreateBusinessChannel(r.Context(), uid(r), store.BusinessChannelCreate{
		ChannelType: input.ChannelType, Name: input.Name, AvatarURL: input.AvatarURL,
		ParentID: input.ParentID, Description: input.Description, Visibility: input.Visibility,
		JoinPolicy: input.JoinPolicy, PostingPolicy: input.PostingPolicy,
		SlowModeSeconds: input.SlowModeSeconds, Metadata: input.Metadata,
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusCreated, map[string]any{"item": businessChannelJSON(item)})
}

func (x *API) businessChannels(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	channelType := 0
	if raw := strings.TrimSpace(query.Get("channelType")); raw != "" {
		var err error
		channelType, err = strconv.Atoi(raw)
		if err != nil || channelType < 0 || channelType > 255 {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid channelType")
			return
		}
	}
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, next, err := x.app.BusinessChannels(r.Context(), uid(r), query.Get("category"), query.Get("parentId"), channelType, query.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, businessChannelJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result, "nextCursor": next})
}

func (x *API) businessChannel(w http.ResponseWriter, r *http.Request) {
	channelType, ok := requestChannelType(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "channelType is required")
		return
	}
	item, err := x.app.BusinessChannel(r.Context(), uid(r), r.PathValue("id"), channelType)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": businessChannelJSON(item)})
}

func (x *API) updateBusinessChannel(w http.ResponseWriter, r *http.Request) {
	channelType, ok := requestChannelType(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "channelType is required")
		return
	}
	var wire struct {
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
	if decode(r, &wire) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid channel update")
		return
	}
	update := store.BusinessChannelUpdate{
		Name: wire.Name, AvatarURL: wire.AvatarURL, Description: wire.Description,
		Visibility: wire.Visibility, JoinPolicy: wire.JoinPolicy, PostingPolicy: wire.PostingPolicy,
		SlowModeSeconds: wire.SlowModeSeconds, Ban: wire.Ban, Disband: wire.Disband,
		SendBan: wire.SendBan, AllowStranger: wire.AllowStranger,
	}
	if wire.Metadata != nil {
		update.Metadata, update.MetadataSet = *wire.Metadata, true
	}
	item, err := x.app.UpdateBusinessChannel(r.Context(), uid(r), r.PathValue("id"), channelType, update)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": businessChannelJSON(item)})
}

func (x *API) applyBusinessChannelMember(w http.ResponseWriter, r *http.Request, action, targetID string, role string, mutedUntil, expiresAt *time.Time) {
	channelType, ok := requestChannelType(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "channelType is required")
		return
	}
	if err := x.app.ApplyBusinessChannelMember(r.Context(), store.BusinessChannelMemberAction{
		ActorID: uid(r), ChannelID: r.PathValue("id"), ChannelType: channelType,
		TargetID: targetID, Action: action, Role: role, MutedUntil: mutedUntil, ExpiresAt: expiresAt,
	}); err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (x *API) subscribeBusinessChannel(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ExpiresAt *time.Time `json:"expiresAt"`
	}
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid subscription request")
		return
	}
	channelType, ok := requestChannelType(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "channelType is required")
		return
	}
	if err := x.app.ApplyBusinessChannelMember(r.Context(), store.BusinessChannelMemberAction{
		ActorID: uid(r), ChannelID: r.PathValue("id"), ChannelType: channelType,
		TargetID: uid(r), Action: "subscribe", ExpiresAt: input.ExpiresAt,
	}); err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (x *API) unsubscribeBusinessChannel(w http.ResponseWriter, r *http.Request) {
	x.applyBusinessChannelMember(w, r, "unsubscribe", uid(r), "", nil, nil)
}

func (x *API) addBusinessChannelMember(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ExpiresAt *time.Time `json:"expiresAt"`
	}
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid member request")
		return
	}
	channelType, ok := requestChannelType(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "channelType is required")
		return
	}
	if err := x.app.ApplyBusinessChannelMember(r.Context(), store.BusinessChannelMemberAction{
		ActorID: uid(r), ChannelID: r.PathValue("id"), ChannelType: channelType,
		TargetID: r.PathValue("userId"), Action: "add", ExpiresAt: input.ExpiresAt,
	}); err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (x *API) removeBusinessChannelMember(w http.ResponseWriter, r *http.Request) {
	x.applyBusinessChannelMember(w, r, "remove", r.PathValue("userId"), "", nil, nil)
}

func (x *API) updateBusinessChannelMember(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Role        *string    `json:"role"`
		MutedUntil  *time.Time `json:"mutedUntil"`
		ClearMute   bool       `json:"clearMute"`
		ExpiresAt   *time.Time `json:"expiresAt"`
		ClearExpiry bool       `json:"clearExpiry"`
	}
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid member update")
		return
	}
	actionCount := 0
	if input.Role != nil {
		actionCount++
	}
	if input.MutedUntil != nil || input.ClearMute {
		actionCount++
	}
	if input.ExpiresAt != nil || input.ClearExpiry {
		actionCount++
	}
	if actionCount != 1 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "exactly one member update is required")
		return
	}
	if input.Role != nil {
		x.applyBusinessChannelMember(w, r, "role", r.PathValue("userId"), *input.Role, nil, nil)
		return
	}
	if input.MutedUntil != nil || input.ClearMute {
		x.applyBusinessChannelMember(w, r, "mute", r.PathValue("userId"), "", input.MutedUntil, nil)
		return
	}
	x.applyBusinessChannelMember(w, r, "expiry", r.PathValue("userId"), "", nil, input.ExpiresAt)
}

func (x *API) businessChannelMembers(w http.ResponseWriter, r *http.Request) {
	channelType, ok := requestChannelType(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "channelType is required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, err := x.app.BusinessChannelMembers(r.Context(), uid(r), r.PathValue("id"), channelType, r.URL.Query().Get("cursor"), limit)
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

func (x *API) businessChannelAccess(w http.ResponseWriter, r *http.Request) {
	channelType, ok := requestChannelType(r)
	accessType := strings.TrimSpace(r.URL.Query().Get("accessType"))
	if !ok || (accessType != "" && accessType != "allow" && accessType != "deny") {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid channel access query")
		return
	}
	channel, err := x.app.BusinessChannel(r.Context(), uid(r), r.PathValue("id"), channelType)
	if err != nil {
		handleErr(w, err)
		return
	}
	if channel.Role != "owner" && channel.Role != "admin" && channel.Role != "moderator" {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "channel operator role is required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, err := x.app.AdminBusinessChannelAccess(r.Context(), channel.ID, channelType, accessType, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, businessChannelAccessJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result, "nextCursor": next})
}

func (x *API) setBusinessChannelAccess(w http.ResponseWriter, r *http.Request) {
	channelType, ok := requestChannelType(r)
	accessType := strings.TrimSpace(r.PathValue("accessType"))
	if !ok || (accessType != "allow" && accessType != "deny") {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid channel access request")
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if r.Method == http.MethodPut && decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid channel access request")
		return
	}
	err := x.app.ApplyBusinessChannelAccess(r.Context(), store.BusinessChannelAccessAction{
		ActorID: uid(r), ChannelID: r.PathValue("id"), ChannelType: channelType,
		TargetID: r.PathValue("userId"), AccessType: accessType, Reason: input.Reason,
		Enabled: r.Method == http.MethodPut,
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"status": "ok"})
}
