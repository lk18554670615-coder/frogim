package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/wukong"
)

func newWukongMessageSourceLoader(client *wukong.Client, application *app.App) app.MessageSourceLoader {
	return func(ctx context.Context, userID string, sourceIDs []string) ([]*model.Message, error) {
		var refs []store.WukongMessageRef
		var err error
		deadline := time.NewTimer(750 * time.Millisecond)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer deadline.Stop()
		defer ticker.Stop()
		for {
			refs, err = application.WukongForwardMessageRefs(ctx, userID, sourceIDs)
			if err == nil || !errors.Is(err, store.ErrForbidden) {
				break
			}
			// The send API returns after WuKong ACK, while the searchable
			// business index arrives asynchronously through msg.notify. Bound
			// this race so immediate edit/forward requests do not fail merely
			// because the webhook is a few milliseconds behind the ACK.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-deadline.C:
				return nil, err
			case <-ticker.C:
			}
		}
		if err != nil {
			return nil, err
		}
		type channelBatch struct {
			channelID   string
			channelType uint8
			messageIDs  []int64
		}
		batches := map[string]*channelBatch{}
		refsByID := make(map[string]store.WukongMessageRef, len(refs))
		for _, ref := range refs {
			messageID, parseErr := strconv.ParseInt(ref.MessageID, 10, 64)
			if parseErr != nil || messageID == 0 {
				return nil, store.ErrConflict
			}
			key := fmt.Sprintf("%d\x00%s", ref.ChannelType, ref.ChannelID)
			batch := batches[key]
			if batch == nil {
				batch = &channelBatch{channelID: ref.ChannelID, channelType: ref.ChannelType}
				batches[key] = batch
			}
			batch.messageIDs = append(batch.messageIDs, messageID)
			refsByID[ref.MessageID] = ref
		}
		loaded := make(map[string]*model.Message, len(refs))
		for _, batch := range batches {
			messages, searchErr := client.SearchMessages(ctx, wukong.MessageSearchRequest{
				LoginUID: userID, ChannelID: batch.channelID, ChannelType: batch.channelType,
				MessageIDs: batch.messageIDs,
			})
			if searchErr != nil {
				return nil, searchErr
			}
			for _, message := range messages {
				messageID := wukongString(message["message_idstr"])
				ref, exists := refsByID[messageID]
				if !exists {
					continue
				}
				mapped, mapErr := wukongForwardSource(message, ref)
				if mapErr != nil {
					return nil, mapErr
				}
				loaded[messageID] = mapped
			}
		}
		ordered := make([]*model.Message, 0, len(sourceIDs))
		for _, sourceID := range sourceIDs {
			message := loaded[sourceID]
			if message == nil {
				return nil, store.ErrConflict
			}
			ordered = append(ordered, message)
		}
		if err = application.EnrichWukongMessages(ctx, userID, ordered); err != nil {
			return nil, err
		}
		if _, err = application.WukongForwardMessageRefs(ctx, userID, sourceIDs); err != nil {
			return nil, err
		}
		return ordered, nil
	}
}

func newWukongMessageSearchLoader(client *wukong.Client, application *app.App) app.MessageSearchLoader {
	return func(ctx context.Context, userID, conversationID, query string, before int64, limit int) ([]*model.Message, error) {
		if limit <= 0 || limit > 100 {
			limit = 50
		}
		// `before` is an exclusive WuKong message sequence. There cannot be
		// any older message before sequence one; treating it like zero would
		// accidentally return the newest page again.
		if before == 1 {
			return []*model.Message{}, nil
		}
		route, err := application.ResolveWukongChannel(ctx, userID, conversationID)
		if err != nil {
			return nil, err
		}
		needle := strings.ToLower(strings.TrimSpace(query))
		items := make([]*model.Message, 0, limit)
		var startSequence uint64
		for page := 0; page < 20 && len(items) < limit; page++ {
			output, syncErr := syncVisibleGroupMessages(ctx, client, application, wukong.MessageSyncRequest{
				LoginUID: userID, ChannelID: route.ChannelID, ChannelType: route.ChannelType,
				StartMessageSeq: startSequence, Limit: 500, PullMode: 0, EventSummaryMode: "basic",
			})
			if syncErr != nil {
				return nil, syncErr
			}
			if len(output.Messages) == 0 {
				break
			}
			pageMessages := make([]*model.Message, 0, len(output.Messages))
			minimumSequence := uint64(0)
			for _, raw := range output.Messages {
				sequence := uint64(wukongInt64(raw["message_seq"]))
				if sequence > 0 && (minimumSequence == 0 || sequence < minimumSequence) {
					minimumSequence = sequence
				}
				payload, _ := raw["payload"].(map[string]any)
				if int(wukongInt64(payload["type"])) != wukong.ContentTypeText {
					continue
				}
				messageID := wukongString(raw["message_idstr"])
				mapped, mapErr := wukongForwardSource(raw, store.WukongMessageRef{
					MessageID: messageID, ConversationID: conversationID,
					ChannelID: route.ChannelID, ChannelType: route.ChannelType,
				})
				if mapErr == nil {
					pageMessages = append(pageMessages, mapped)
				}
			}
			if err = application.EnrichWukongMessages(ctx, userID, pageMessages); err != nil {
				return nil, err
			}
			for index := len(pageMessages) - 1; index >= 0 && len(items) < limit; index-- {
				message := pageMessages[index]
				if message.RecalledAt != nil || message.ExpiredAt != nil || (before > 0 && message.CreatedAt.UnixMilli() >= before) {
					continue
				}
				text, _ := message.Body["text"].(string)
				if strings.Contains(strings.ToLower(text), needle) {
					items = append(items, message)
				}
			}
			if output.More == 0 || minimumSequence <= 1 {
				break
			}
			startSequence = minimumSequence - 1
		}
		return filterCurrentGroupHistory(ctx, application, userID, conversationID, route.ChannelType, items)
	}
}

