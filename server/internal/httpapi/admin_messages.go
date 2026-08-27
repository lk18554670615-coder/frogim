package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/wukong"
)

type adminMessageBatch struct {
	loginUID    string
	channelID   string
	channelType uint8
	messageIDs  []int64
}

// loadAdminMessageBodies keeps PostgreSQL as a metadata index and fetches the
// canonical message body from WuKongIM only for the current result page.
func (x *API) loadAdminMessageBodies(ctx context.Context, items []*model.Message) (loaded int, missing int, err error) {
	if len(items) == 0 {
		return 0, 0, nil
	}
	if x.wukongClient == nil {
		return 0, len(items), app.ErrUnavailable
	}

	batches := map[string]*adminMessageBatch{}
	targets := make(map[string]*model.Message, len(items))
	for _, item := range items {
		if item == nil {
			missing++
			continue
		}
		messageID, parseErr := strconv.ParseInt(strings.TrimSpace(item.ID), 10, 64)
		if parseErr != nil || messageID <= 0 || strings.TrimSpace(item.SenderID) == "" || strings.TrimSpace(item.WukongChannelID) == "" || !wukong.SupportedChannelType(item.WukongChannelType) {
			missing++
			continue
		}
		key := fmt.Sprintf("%d\x00%s", item.WukongChannelType, item.WukongChannelID)
		if item.WukongChannelType == wukong.ChannelPerson {
			// Both directions of a direct conversation are searchable through
			// one participant/peer route, so keep the page request bounded.
			key = fmt.Sprintf("%d\x00%s", item.WukongChannelType, item.ConversationID)
		}
		batch := batches[key]
		if batch == nil {
			batch = &adminMessageBatch{loginUID: item.SenderID, channelID: item.WukongChannelID, channelType: item.WukongChannelType}
			batches[key] = batch
		}
		batch.messageIDs = append(batch.messageIDs, messageID)
		targets[item.ID] = item
	}

	seen := make(map[string]struct{}, len(targets))
	for _, batch := range batches {
		messages, searchErr := x.wukongClient.SearchMessages(ctx, wukong.MessageSearchRequest{
			LoginUID: batch.loginUID, ChannelID: batch.channelID, ChannelType: batch.channelType,
			MessageIDs: batch.messageIDs,
		})
		if searchErr != nil {
			return loaded, len(items) - loaded, fmt.Errorf("%w: WuKongIM message lookup failed: %v", app.ErrUnavailable, searchErr)
		}
		for _, raw := range messages {
			messageID := wukongString(raw["message_idstr"])
			if messageID == "" {
				messageID = wukongString(raw["message_id"])
			}
			target := targets[messageID]
			if target == nil {
				continue
			}
			mapped, mapErr := wukongForwardSource(raw, store.WukongMessageRef{
				MessageID: messageID, ConversationID: target.ConversationID,
				ChannelID: target.WukongChannelID, ChannelType: target.WukongChannelType,
			})
			if mapErr != nil {
				payload, ok := wukongStreamProjectedPayload(raw)
				if !ok {
					continue
				}
				body := make(map[string]any, len(payload))
				for key, value := range payload {
					if key != "type" && key != "reply" && key != "mention" {
						body[key] = value
					}
				}
				mapped = &model.Message{Body: body}
			}
			body := mapped.Body
			if len(target.Body) > 0 {
				// An edited body is a business extension and is the current
				// canonical view presented to administrators.
				body = target.Body
			}
			target.Body = body
			if target.ReplyToID == "" {
				target.ReplyToID = mapped.ReplyToID
			}
			seen[messageID] = struct{}{}
			loaded++
		}
	}
	missing += len(targets) - len(seen)
	return loaded, missing, nil
}

func (x *API) recordAdminMessageView(r *http.Request, result string, returned, loaded, missing int, viewErr error) {
	query := r.URL.Query()
	metadata := map[string]any{
		"query": strings.TrimSpace(query.Get("q")), "messageType": strings.TrimSpace(query.Get("type")),
		"cursor": strings.TrimSpace(query.Get("cursor")), "returned": returned,
		"contentLoaded": loaded, "contentMissing": missing,
	}
	if viewErr != nil {
		metadata["error"] = viewErr.Error()
	}
	x.app.RecordAdminAudit(uid(r), "message.search.viewed", "message_search", "results", result, x.clientIP(r), metadata)
}
