package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/linli/im/server/internal/model"
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
	write(w, http.StatusCreated, map[string]any{"item": user})
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
		item, err = x.messageWithDownloadURL(r.Context(), userID, item)
		if err != nil {
			handleErr(w, err)
			return
		}
		if nextBeforeSeq == 0 || item.Seq < nextBeforeSeq {
			nextBeforeSeq = item.Seq
		}
		result = append(result, map[string]any{
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