func newWukongMessageHistoryLoader(client *wukong.Client, application *app.App) app.MessageHistoryLoader {
	return func(ctx context.Context, userID, conversationID string, before int64, limit int) ([]*model.Message, error) {
		if before == 1 {
			return []*model.Message{}, nil
		}
		if limit <= 0 || limit > 100 {
			limit = 50
		}
		route, err := application.ResolveWukongChannel(ctx, userID, conversationID)
		if err != nil {
			return nil, err
		}
		startSequence := uint64(0)
		if before > 1 {
			startSequence = uint64(before - 1)
		}
		output, err := syncVisibleGroupMessages(ctx, client, application, wukong.MessageSyncRequest{
			LoginUID: userID, ChannelID: route.ChannelID, ChannelType: route.ChannelType,
			StartMessageSeq: startSequence, Limit: limit, PullMode: 0, EventSummaryMode: "full",
		})
		if err != nil {
			return nil, err
		}
		items := make([]*model.Message, 0, len(output.Messages))
		for index := len(output.Messages) - 1; index >= 0; index-- {
			raw := output.Messages[index]
			messageID := wukongString(raw["message_idstr"])
			message, mapErr := wukongForwardSource(raw, store.WukongMessageRef{
				MessageID: messageID, ConversationID: conversationID,
				ChannelID: route.ChannelID, ChannelType: route.ChannelType,
			})
			if mapErr == nil {
				items = append(items, message)
			}
		}
		if err = application.EnrichWukongMessages(ctx, userID, items); err != nil {
			return nil, err
		}
		return filterCurrentGroupHistory(ctx, application, userID, conversationID, route.ChannelType, items)
	}
}

func wukongForwardSource(raw wukong.SyncedMessage, ref store.WukongMessageRef) (*model.Message, error) {
	payload, ok := wukongStreamProjectedPayload(raw)
	if !ok {
		return nil, store.ErrConflict
	}
	contentType := int(wukongInt64(payload["type"]))
	messageType := map[int]string{
		wukong.ContentTypeText: "text", wukong.ContentTypeImage: "image", wukong.ContentTypeGIF: "image",
		wukong.ContentTypeVoice: "audio", wukong.ContentTypeVideo: "video", wukong.ContentTypeLocation: "location",
		wukong.ContentTypeCard: "contact", wukong.ContentTypeFile: "file", wukong.ContentTypeMergedHistory: "chat_history",
		wukong.ContentTypeSystemEvent: "system", wukong.ContentTypeStoreSticker: "sticker", wukong.ContentTypeMomentShare: "moment",
		wukong.ContentTypeCallEvent: "call", wukong.ContentTypeLiveEvent: "live", wukong.ContentTypeSupportEvent: "support",
		wukong.ContentTypeScreenshot: "screenshot",
	}[contentType]
	if messageType == "" {
		return nil, store.ErrConflict
	}
	body := make(map[string]any, len(payload))
	for key, value := range payload {
		if key != "type" && key != "reply" && key != "mention" {
			body[key] = value
		}
	}
	if messageType == "text" {
		body["text"] = wukongString(payload["content"])
		delete(body, "content")
	}
	replyToID := ""
	if reply, replyOK := payload["reply"].(map[string]any); replyOK {
		replyToID = wukongString(reply["message_id"])
	}
	createdAt := time.Now().UTC()
	if timestamp := wukongInt64(raw["timestamp"]); timestamp > 0 {
		createdAt = time.Unix(timestamp, 0).UTC()
	}
	message := &model.Message{
		ID: ref.MessageID, ClientMsgID: wukongString(raw["client_msg_no"]),
		ConversationID: ref.ConversationID, SenderID: wukongString(raw["from_uid"]),
		Seq: wukongInt64(raw["message_seq"]), Type: messageType, Body: body,
		ReplyToID: replyToID, CreatedAt: createdAt,
	}
	if expireSeconds := wukongInt64(raw["expire"]); expireSeconds > 0 {
		expiresAt := createdAt.Add(time.Duration(expireSeconds) * time.Second)
		message.ExpiresAt = &expiresAt
		if !expiresAt.After(time.Now()) {
			message.ExpiredAt = &expiresAt
		}
	}
	return message, nil
}

