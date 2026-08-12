package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/wukong"
)

const (
	maxStreamAnchorText = 4000
	maxStreamDeltaText  = 8192
	maxStreamFinalText  = 100000
)

func (x *API) startMessageStream(w http.ResponseWriter, r *http.Request) {
	if x.wukongClient == nil || x.wukongSetupErr != nil {
		writeError(w, http.StatusServiceUnavailable, "IM_UNAVAILABLE", "instant messaging service is temporarily unavailable")
		return
	}
	var input struct {
		ClientMsgNo  string `json:"clientMsgNo"`
		InitialText  string `json:"initialText"`
		ExpireSecond int64  `json:"expireSeconds"`
	}
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid stream message request")
		return
	}
	input.ClientMsgNo = strings.TrimSpace(input.ClientMsgNo)
	conversationID := strings.TrimSpace(r.PathValue("id"))
	if input.ClientMsgNo == "" || len(input.ClientMsgNo) > 128 || conversationID == "" || len([]rune(input.InitialText)) > maxStreamAnchorText || input.ExpireSecond < 0 || input.ExpireSecond > int64(^uint32(0)) {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid stream message request")
		return
	}
	userID := uid(r)
	route, err := x.app.AuthorizeWukongMessage(r.Context(), app.MessageTransportRequest{
		UserID: userID, ConversationID: conversationID, ClientMsgID: input.ClientMsgNo,
		Type: "text", Body: map[string]any{"text": input.InitialText},
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	result, err := x.wukongClient.SendStreamMessage(r.Context(), wukong.StoredMessageRequest{
		ClientMsgNo: input.ClientMsgNo, FromUID: userID,
		ChannelID: route.ChannelID, ChannelType: route.ChannelType,
		Expire:  uint32(input.ExpireSecond),
		Payload: map[string]any{"type": wukong.ContentTypeText, "content": input.InitialText, "schemaVersion": 1},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "IM_STREAM_START_FAILED", "stream message could not be started")
		return
	}
	write(w, http.StatusCreated, map[string]any{
		"messageId": strconv.FormatInt(result.MessageID, 10), "clientMsgNo": result.ClientMsgNo,
		"channelId": route.ChannelID, "channelType": route.ChannelType,
	})
}

func (x *API) appendMessageStreamEvent(w http.ResponseWriter, r *http.Request) {
	if x.wukongClient == nil || x.wukongSetupErr != nil {
		writeError(w, http.StatusServiceUnavailable, "IM_UNAVAILABLE", "instant messaging service is temporarily unavailable")
		return
	}
	var input struct {
		EventID   string         `json:"eventId"`
		EventType string         `json:"eventType"`
		EventKey  string         `json:"eventKey"`
		Payload   map[string]any `json:"payload"`
	}
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid stream event request")
		return
	}
	input.EventID = strings.TrimSpace(input.EventID)
	input.EventType = strings.ToLower(strings.TrimSpace(input.EventType))
	input.EventKey = strings.TrimSpace(input.EventKey)
	clientMsgNo := strings.TrimSpace(r.PathValue("clientMsgNo"))
	conversationID := strings.TrimSpace(r.PathValue("id"))
	cleanPayload, valid := validateStreamEvent(input.EventType, input.EventKey, input.EventID, input.Payload)
	if !valid || clientMsgNo == "" || len(clientMsgNo) > 128 || conversationID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid stream event request")
		return
	}
	userID := uid(r)
	route, source, err := x.authorizeOwnedStream(r, userID, conversationID, clientMsgNo)
	if err != nil {
		handleErr(w, err)
		return
	}
	messageID, _ := strconv.ParseInt(wukongString(source["message_idstr"]), 10, 64)
	result, err := x.wukongClient.AppendMessageEvent(r.Context(), wukong.MessageEventRequest{
		ChannelID: route.ChannelID, ChannelType: route.ChannelType, FromUID: userID,
		MessageID: messageID, ClientMsgNo: clientMsgNo, EventID: input.EventID,
		EventType: input.EventType, EventKey: input.EventKey, Visibility: "public",
		OccurredAt: time.Now().UTC().UnixMilli(), Payload: cleanPayload,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "IM_STREAM_EVENT_FAILED", "stream event could not be appended")
		return
	}
	write(w, http.StatusOK, result)
}

func (x *API) syncMessageStreamEvents(w http.ResponseWriter, r *http.Request) {
	if x.wukongClient == nil || x.wukongSetupErr != nil {
		writeError(w, http.StatusServiceUnavailable, "IM_UNAVAILABLE", "instant messaging service is temporarily unavailable")
		return
	}
	clientMsgNo := strings.TrimSpace(r.PathValue("clientMsgNo"))
	conversationID := strings.TrimSpace(r.PathValue("id"))
	fromSequence, err := strconv.ParseUint(defaultString(r.URL.Query().Get("fromMsgEventSeq"), "0"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid stream event cursor")
		return
	}
	limit, err := strconv.Atoi(defaultString(r.URL.Query().Get("limit"), "200"))
	if err != nil || limit <= 0 || limit > 2000 || clientMsgNo == "" || len(clientMsgNo) > 128 || conversationID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid stream event sync request")
		return
	}
	eventKey := strings.TrimSpace(r.URL.Query().Get("eventKey"))
	if eventKey != "" && !validStreamEventKey(eventKey) {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid stream event key")
		return
	}
	userID := uid(r)
	route, source, authErr := x.authorizeReadableStream(r, userID, conversationID, clientMsgNo)
	if authErr != nil {
		handleErr(w, authErr)
		return
	}
	fromUID := wukongString(source["from_uid"])
	sourceChannelID := wukongString(source["channel_id"])
	sourceChannelType := uint8(wukongInt64(source["channel_type"]))
	if fromUID == "" || sourceChannelID == "" || sourceChannelType != route.ChannelType ||
		(route.ChannelType == wukong.ChannelPerson && sourceChannelID != userID && sourceChannelID != route.ChannelID) ||
		(route.ChannelType != wukong.ChannelPerson && sourceChannelID != route.ChannelID) {
		writeError(w, http.StatusBadGateway, "IM_STREAM_SOURCE_INVALID", "stream source metadata is inconsistent")
		return
	}
	result, syncErr := x.wukongClient.SyncMessageEvents(r.Context(), wukong.MessageEventSyncRequest{
		ChannelID: sourceChannelID, ChannelType: sourceChannelType, FromUID: fromUID,
		ClientMsgNo: clientMsgNo, EventKey: eventKey, FromMsgEventSeq: fromSequence, Limit: limit,
	})
	if syncErr != nil {
		writeError(w, http.StatusBadGateway, "IM_STREAM_SYNC_FAILED", "stream events could not be synchronized")
		return
	}
	write(w, http.StatusOK, result)
}

