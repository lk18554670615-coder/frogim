package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/wukong"
)

type conversationSyncInput struct {
	Version             int64   `json:"version"`
	LastMsgSeqs         string  `json:"lastMsgSeqs"`
	MsgCount            int64   `json:"msgCount"`
	OnlyUnread          bool    `json:"onlyUnread"`
	ExcludeChannelTypes []uint8 `json:"excludeChannelTypes"`
	Page                int     `json:"page"`
	PageSize            int     `json:"pageSize"`
}

func (x *API) wukongConversationSync(w http.ResponseWriter, r *http.Request) {
	if x.wukongClient == nil || x.wukongSetupErr != nil {
		writeError(w, http.StatusServiceUnavailable, "IM_UNAVAILABLE", "instant messaging service is temporarily unavailable")
		return
	}
	var input conversationSyncInput
	if decode(r, &input) != nil || input.Version < 0 || input.MsgCount < 0 || input.MsgCount > wukong.MaxConversationSyncMessageCount || input.Page < 0 || input.PageSize < 0 || input.PageSize > 500 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid conversation sync request")
		return
	}
	onlyUnread := uint8(0)
	if input.OnlyUnread {
		onlyUnread = 1
	}
	items, err := x.wukongClient.SyncConversations(r.Context(), wukong.ConversationSyncRequest{
		UID: uid(r), Version: input.Version, LastMsgSeqs: input.LastMsgSeqs,
		MsgCount: input.MsgCount, OnlyUnread: onlyUnread,
		ExcludeChannelTypes: input.ExcludeChannelTypes, Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "IM_SYNC_FAILED", "conversation sync failed")
		return
	}
	for index := range items {
		if items[index].ChannelType == wukong.ChannelGroup {
			access, accessErr := x.app.GroupHistoryAccess(r.Context(), uid(r), items[index].ChannelID)
			if accessErr != nil {
				items[index].Recents = []wukong.SyncedMessage{}
				items[index].Unread = 0
				items[index].LastClientNo = ""
				continue
			}
			items[index].Recents = filterHistoryMessages(items[index].Recents, *access)
			if len(items[index].Recents) == 0 {
				items[index].LastClientNo = ""
			}
			items[index].Unread = min(items[index].Unread, max(0, int(int64(items[index].LastMsgSeq)-access.UnreadAfterSeq)))
		}
		for recentIndex := range items[index].Recents {
			x.enrichWukongMedia(r.Context(), uid(r), items[index].Recents[recentIndex])
		}
	}
	recents := make([]wukong.SyncedMessage, 0)
	for index := range items {
		recents = append(recents, items[index].Recents...)
	}
	if err = x.enrichWukongExtensions(r.Context(), uid(r), recents); err != nil {
		handleErr(w, err)
		return
	}
	for index := range items {
		filtered, e := filterDeletedWireMessages(r.Context(), x.app, uid(r), items[index].Recents)
		if e != nil {
			handleErr(w, e)
			return
		}
		visible := make([]wukong.SyncedMessage, 0, len(items[index].Recents))
		for _, m := range filtered {
			if m["is_mutual_deleted"] != 1 {
				visible = append(visible, m)
			}
		}
		removed := len(visible) < len(items[index].Recents)
		items[index].Recents = visible
		if removed || (len(visible) == 0 && items[index].LastMsgSeq > 0) {
			page, err := syncVisibleGroupMessages(r.Context(), x.wukongClient, x.app, wukong.MessageSyncRequest{LoginUID: uid(r), ChannelID: items[index].ChannelID, ChannelType: items[index].ChannelType, Limit: 1, PullMode: 0})
			if err != nil {
				handleErr(w, err)
				return
			}
			visible = page.Messages
			items[index].Recents = visible
			for _, m := range visible {
				x.enrichWukongMedia(r.Context(), uid(r), m)
			}
			if err = x.enrichWukongExtensions(r.Context(), uid(r), visible); err != nil {
				handleErr(w, err)
				return
			}
		}
		if len(visible) == 0 {
			items[index].LastClientNo = ""
			items[index].Unread = 0
		} else {
			items[index].LastClientNo, _ = visible[len(visible)-1]["client_msg_no"].(string)
		}
		if items[index].Unread > 0 {
			deleted, e := x.app.DeletedUnreadCount(r.Context(), uid(r), items[index].ChannelID, items[index].ChannelType, int64(items[index].LastMsgSeq)-int64(items[index].Unread), int64(items[index].LastMsgSeq))
			if e != nil {
				handleErr(w, e)
				return
			}
			items[index].Unread = max(0, items[index].Unread-deleted)
		}
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}