func wukongStreamProjectedPayload(raw wukong.SyncedMessage) (map[string]any, bool) {
	source, ok := raw["payload"].(map[string]any)
	if !ok {
		return nil, false
	}
	payload := make(map[string]any, len(source))
	for key, value := range source {
		payload[key] = value
	}
	meta, _ := raw["event_meta"].(map[string]any)
	events, _ := meta["events"].([]any)
	for _, value := range events {
		event, _ := value.(map[string]any)
		if wukongString(event["event_key"]) != "main" {
			continue
		}
		snapshot, _ := event["snapshot"].(map[string]any)
		if snapshot["kind"] == "text" {
			if text, textOK := snapshot["text"].(string); textOK {
				payload["content"] = text
			}
		}
		break
	}
	return payload, true
}

func wukongString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return ""
	}
}

func wukongInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case uint64:
		if typed <= uint64(1<<63-1) {
			return int64(typed)
		}
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	}
	return 0
}

func newWukongMessageTransport(client *wukong.Client, application *app.App) app.MessageTransport {
	// The current deployment is deliberately single-node. A fixed shard set
	// serializes retries for the same sender/client key without an unbounded
	// mutex map; durable duplicate discovery still comes from WuKongIM.
	var sendLocks [256]sync.Mutex
	return func(ctx context.Context, request app.MessageTransportRequest) (app.MessageTransportResult, error) {
		lockKey := request.UserID + "\x00" + request.ClientMsgID
		lock := &sendLocks[crc32.ChecksumIEEE([]byte(lockKey))%uint32(len(sendLocks))]
		lock.Lock()
		defer lock.Unlock()

		route, err := application.AuthorizeWukongMessage(ctx, request)
		if err != nil {
			return app.MessageTransportResult{}, err
		}
		createdAt := time.Now().UTC()
		payload, err := wukongMessagePayload(request, createdAt)
		if err != nil {
			return app.MessageTransportResult{}, err
		}
		existing, err := client.SearchMessages(ctx, wukong.MessageSearchRequest{
			LoginUID: request.UserID, ChannelID: route.ChannelID, ChannelType: route.ChannelType,
			ClientMsgNos: []string{request.ClientMsgID},
		})
		if err != nil {
			return app.MessageTransportResult{}, err
		}
		if len(existing) > 0 {
			return existingWukongMessageResult(existing[0], request, route, payload)
		}
		if mediaID := wukongMessageMediaID(request); mediaID != "" {
			if err = application.BindMediaChannel(store.MediaChannelBinding{
				MediaID: mediaID, ChannelID: route.ChannelID,
				ChannelType: route.ChannelType, SenderID: request.UserID,
			}); err != nil {
				return app.MessageTransportResult{}, err
			}
		}
		result, err := client.SendStoredMessage(ctx, wukong.StoredMessageRequest{
			ClientMsgNo: request.ClientMsgID,
			FromUID:     request.UserID,
			ChannelID:   route.ChannelID,
			ChannelType: route.ChannelType,
			Expire:      uint32(request.ExpiresInSeconds),
			Payload:     payload,
		})
		if err != nil {
			return app.MessageTransportResult{}, err
		}
		return app.MessageTransportResult{
			MessageID:   strconv.FormatInt(result.MessageID, 10),
			ClientMsgID: result.ClientMsgNo,
			CreatedAt:   createdAt,
		}, nil
	}
}

func existingWukongMessageResult(raw wukong.SyncedMessage, request app.MessageTransportRequest, route store.WukongMessageRoute, expectedPayload map[string]any) (app.MessageTransportResult, error) {
	messageID := wukongString(raw["message_idstr"])
	if messageID == "" {
		messageID = wukongString(raw["message_id"])
	}
	existingPayload, payloadOK := raw["payload"].(map[string]any)
	if messageID == "" || wukongString(raw["client_msg_no"]) != request.ClientMsgID ||
		wukongString(raw["from_uid"]) != request.UserID ||
		uint8(wukongInt64(raw["channel_type"])) != route.ChannelType ||
		wukongString(raw["channel_id"]) != route.ChannelID ||
		wukongInt64(raw["expire"]) != request.ExpiresInSeconds || !payloadOK ||
		!sameWukongMessagePayload(existingPayload, expectedPayload) {
		return app.MessageTransportResult{}, store.ErrConflict
	}
	createdAt := time.Now().UTC()
	if timestamp := wukongInt64(raw["timestamp"]); timestamp > 0 {
		createdAt = time.Unix(timestamp, 0).UTC()
	}
	return app.MessageTransportResult{
		MessageID: messageID, MessageSeq: wukongInt64(raw["message_seq"]),
		ClientMsgID: request.ClientMsgID, Duplicate: true, CreatedAt: createdAt,
	}, nil
}