func (x *API) authorizeOwnedStream(r *http.Request, userID, conversationID, clientMsgNo string) (store.WukongMessageRoute, wukong.SyncedMessage, error) {
	route, err := x.app.AuthorizeWukongMessage(r.Context(), app.MessageTransportRequest{
		UserID: userID, ConversationID: conversationID, ClientMsgID: clientMsgNo,
		Type: "text", Body: map[string]any{"text": ""},
	})
	if err != nil {
		return store.WukongMessageRoute{}, nil, err
	}
	message, err := x.findStreamMessage(r, userID, route, clientMsgNo)
	if err != nil {
		return store.WukongMessageRoute{}, nil, err
	}
	if wukongString(message["from_uid"]) != userID {
		return store.WukongMessageRoute{}, nil, store.ErrForbidden
	}
	return route, message, nil
}

func (x *API) authorizeReadableStream(r *http.Request, userID, conversationID, clientMsgNo string) (store.WukongMessageRoute, wukong.SyncedMessage, error) {
	route, err := x.app.ResolveWukongChannel(r.Context(), userID, conversationID)
	if err != nil {
		return store.WukongMessageRoute{}, nil, err
	}
	message, err := x.findStreamMessage(r, userID, route, clientMsgNo)
	return route, message, err
}

func (x *API) findStreamMessage(r *http.Request, userID string, route store.WukongMessageRoute, clientMsgNo string) (wukong.SyncedMessage, error) {
	items, err := x.wukongClient.SearchMessages(r.Context(), wukong.MessageSearchRequest{
		LoginUID: userID, ChannelID: route.ChannelID, ChannelType: route.ChannelType,
		ClientMsgNos: []string{clientMsgNo},
	})
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if wukongString(item["client_msg_no"]) == clientMsgNo && int(wukongInt64(item["setting"]))&wukong.SettingStream != 0 {
			return item, nil
		}
	}
	return nil, store.ErrNotFound
}

func validateStreamEvent(eventType, eventKey, eventID string, payload map[string]any) (map[string]any, bool) {
	if eventID == "" || len(eventID) > 128 {
		return nil, false
	}
	if eventType == wukong.MessageEventStreamFinish {
		return nil, (eventKey == "" || eventKey == "main") && len(payload) == 0
	}
	if eventKey == "" {
		eventKey = "main"
	}
	if !validStreamEventKey(eventKey) {
		return nil, false
	}
	switch eventType {
	case wukong.MessageEventStreamDelta:
		kind, _ := payload["kind"].(string)
		delta, _ := payload["delta"].(string)
		if kind != "text" || delta == "" || len([]rune(delta)) > maxStreamDeltaText {
			return nil, false
		}
		return map[string]any{"kind": "text", "delta": delta}, true
	case wukong.MessageEventStreamSnapshot:
		kind, _ := payload["kind"].(string)
		text, _ := payload["text"].(string)
		if kind != "text" || len([]rune(text)) > maxStreamFinalText {
			return nil, false
		}
		return map[string]any{"kind": "text", "text": text}, true
	case wukong.MessageEventStreamClose:
		clean := map[string]any{"end_reason": 0}
		if reason, exists := payload["end_reason"]; exists {
			value, ok := jsonNumberByte(reason)
			if !ok {
				return nil, false
			}
			clean["end_reason"] = value
		}
		for key := range payload {
			if key != "end_reason" {
				return nil, false
			}
		}
		return clean, true
	case wukong.MessageEventStreamError:
		message, _ := payload["error"].(string)
		message = strings.TrimSpace(message)
		if message == "" || len([]rune(message)) > 1000 {
			return nil, false
		}
		return map[string]any{"error": message}, true
	case wukong.MessageEventStreamCancel:
		return nil, len(payload) == 0
	default:
		return nil, false
	}
}

func validStreamEventKey(value string) bool {
	if value == "" || len(value) > 64 || value == "__finish__" {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("_.:-", char) {
			continue
		}
		return false
	}
	return true
}

func jsonNumberByte(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		integer := int(typed)
		return integer, typed == float64(integer) && integer >= 0 && integer <= 255
	case json.Number:
		integer, err := strconv.Atoi(typed.String())
		return integer, err == nil && integer >= 0 && integer <= 255
	case int:
		return typed, typed >= 0 && typed <= 255
	default:
		return 0, false
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