type messageSyncInput struct {
	ChannelID       string `json:"channelId"`
	ChannelType     uint8  `json:"channelType"`
	StartMessageSeq uint64 `json:"startMessageSeq"`
	EndMessageSeq   uint64 `json:"endMessageSeq"`
	Limit           int    `json:"limit"`
	PullMode        int    `json:"pullMode"`
	EventSummary    string `json:"eventSummaryMode"`
}

func (x *API) wukongMessageSync(w http.ResponseWriter, r *http.Request) {
	if x.wukongClient == nil || x.wukongSetupErr != nil {
		writeError(w, http.StatusServiceUnavailable, "IM_UNAVAILABLE", "instant messaging service is temporarily unavailable")
		return
	}
	var input messageSyncInput
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid message sync request")
		return
	}
	userID := uid(r)
	if !x.canAccessWukongChannel(userID, strings.TrimSpace(input.ChannelID), input.ChannelType) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "channel access denied")
		return
	}
	output, err := syncVisibleGroupMessages(r.Context(), x.wukongClient, x.app, wukong.MessageSyncRequest{
		LoginUID: userID, ChannelID: input.ChannelID, ChannelType: input.ChannelType,
		StartMessageSeq: input.StartMessageSeq, EndMessageSeq: input.EndMessageSeq,
		Limit: input.Limit, PullMode: input.PullMode, EventSummaryMode: input.EventSummary,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "IM_SYNC_FAILED", "message sync failed")
		return
	}
	for index := range output.Messages {
		if payload, ok := wukongStreamProjectedPayload(output.Messages[index]); ok {
			output.Messages[index]["payload"] = payload
		}
		x.enrichWukongMedia(r.Context(), userID, output.Messages[index])
	}
	if err = x.enrichWukongExtensions(r.Context(), userID, output.Messages); err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, output)
}

func (x *API) wukongMessageExtensionSync(w http.ResponseWriter, r *http.Request) {
	var input struct {
		MessageIDs []string `json:"messageIds"`
	}
	if decode(r, &input) != nil || len(input.MessageIDs) == 0 || len(input.MessageIDs) > 500 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "messageIds must contain between 1 and 500 items")
		return
	}
	seen := make(map[string]struct{}, len(input.MessageIDs))
	messageIDs := make([]string, 0, len(input.MessageIDs))
	for _, raw := range input.MessageIDs {
		messageID := strings.TrimSpace(raw)
		if messageID == "" || len(messageID) > 32 {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "messageIds contains an invalid item")
			return
		}
		if _, duplicate := seen[messageID]; duplicate {
			continue
		}
		seen[messageID] = struct{}{}
		messageIDs = append(messageIDs, messageID)
	}
	items, err := x.app.WukongMessageExtensions(r.Context(), uid(r), messageIDs)
	if err != nil {
		handleErr(w, err)
		return
	}
	for id, extra := range items {
		items[id] = clientDeletionExtra(extra)
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}