func sameWukongMessagePayload(existing, expected map[string]any) bool {
	copyWithoutPortableExpiry := func(source map[string]any) any {
		copyValue := make(map[string]any, len(source))
		for key, value := range source {
			if key != "expiresAt" {
				copyValue[key] = value
			}
		}
		encoded, err := json.Marshal(copyValue)
		if err != nil {
			return nil
		}
		var normalized any
		if json.Unmarshal(encoded, &normalized) != nil {
			return nil
		}
		return normalized
	}
	return reflect.DeepEqual(copyWithoutPortableExpiry(existing), copyWithoutPortableExpiry(expected))
}

func wukongMessageMediaID(request app.MessageTransportRequest) string {
	switch request.Type {
	case "image", "audio", "video", "file":
		mediaID, _ := request.Body["mediaId"].(string)
		return strings.TrimSpace(mediaID)
	default:
		return ""
	}
}

func newWukongReadStateTransport(client *wukong.Client, application *app.App) app.ReadStateTransport {
	return func(ctx context.Context, userID, conversationID string, readSequence int64) (int64, error) {
		route, err := application.ResolveWukongChannel(ctx, userID, conversationID)
		if err != nil {
			return 0, err
		}
		maximum, err := client.ChannelMaxMessageSeq(ctx, userID, route.ChannelID, route.ChannelType)
		if err != nil {
			return 0, err
		}
		read := uint64(0)
		if readSequence > 0 {
			read = uint64(readSequence)
		}
		if route.ChannelType == wukong.ChannelGroup {
			access, accessErr := application.GroupHistoryAccess(ctx, userID, conversationID)
			if accessErr != nil {
				return 0, accessErr
			}
			read = max(read, uint64(access.UnreadAfterSeq))
		}
		unread := uint64(0)
		if maximum > read {
			unread = maximum - read
		} else {
			read = maximum
		}
		if unread > uint64(^uint(0)>>1) {
			return 0, errors.New("WuKong unread count exceeds platform integer range")
		}
		if err = client.SetConversationUnread(ctx, userID, route.ChannelID, route.ChannelType, int(unread)); err != nil {
			return 0, err
		}
		return int64(read), nil
	}
}

func wukongMessagePayload(request app.MessageTransportRequest, createdAt time.Time) (map[string]any, error) {
	contentType := map[string]int{
		"text":         wukong.ContentTypeText,
		"image":        wukong.ContentTypeImage,
		"audio":        wukong.ContentTypeVoice,
		"video":        wukong.ContentTypeVideo,
		"location":     wukong.ContentTypeLocation,
		"contact":      wukong.ContentTypeCard,
		"file":         wukong.ContentTypeFile,
		"chat_history": wukong.ContentTypeMergedHistory,
	}[request.Type]
	if contentType == 0 {
		return nil, errors.New("unsupported WuKongIM message type")
	}
	payload := make(map[string]any, len(request.Body)+4)
	for key, value := range request.Body {
		payload[key] = value
	}
	if request.Type == "image" && strings.EqualFold(strings.TrimSpace(wukongString(payload["mime"])), "image/gif") {
		contentType = wukong.ContentTypeGIF
	}
	payload["type"] = contentType
	if request.ExpiresInSeconds > 0 {
		payload["expiresAt"] = createdAt.UTC().Add(time.Duration(request.ExpiresInSeconds) * time.Second).Format(time.RFC3339Nano)
	}
	if request.Type == "text" {
		text, _ := payload["text"].(string)
		delete(payload, "text")
		if strings.TrimSpace(text) == "" {
			return nil, errors.New("WuKongIM text content is required")
		}
		payload["content"] = text
	}
	if contentType >= wukong.ContentTypeMergedHistory {
		payload["schemaVersion"] = 1
		if _, exists := payload["digest"]; !exists {
			payload["digest"] = "[聊天记录]"
		}
	}
	if request.ReplyToID != "" {
		payload["reply"] = map[string]any{
			"message_id":  request.ReplyToID,
			"message_seq": 0,
			"from_uid":    "",
			"from_name":   "",
		}
	}
	if len(request.Mentions) > 0 || request.MentionAll {
		mention := map[string]any{"uids": append([]string(nil), request.Mentions...)}
		if request.MentionAll {
			mention["all"] = 1
		}
		payload["mention"] = mention
	}
	return payload, nil
}
