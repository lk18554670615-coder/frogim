package httpapi

import (
	"context"
	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/wukong"
	"net/http"
	"time"
)

func (x *API) adminGroupHistoryVisibility(w http.ResponseWriter, r *http.Request) {
	var input struct {
		HistoryVisibleToNewMembers *bool  `json:"historyVisibleToNewMembers"`
		Confirmed                  bool   `json:"confirmed"`
		Reason                     string `json:"reason"`
	}
	if decode(r, &input) != nil || input.HistoryVisibleToNewMembers == nil || !confirmedReason(input.Confirmed, input.Reason) {
		writeError(w, 400, "CONFIRMATION_REQUIRED", "historyVisibleToNewMembers, confirmed and reason are required")
		return
	}
	if err := x.app.SetAdminGroupHistoryVisibility(r.Context(), uid(r), r.PathValue("id"), *input.HistoryVisibleToNewMembers, input.Reason); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]any{"ok": true})
}

// Recheck after remote IO/enrichment, including searches that span several pages.
func filterCurrentGroupHistory(ctx context.Context, application *app.App, uid, cid string, channelType uint8, messages []*model.Message) ([]*model.Message, error) {
	if err := application.EnrichWukongMessages(ctx, uid, messages); err != nil {
		return nil, err
	}
	filtered := make([]*model.Message, 0, len(messages))
	for _, m := range messages {
		if m.DeletedForEveryoneAt == nil {
			filtered = append(filtered, m)
		}
	}
	messages = filtered
	if channelType != wukong.ChannelGroup {
		return messages, nil
	}
	access, err := application.GroupHistoryAccess(ctx, uid, cid)
	if err != nil {
		return nil, err
	}
	visible := make([]*model.Message, 0, len(messages))
	for _, message := range messages {
		if access.Allows(message.Seq, message.CreatedAt) {
			visible = append(visible, message)
		}
	}
	return visible, nil
}

// Only business reads go through this adapter. The admin audit loader deliberately
// continues using the internal WuKong client directly.
func syncVisibleGroupMessages(ctx context.Context, client *wukong.Client, a *app.App, request wukong.MessageSyncRequest) (wukong.MessageSyncResponse, error) {
	limit := request.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 500 {
		limit = 500
	}
	result := wukong.MessageSyncResponse{Messages: []wukong.SyncedMessage{}}
	for {
		request.Limit = limit - len(result.Messages)
		page, err := syncGroupHistoryPage(ctx, client, a, request)
		if err != nil {
			return result, err
		}
		visible, err := filterDeletedWireMessages(ctx, a, request.LoginUID, page.Messages)
		if err != nil {
			return result, err
		}
		result.StartMessageSeq = page.StartMessageSeq
		result.EndMessageSeq = page.EndMessageSeq
		result.More = page.More
		result.Messages = append(result.Messages, visible...)
		if len(result.Messages) >= limit || page.More == 0 || len(page.Messages) == 0 || len(visible) == len(page.Messages) {
			return result, nil
		}
		lo, hi := uint64(^uint64(0)), uint64(0)
		for _, m := range page.Messages {
			seq := uint64(wukongInt64(m["message_seq"]))
			lo = min(lo, seq)
			hi = max(hi, seq)
		}
		next := hi + 1
		if request.PullMode == 0 {
			if lo <= 1 {
				result.More = 0
				return result, nil
			}
			next = lo - 1
		}
		if request.StartMessageSeq == next {
			result.More = 0
			return result, nil
		}
		request.StartMessageSeq = next
	}
}

func syncGroupHistoryPage(ctx context.Context, client *wukong.Client, a *app.App, request wukong.MessageSyncRequest) (wukong.MessageSyncResponse, error) {
	var access *model.HistoryAccess
	if request.ChannelType == wukong.ChannelGroup {
		var err error
		access, err = a.GroupHistoryAccess(ctx, request.LoginUID, request.ChannelID)
		if err != nil {
			return wukong.MessageSyncResponse{}, err
		}
		if !access.VisibleAll && access.AfterSeq != nil && (request.StartMessageSeq != 0 || request.EndMessageSeq != 0) {
			floor := uint64(*access.AfterSeq)
			if request.PullMode == 0 {
				if request.StartMessageSeq > 0 && request.StartMessageSeq <= floor {
					return wukong.MessageSyncResponse{Messages: []wukong.SyncedMessage{}}, nil
				}
				request.EndMessageSeq = max(request.EndMessageSeq, floor)
			} else if request.StartMessageSeq != 0 || request.EndMessageSeq != 0 {
				request.StartMessageSeq = max(request.StartMessageSeq, floor+1)
			}
		}
	}
	output, err := client.SyncMessages(ctx, request)
	if err != nil {
		return output, err
	}
	if access != nil {
		// Re-read after network IO so a concurrent close cannot return an older ON policy.
		access, err = a.GroupHistoryAccess(ctx, request.LoginUID, request.ChannelID)
		if err != nil {
			return wukong.MessageSyncResponse{}, err
		}
		filtered := filterHistoryMessages(output.Messages, *access)
		if len(filtered) != len(output.Messages) && (request.PullMode == 0 || request.StartMessageSeq == 0) {
			output.More = 0
		}
		output.Messages = filtered
		if !access.VisibleAll && access.AfterSeq != nil && request.PullMode == 0 {
			for _, msg := range filtered {
				if wukongInt64(msg["message_seq"]) <= *access.AfterSeq+1 {
					output.More = 0
				}
			}
		}
	}
	return output, nil
}
func filterHistoryMessages(messages []wukong.SyncedMessage, h model.HistoryAccess) []wukong.SyncedMessage {
	items := make([]wukong.SyncedMessage, 0, len(messages))
	for _, msg := range messages {
		if h.Allows(wukongInt64(msg["message_seq"]), time.Unix(wukongInt64(msg["timestamp"]), 0)) {
			items = append(items, msg)
		}
	}
	return items
}