func (x *API) wukongMessageExtraSync(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ChannelID   string `json:"channelId"`
		ChannelType uint8  `json:"channelType"`
		Version     int64  `json:"version"`
		Limit       int    `json:"limit"`
	}
	if decode(r, &input) != nil || strings.TrimSpace(input.ChannelID) == "" || input.Version < 0 || input.Limit < 0 || input.Limit > 500 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid message extra sync request")
		return
	}
	items, err := x.app.WukongMessageExtras(r.Context(), uid(r), strings.TrimSpace(input.ChannelID), input.ChannelType, input.Version, input.Limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		deleted := item.Extra["deletedForEveryoneAt"] != nil
		if deleted {
			item.EditedBody = nil
			item.Extra = clientDeletionExtra(item.Extra)
			item.Recalled = false
			item.Revoker = ""
		}
		recalled := 0
		if item.Recalled {
			recalled = 1
		}
		pinned := 0
		if item.Pinned {
			pinned = 1
		}
		result = append(result, map[string]any{
			"message_idstr": item.MessageID, "message_seq": item.MessageSeq,
			"is_mutual_deleted": boolToInt(deleted),
			"channel_id":        item.ChannelID, "channel_type": item.ChannelType,
			"readed": item.Read, "readed_count": item.ReadCount, "unread_count": item.UnreadCount,
			"revoke": recalled, "revoker": item.Revoker, "content_edit": wukongEditedPayload(item.EditedBody),
			"edited_at": item.EditedAt, "extra_version": item.SyncVersion,
			"is_pinned": pinned, "extra": item.Extra,
		})
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func wukongEditedPayload(body map[string]any) map[string]any {
	if len(body) == 0 {
		return nil
	}
	payload := make(map[string]any, len(body)+2)
	for key, value := range body {
		payload[key] = value
	}
	payload["type"] = wukong.ContentTypeText
	if text, ok := payload["text"].(string); ok {
		payload["content"] = text
		delete(payload, "text")
	}
	mention := map[string]any{}
	if mentions, exists := payload["mentions"]; exists {
		mention["uids"] = mentions
		delete(payload, "mentions")
	}
	if mentionAll, _ := payload["mentionAll"].(bool); mentionAll {
		mention["all"] = 1
		delete(payload, "mentionAll")
	}
	if len(mention) > 0 {
		payload["mention"] = mention
	}
	return payload
}

func (x *API) wukongReminderSync(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Version int64 `json:"version"`
		Limit   int   `json:"limit"`
	}
	if decode(r, &input) != nil || input.Version < 0 || input.Limit < 0 || input.Limit > 500 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid reminder sync request")
		return
	}
	items, err := x.app.WukongReminders(r.Context(), uid(r), input.Version, input.Limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"reminder_id": item.ID, "message_id": item.MessageID,
			"message_seq": item.MessageSeq, "channel_id": item.ChannelID,
			"channel_type": item.ChannelType, "type": item.Type,
			"is_locate": item.IsLocate, "uid": item.UserID, "text": item.Text,
			"data": item.Data, "version": item.Version, "done": item.Done,
			"need_upload": item.NeedUpload, "publisher": item.Publisher,
		})
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func (x *API) wukongReminderDone(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ReminderIDs []int64 `json:"reminderIds"`
	}
	if decode(r, &input) != nil || len(input.ReminderIDs) == 0 || len(input.ReminderIDs) > 500 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "reminderIds must contain between 1 and 500 items")
		return
	}
	for _, id := range input.ReminderIDs {
		if id <= 0 {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "reminderIds contains an invalid item")
			return
		}
	}
	if err := x.app.DoneWukongReminders(r.Context(), uid(r), input.ReminderIDs); err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"status": "ok"})
}

type wukongChannelInput struct {
	ChannelID   string `json:"channelId"`
	ChannelType uint8  `json:"channelType"`
}

func (x *API) wukongChannelInfo(w http.ResponseWriter, r *http.Request) {
	var input wukongChannelInput
	if decode(r, &input) != nil || strings.TrimSpace(input.ChannelID) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid channel request")
		return
	}
	item, err := x.app.WukongChannelInfo(r.Context(), uid(r), strings.TrimSpace(input.ChannelID), input.ChannelType)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": wukongChannelInfoJSON(item)})
}

