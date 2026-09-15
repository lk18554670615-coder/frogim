package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/media"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/wukong"
)

func (x *API) adminGroupSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExpectedUpdatedAt               time.Time `json:"expectedUpdatedAt"`
		Name                            *string   `json:"name"`
		AvatarMediaID                   *string   `json:"avatarMediaId"`
		Announcement                    *string   `json:"announcement"`
		JoinPolicy                      *string   `json:"joinPolicy"`
		AllowMemberAddFriend            *bool     `json:"allowMemberAddFriend"`
		HistoryVisibleToNewMembers      *bool     `json:"historyVisibleToNewMembers"`
		MemberMessageRateLimitPerMinute *int      `json:"memberMessageRateLimitPerMinute"`
		AllMuted                        *bool     `json:"allMuted"`
		Reason                          string    `json:"reason"`
		Confirmed                       bool      `json:"confirmed"`
	}
	if decode(r, &body) != nil || !confirmedReason(body.Confirmed, body.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	result, err := x.app.AdminUpdateGroupSettings(r.Context(), store.AdminGroupSettingsUpdate{
		ActorID: uid(r), GroupID: r.PathValue("id"), Reason: body.Reason, RequestIP: x.clientIP(r), ExpectedUpdatedAt: body.ExpectedUpdatedAt,
		Name: body.Name, AvatarMediaID: body.AvatarMediaID, Announcement: body.Announcement, JoinPolicy: body.JoinPolicy,
		AllowMemberAddFriend: body.AllowMemberAddFriend, HistoryVisibleToNewMembers: body.HistoryVisibleToNewMembers,
		MemberMessageRateLimitPerMinute: body.MemberMessageRateLimitPerMinute, AllMuted: body.AllMuted,
	})
	if err != nil {
		if err == app.ErrGroupSettingsChanged {
			writeError(w, http.StatusConflict, "GROUP_SETTINGS_CHANGED", "群设置已被其他人修改，请重新加载")
			return
		}
		handleErr(w, err)
		return
	}
	x.decorateAdminGroupOverview(result.Group)
	write(w, http.StatusOK, result)
}

func (x *API) adminGroupOwnerID(groupID string) (string, error) {
	item, err := x.app.AdminGroupOverview(groupID)
	if err != nil {
		return "", err
	}
	ownerID, _ := item["ownerId"].(string)
	if strings.TrimSpace(ownerID) == "" {
		return "", app.ErrNotFound
	}
	return ownerID, nil
}

