package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/wukong"
)

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
				x.signAvatarURL(sender)
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
		result = append(result, map[string]any{"id": item.ID, "conversationId": groupID, "conversationSeq": item.Seq, "senderId": item.SenderID, "sender": senders[item.SenderID], "type": item.Type, "body": item.Body, "createdAt": item.CreatedAt, "recalledAt": item.RecalledAt, "expiresAt": item.ExpiresAt, "expiredAt": item.ExpiredAt, "editedAt": item.EditedAt, "adminRecall": item.AdminRecall, "moderatedBy": item.ModeratedBy, "moderationReason": item.ModerationReason})
	}
	if len(items) < limit || next <= 1 {
		next = 0
	}
	x.app.RecordAdminAudit(uid(r), "group.message.history.viewed", "group", groupID, "success", x.clientIP(r), map[string]any{"beforeSeq": before, "returned": len(result)})
	write(w, 200, map[string]any{"items": result, "nextBeforeSeq": next})
}
func (x *API) adminMessageWithDownloadURL(ctx context.Context, message *model.Message) (*model.Message, error) {
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
	url, err := x.media.DownloadURL(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	copy.Body["downloadUrl"] = url
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
		x.signAvatarURL(item.User)
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