func (x *API) wukongChannelMembers(w http.ResponseWriter, r *http.Request) {
	var input struct {
		wukongChannelInput
		Version int64 `json:"version"`
		Limit   int   `json:"limit"`
	}
	if decode(r, &input) != nil || strings.TrimSpace(input.ChannelID) == "" || input.Version < 0 || input.Limit < 0 || input.Limit > 500 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid channel member sync request")
		return
	}
	items, err := x.app.WukongChannelMembers(r.Context(), uid(r), strings.TrimSpace(input.ChannelID), input.ChannelType, input.Version, input.Limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, wukongChannelMemberJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func wukongChannelInfoJSON(item store.WukongChannelInfo) map[string]any {
	return map[string]any{
		"channel_id": item.ChannelID, "channel_type": item.ChannelType,
		"channel_name": item.Name, "channel_remark": item.Remark, "avatar": item.AvatarURL,
		"show_nick": item.ShowNick, "top": item.Top, "save": item.Save, "mute": item.Mute,
		"forbidden": item.Forbidden, "invite": item.Invite, "status": item.Status, "follow": item.Follow,
		"created_at": item.CreatedAt.UTC().Format(time.RFC3339Nano), "updated_at": item.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"version": item.Version, "online": item.Online, "last_offline": item.LastOffline,
		"receipt": item.Receipt, "category": item.Category, "remote_extra": item.Extra,
	}
}

func wukongChannelMemberJSON(item store.WukongChannelMember) map[string]any {
	role := 0
	if item.Role == "owner" {
		role = 1
	} else if item.Role == "admin" {
		role = 2
	}
	return map[string]any{
		"channel_id": item.ChannelID, "channel_type": item.ChannelType,
		"member_uid": item.UserID, "member_name": item.Name, "member_remark": item.Remark,
		"member_avatar": item.AvatarURL, "role": role, "status": item.Status,
		"is_deleted": item.Deleted, "version": item.Version,
		"created_at": item.CreatedAt.UTC().Format(time.RFC3339Nano), "updated_at": item.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"extra": item.Extra,
	}
}

func (x *API) enrichWukongExtensions(ctx context.Context, userID string, messages []wukong.SyncedMessage) error {
	messageIDs := make([]string, 0, len(messages))
	for _, message := range messages {
		if messageID, _ := message["message_idstr"].(string); strings.TrimSpace(messageID) != "" {
			messageIDs = append(messageIDs, messageID)
		}
	}
	if len(messageIDs) == 0 {
		return nil
	}
	extensions := map[string]map[string]any{}
	for len(messageIDs) > 0 {
		n := min(500, len(messageIDs))
		batch, err := x.app.WukongMessageExtensions(ctx, userID, messageIDs[:n])
		if err != nil {
			slog.Warn("WuKongIM business extension enrichment failed", "error", err)
			return err
		}
		for id, extra := range batch {
			extensions[id] = extra
		}
		messageIDs = messageIDs[n:]
	}
	for _, message := range messages {
		messageID, _ := message["message_idstr"].(string)
		extension := extensions[messageID]
		if extension["deletedForEveryoneAt"] != nil {
			message["is_mutual_deleted"] = 1
			message["payload"] = map[string]any{"type": 99, "deletedForEveryoneAt": extension["deletedForEveryoneAt"]}
			message["message_extra"] = map[string]any{"message_idstr": messageID, "is_mutual_deleted": 1, "extra_version": extension["version"]}
			continue
		}
		if len(extension) == 0 {
			continue
		}
		payload, ok := message["payload"].(map[string]any)
		if !ok {
			continue
		}
		if editedBody, edited := extension["editedBody"].(map[string]any); edited {
			for key, value := range editedBody {
				payload[key] = value
			}
			if text, textOK := editedBody["text"].(string); textOK {
				payload["content"] = text
				delete(payload, "text")
			}
			mention := map[string]any{}
			if mentions, exists := editedBody["mentions"]; exists {
				mention["uids"] = mentions
				delete(payload, "mentions")
			}
			if mentionAll, _ := editedBody["mentionAll"].(bool); mentionAll {
				mention["all"] = 1
				delete(payload, "mentionAll")
			}
			if len(mention) > 0 {
				payload["mention"] = mention
			} else {
				delete(payload, "mention")
			}
		}
		for _, key := range []string{"version", "recalledAt", "editedAt", "editVersion", "reactions", "isPinned", "pinnedBy", "pinnedAt"} {
			if value, exists := extension[key]; exists {
				payload[key] = value
			}
		}
		message["payload"] = payload
		revoke := 0
		if extension["recalledAt"] != nil {
			revoke = 1
		}
		extra := map[string]any{
			"message_idstr": messageID, "revoke": revoke,
			"revoker": extension["revoker"], "extra_version": extension["version"],
			"content_edit": wukongEditedPayloadValue(extension["editedBody"]),
			"edited_at":    wukongExtensionUnix(extension["editedAt"]),
		}
		message["message_extra"] = extra
	}
	return nil
}

func wukongEditedPayloadValue(value any) map[string]any {
	body, _ := value.(map[string]any)
	return wukongEditedPayload(body)
}

func wukongExtensionUnix(value any) int64 {
	text, _ := value.(string)
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return 0
	}
	return parsed.Unix()
}

func (x *API) enrichWukongMedia(ctx context.Context, userID string, message wukong.SyncedMessage) {
	payload, ok := message["payload"].(map[string]any)
	if !ok {
		return
	}
	mediaID, _ := payload["mediaId"].(string)
	if strings.TrimSpace(mediaID) == "" {
		return
	}
	allowed, err := x.app.CanAccessMedia(userID, mediaID)
	if err != nil || !allowed {
		return
	}
	url, err := x.media.DownloadURL(ctx, mediaID)
	if err == nil {
		payload["url"] = url
		x.enrichVideoCover(ctx, mediaID, payload)
		message["payload"] = payload
	}
}

func (x *API) canAccessWukongChannel(userID, channelID string, channelType uint8) bool {
	if userID == "" || channelID == "" {
		return false
	}
	switch channelType {
	case wukong.ChannelPerson:
		for _, friend := range x.app.Friends(userID) {
			if friend.ID == channelID {
				return true
			}
		}
		return false
	case wukong.ChannelGroup:
		return x.app.CanAccess(userID, channelID)
	case wukong.ChannelCustomer, wukong.ChannelCommunity, wukong.ChannelCommunityTopic,
		wukong.ChannelInfo, wukong.ChannelLive, wukong.ChannelVisitor:
		return x.app.CanAccess(userID, channelID)
	default:
		return false
	}
}
