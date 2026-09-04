package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/wukong"
)

func (x *API) createAdminUser(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Phone     string `json:"phone"`
		Name      string `json:"name"`
		Password  string `json:"password"`
		Gender    string `json:"gender"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(r, &payload) != nil || !confirmedReason(payload.Confirmed, payload.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	user, err := x.app.CreateAdminUser(r.Context(), uid(r), payload.Phone, payload.Name, payload.Password, payload.Gender, payload.Reason)
	if err != nil {
		handleErr(w, err)
		return
	}
	x.signAvatarURL(user)
	x.recordRegistration(r, user.ID, "admin")
	write(w, http.StatusCreated, map[string]any{"item": user})
}

func (x *API) createAdminUsersBatch(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Items     []app.AdminUserBatchInput `json:"items"`
		Reason    string                    `json:"reason"`
		Confirmed bool                      `json:"confirmed"`
	}
	if decode(r, &payload) != nil || !confirmedReason(payload.Confirmed, payload.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	if len(payload.Items) == 0 || len(payload.Items) > 100 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "items must contain between 1 and 100 users")
		return
	}
	batchID, items, err := x.app.CreateAdminUsersBatch(r.Context(), uid(r), payload.Items, payload.Reason)
	if err != nil {
		handleErr(w, err)
		return
	}
	succeeded := 0
	for index := range items {
		if items[index].Status == "created" {
			succeeded++
			x.signAvatarURL(items[index].User)
			x.recordRegistration(r, items[index].User.ID, "admin")
		}
	}
	failed := len(items) - succeeded
	result := "success"
	if succeeded == 0 {
		result = "failed"
	} else if failed > 0 {
		result = "partial"
	}
	x.app.RecordAdminAudit(uid(r), "user.batch_created", "user_batch", batchID, result, x.clientIP(r), map[string]any{
		"reason": strings.TrimSpace(payload.Reason), "total": len(items), "succeeded": succeeded, "failed": failed,
	})
	write(w, http.StatusOK, map[string]any{
		"batchId": batchID, "total": len(items), "succeeded": succeeded, "failed": failed, "items": items,
	})
}

func (x *API) adminUserFriendMessages(w http.ResponseWriter, r *http.Request) {
	userID, friendID := strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("friendId"))
	before, _ := strconv.ParseInt(r.URL.Query().Get("beforeSeq"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	conversation, items, err := x.app.AdminDirectHistory(r.Context(), userID, friendID, before, limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	// Admin audit reads deliberately bypass ordinary-client deletion filtering.
	if x.wukongClient != nil {
		start := uint64(0)
		if before > 0 {
			start = uint64(before - 1)
		}
		page, e := x.wukongClient.SyncMessages(r.Context(), wukong.MessageSyncRequest{LoginUID: userID, ChannelID: friendID, ChannelType: 1, StartMessageSeq: start, Limit: limit, PullMode: 0})
		if e != nil {
			writeError(w, 503, "IM_UNAVAILABLE", "message history unavailable")
			return
		}
		items = nil
		for _, raw := range page.Messages {
			message, e := wukongForwardSource(raw, store.WukongMessageRef{MessageID: wukongMessageID(raw), ConversationID: conversation.ID, ChannelID: friendID, ChannelType: 1})
			if e != nil {
				handleErr(w, e)
				return
			}
			items = append(items, message)
		}
		if e = x.app.EnrichWukongMessages(r.Context(), userID, items); e != nil {
			handleErr(w, e)
			return
		}
	}
	participants := map[string]*model.User{}
	for _, participantID := range []string{userID, friendID} {
		participant, lookupErr := x.app.UserContext(r.Context(), participantID)
		if lookupErr != nil {
			handleErr(w, lookupErr)
			return
		}
		x.signAvatarURL(participant)
		participants[participantID] = participant
	}
	result := make([]map[string]any, 0, len(items))
	nextBeforeSeq := int64(0)
	for _, item := range items {
		item, err = x.adminMessageWithDownloadURL(r.Context(), item)
		if err != nil {
			handleErr(w, err)
			return
		}
		if nextBeforeSeq == 0 || item.Seq < nextBeforeSeq {
			nextBeforeSeq = item.Seq
		}
		result = append(result, map[string]any{
			"deletedForEveryoneAt": item.DeletedForEveryoneAt, "deletedForEveryoneBy": item.DeletedForEveryoneBy,
			"id": item.ID, "clientMsgId": item.ClientMsgID, "conversationId": item.ConversationID,
			"conversationSeq": item.Seq, "senderId": item.SenderID, "sender": participants[item.SenderID],
			"type": item.Type, "body": item.Body, "replyToId": item.ReplyToID, "createdAt": item.CreatedAt,
			"recalledAt": item.RecalledAt, "expiresAt": item.ExpiresAt, "expiredAt": item.ExpiredAt,
			"editedAt": item.EditedAt, "editVersion": item.EditVersion, "encrypted": false,
			"deleted": item.AdminRecall, "adminRecall": item.AdminRecall, "moderatedBy": item.ModeratedBy,
			"moderationReason": item.ModerationReason, "moderatedAt": item.ModeratedAt,
		})
	}
	if len(items) < limit || nextBeforeSeq <= 1 {
		nextBeforeSeq = 0
	}
	x.app.RecordAdminAudit(uid(r), "message.history.viewed", "conversation", conversation.ID, "success", x.clientIP(r), map[string]any{
		"userId": userID, "friendId": friendID, "beforeSeq": before, "returned": len(result),
	})
	write(w, http.StatusOK, map[string]any{
		"conversationId": conversation.ID, "participants": participants, "items": result, "nextBeforeSeq": nextBeforeSeq,
	})
}

func (x *API) adminRecallUserMessage(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(r, &payload) != nil || !confirmedReason(payload.Confirmed, payload.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	userID, friendID, messageID := r.PathValue("id"), r.PathValue("friendId"), r.PathValue("messageId")
	already, conversationID, sequence, err := x.app.AdminRecallMessage(r.Context(), uid(r), userID, friendID, messageID, payload.Reason)
	if err != nil {
		x.app.RecordAdminAudit(uid(r), "message.admin_recall", "message", messageID, "failed", x.clientIP(r), map[string]any{
			"userId": userID, "friendId": friendID, "reason": strings.TrimSpace(payload.Reason), "error": err.Error(),
		})
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "message.admin_recall", "message", messageID, "success", x.clientIP(r), map[string]any{
		"userId": userID, "friendId": friendID, "conversationId": conversationID, "conversationSeq": sequence,
		"reason": strings.TrimSpace(payload.Reason), "alreadyRecalled": already,
	})
	write(w, http.StatusOK, map[string]any{"recalled": true, "alreadyRecalled": already, "conversationId": conversationID, "conversationSeq": sequence})
}