func (x *API) adminGroupAvatarPresign(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MIME      string `json:"mime"`
		FileName  string `json:"fileName"`
		Size      int64  `json:"size"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(r, &body) != nil || !confirmedReason(body.Confirmed, body.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	service, ok := x.media.(groupAvatarMediaService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE", "group avatar upload is unavailable")
		return
	}
	ownerID, err := x.adminGroupOwnerID(r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	prepared, err := service.PrepareGroupAvatar(r.Context(), ownerID, r.PathValue("id"), body.MIME, body.FileName, body.Size)
	if err != nil {
		if err == media.ErrInvalid {
			writeError(w, http.StatusBadRequest, "INVALID_MEDIA", "请选择有效且大小符合限制的图片")
		} else if err == media.ErrUnavailable {
			writeError(w, http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE", err.Error())
		} else {
			handleErr(w, err)
		}
		return
	}
	write(w, http.StatusCreated, prepared)
}

func (x *API) adminGroupAvatarComplete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Checksum  string `json:"checksum"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(r, &body) != nil || !confirmedReason(body.Confirmed, body.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	ownerID, err := x.adminGroupOwnerID(r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	item, err := x.media.Complete(r.Context(), ownerID, r.PathValue("mediaId"), body.Checksum)
	if err != nil {
		if err == media.ErrInvalid {
			writeError(w, http.StatusBadRequest, "INVALID_MEDIA", err.Error())
		} else if err == media.ErrForbidden {
			writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		} else {
			handleErr(w, err)
		}
		return
	}
	if !strings.HasPrefix(strings.ToLower(item.MIME), "image/") || !strings.HasPrefix(item.ObjectKey, "groups/"+r.PathValue("id")+"/") {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "avatar upload does not belong to this group")
		return
	}
	write(w, http.StatusOK, map[string]any{"mediaId": item.ID, "avatarUrl": x.permanentMediaURL(item.ID, false)})
}

func (x *API) decorateAdminGroupOverview(item map[string]any) {
	if owner, ok := item["owner"].(*model.User); ok {
		x.setAdminAvatarURL(owner)
	}
	if avatar, ok := item["avatarUrl"].(string); ok {
		if mediaID := avatarMediaIDFromPath(avatar); mediaID != "" {
			item["avatarUrl"] = x.permanentMediaURL(mediaID, false)
		}
	}
}

type adminGroupWrite struct {
	Reason    string `json:"reason"`
	Confirmed bool   `json:"confirmed"`
}

func decodeAdminGroupWrite(w http.ResponseWriter, r *http.Request, target any) (string, bool) {
	if decode(r, target) != nil {
		return "", false
	}
	var reason string
	var confirmed bool
	switch p := target.(type) {
	case *adminGroupWrite:
		reason, confirmed = p.Reason, p.Confirmed
	case *struct {
		SenderUID string `json:"senderUid"`
		Content   string `json:"content"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}:
		reason, confirmed = p.Reason, p.Confirmed
	case *struct {
		Muted     bool   `json:"muted"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}:
		reason, confirmed = p.Reason, p.Confirmed
	case *struct {
		Remark    string `json:"remark"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}:
		reason, confirmed = p.Reason, p.Confirmed
	}
	return strings.TrimSpace(reason), confirmedReason(confirmed, reason)
}

func (x *API) adminSendGroupMessage(w http.ResponseWriter, r *http.Request) {
	var p struct {
		SenderUID string `json:"senderUid"`
		Content   string `json:"content"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	reason, ok := decodeAdminGroupWrite(w, r, &p)
	if !ok {
		writeError(w, 400, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	message, duplicate, err := x.app.AdminSendGroupMessage(r.Context(), p.SenderUID, r.PathValue("id"), p.Content)
	metadata := map[string]any{"groupId": r.PathValue("id"), "senderUid": strings.TrimSpace(p.SenderUID), "reason": reason}
	if err != nil {
		x.app.RecordAdminAudit(uid(r), "group.message.proxy_send", "group", r.PathValue("id"), "failed", x.clientIP(r), metadata)
		handleErr(w, err)
		return
	}
	metadata["messageId"] = message.ID
	x.app.RecordAdminAudit(uid(r), "group.message.proxy_send", "group", r.PathValue("id"), "success", x.clientIP(r), metadata)
	write(w, http.StatusCreated, map[string]any{"item": message, "duplicate": duplicate})
}

func (x *API) adminGroupMessages(w http.ResponseWriter, r *http.Request) {
	if x.wukongClient == nil || x.wukongSetupErr != nil {
		writeError(w, http.StatusServiceUnavailable, "WUKONG_UNAVAILABLE", "WuKongIM is unavailable")
		return
	}
	groupID := strings.TrimSpace(r.PathValue("id"))
	before, _ := strconv.ParseInt(r.URL.Query().Get("beforeSeq"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	overview, err := x.app.AdminGroupOverview(groupID)
	if err != nil {
		handleErr(w, err)
		return
	}
	ownerID, _ := overview["ownerId"].(string)
	if ownerID == "" {
		handleErr(w, store.ErrNotFound)
		return
	}
	start := uint64(0)
	if before > 1 {
		start = uint64(before - 1)
	}
	output, err := x.wukongClient.SyncMessages(r.Context(), wukong.MessageSyncRequest{LoginUID: ownerID, ChannelID: groupID, ChannelType: wukong.ChannelGroup, StartMessageSeq: start, Limit: limit, PullMode: 0, EventSummaryMode: "full"})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "WUKONG_UNAVAILABLE", err.Error())
		return
	}
	items := make([]*model.Message, 0, len(output.Messages))
	for index := len(output.Messages) - 1; index >= 0; index-- {
		raw := output.Messages[index]
		messageID := wukongString(raw["message_idstr"])
		message, mapErr := wukongForwardSource(raw, store.WukongMessageRef{MessageID: messageID, ConversationID: groupID, ChannelID: groupID, ChannelType: wukong.ChannelGroup})
		if mapErr == nil {
			items = append(items, message)
		}
	}
	if err = x.app.EnrichAdminGroupMessages(r.Context(), groupID, items); err != nil {
		handleErr(w, err)
		return
	}
	senders := map[string]*model.User{}
	result := make([]map[string]any, 0, len(items))
	next := int64(0)
	for _, item := range items {
		if _, exists := senders[item.SenderID]; !exists {
			sender, lookupErr := x.app.UserContext(r.Context(), item.SenderID)
			if lookupErr == nil {
				x.setAdminAvatarURL(sender)
				senders[item.SenderID] = sender
			} else {
				senders[item.SenderID] = &model.User{ID: item.SenderID, Name: item.SenderID}
			}
		}
		item, err = x.adminMessageWithDownloadURL(r.Context(), item)
		if err != nil {
			handleErr(w, err)
			return
		}
		if next == 0 || item.Seq < next {
			next = item.Seq
		}
		result = append(result, map[string]any{"id": item.ID, "conversationId": groupID, "conversationSeq": item.Seq, "senderId": item.SenderID, "sender": senders[item.SenderID], "type": item.Type, "body": item.Body, "createdAt": item.CreatedAt, "recalledAt": item.RecalledAt, "expiresAt": item.ExpiresAt, "expiredAt": item.ExpiredAt, "editedAt": item.EditedAt, "adminRecall": item.AdminRecall, "moderatedBy": item.ModeratedBy, "moderationReason": item.ModerationReason, "deletedForEveryoneAt": item.DeletedForEveryoneAt, "deletedForEveryoneBy": item.DeletedForEveryoneBy})
	}
	if len(items) < limit || next <= 1 {
		next = 0
	}
	x.app.RecordAdminAudit(uid(r), "group.message.history.viewed", "group", groupID, "success", x.clientIP(r), map[string]any{"beforeSeq": before, "returned": len(result)})
	write(w, 200, map[string]any{"items": result, "nextBeforeSeq": next})
}
func (x *API) adminMessageWithDownloadURL(_ context.Context, message *model.Message) (*model.Message, error) {
	if message == nil {
		return nil, nil
	}
	copy := *message
	copy.Body = make(map[string]any, len(message.Body)+1)
	for key, value := range message.Body {
		copy.Body[key] = value
	}
	if message.Type != "image" && message.Type != "audio" && message.Type != "video" && message.Type != "file" {
		return &copy, nil
	}
	mediaID, _ := copy.Body["mediaId"].(string)
	if mediaID == "" || x.media == nil {
		return &copy, nil
	}
	delete(copy.Body, "cover")
	delete(copy.Body, "coverMediaId")
	delete(copy.Body, "coverLocalPath")
	if item, err := x.app.GetMedia(mediaID); err == nil && item.CoverMediaID != "" {
		copy.Body["coverMediaId"] = item.CoverMediaID
		copy.Body["cover"] = x.permanentMediaURL(mediaID, true)
	}
	permanentURL := x.permanentMediaURL(mediaID, false)
	copy.Body["downloadUrl"] = permanentURL
	for _, key := range []string{"url", "fileUrl", "imageUrl", "videoUrl"} {
		if _, exists := copy.Body[key]; exists {
			copy.Body[key] = permanentURL
		}
	}
	return &copy, nil
}

func (x *API) adminRecallGroupMessage(w http.ResponseWriter, r *http.Request) {
	var p adminGroupWrite
	reason, ok := decodeAdminGroupWrite(w, r, &p)
	if !ok {
		writeError(w, 400, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	messageID := r.PathValue("messageId")
	already, sequence, err := x.app.AdminRecallGroupMessage(r.Context(), uid(r), r.PathValue("id"), messageID, reason)
	result := "success"
	if err != nil {
		result = "failed"
	}
	x.app.RecordAdminAudit(uid(r), "group.message.admin_recall", "message", messageID, result, x.clientIP(r), map[string]any{"groupId": r.PathValue("id"), "reason": reason, "alreadyRecalled": already, "conversationSeq": sequence})
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]any{"recalled": true, "alreadyRecalled": already, "conversationSeq": sequence})
}
func (x *API) adminGroupBlacklist(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.AdminGroupBlacklist(r.Context(), r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	for _, item := range items {
		x.setAdminAvatarURL(item.User)
	}
	write(w, 200, map[string]any{"items": items})
}
func (x *API) adminAddGroupBlacklist(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Remark    string `json:"remark"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	reason, ok := decodeAdminGroupWrite(w, r, &p)
	if !ok {
		writeError(w, 400, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	err := x.app.AdminAddGroupBlacklist(r.Context(), uid(r), r.PathValue("id"), r.PathValue("userId"), p.Remark)
	x.auditAdminGroupWrite(r, "group.blacklist.added", r.PathValue("id")+":"+r.PathValue("userId"), reason, err)
	if err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) adminRemoveGroupBlacklist(w http.ResponseWriter, r *http.Request) {
	var p adminGroupWrite
	reason, ok := decodeAdminGroupWrite(w, r, &p)
	if !ok {
		writeError(w, 400, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	err := x.app.AdminRemoveGroupBlacklist(r.Context(), uid(r), r.PathValue("id"), r.PathValue("userId"), reason)
	x.auditAdminGroupWrite(r, "group.blacklist.removed", r.PathValue("id")+":"+r.PathValue("userId"), reason, err)
	if err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) adminGroupMuteAll(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Muted     bool   `json:"muted"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	reason, ok := decodeAdminGroupWrite(w, r, &p)
	if !ok {
		writeError(w, 400, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	err := x.app.AdminSetGroupMuteAll(r.Context(), uid(r), r.PathValue("id"), p.Muted, reason)
	x.auditAdminGroupWrite(r, "group.mute_all.updated", r.PathValue("id"), reason, err)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]bool{"muted": p.Muted})
}
func (x *API) adminBanGroup(w http.ResponseWriter, r *http.Request) { x.adminSetGroupBan(w, r, true) }
func (x *API) adminUnbanGroup(w http.ResponseWriter, r *http.Request) {
	x.adminSetGroupBan(w, r, false)
}
func (x *API) adminSetGroupBan(w http.ResponseWriter, r *http.Request, banned bool) {
	var p adminGroupWrite
	reason, ok := decodeAdminGroupWrite(w, r, &p)
	if !ok {
		writeError(w, 400, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	err := x.app.AdminSetGroupBan(r.Context(), uid(r), r.PathValue("id"), banned, reason)
	action := "group.unbanned"
	if banned {
		action = "group.banned"
	}
	x.auditAdminGroupWrite(r, action, r.PathValue("id"), reason, err)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]bool{"banned": banned})
}
func (x *API) auditAdminGroupWrite(r *http.Request, action, target, reason string, err error) {
	result := "success"
	metadata := map[string]any{"reason": reason}
	if err != nil {
		result = "failed"
		metadata["error"] = err.Error()
	}
	x.app.RecordAdminAudit(uid(r), action, "group", target, result, x.clientIP(r), metadata)
}
