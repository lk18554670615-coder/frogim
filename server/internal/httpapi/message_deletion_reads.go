package httpapi

import (
	"context"
	"fmt"
	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/wukong"
)

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func wukongMessageID(m wukong.SyncedMessage) string {
	if id, ok := m["message_idstr"].(string); ok && id != "" {
		return id
	}
	return fmt.Sprint(m["message_id"])
}

// Tombstones carry no text or media, including older edit snapshots.
func clientDeletionExtra(extra map[string]any) map[string]any {
	if extra["deletedForEveryoneAt"] == nil {
		return extra
	}
	return map[string]any{"deletedForEveryoneAt": extra["deletedForEveryoneAt"], "deletedForEveryoneBy": extra["deletedForEveryoneBy"], "version": extra["version"]}
}

func filterDeletedWireMessages(ctx context.Context, a *app.App, uid string, messages []wukong.SyncedMessage) ([]wukong.SyncedMessage, error) {
	ids := make([]string, 0, len(messages)*2)
	for _, m := range messages {
		ids = append(ids, wukongMessageID(m))
		if body, ok := wukongStreamProjectedPayload(m); ok {
			if reply, ok := body["reply"].(map[string]any); ok {
				if id, ok := reply["message_id"].(string); ok {
					ids = append(ids, id)
				}
			}
		}
	}
	extras := map[string]map[string]any{}
	for len(ids) > 0 {
		n := min(len(ids), 500)
		batch, err := a.WukongMessageExtensions(ctx, uid, ids[:n])
		if err != nil {
			return nil, err
		}
		for id, extra := range batch {
			extras[id] = extra
		}
		ids = ids[n:]
	}
	result := make([]wukong.SyncedMessage, 0, len(messages))
	for _, m := range messages {
		if extras[wukongMessageID(m)]["deletedForEveryoneAt"] != nil {
			continue
		}
		if body, ok := wukongStreamProjectedPayload(m); ok {
			if reply, ok := body["reply"].(map[string]any); ok {
				if id, ok := reply["message_id"].(string); ok && extras[id]["deletedForEveryoneAt"] != nil {
					delete(body, "reply")
					m["payload"] = body
				}
			}
		}
		result = append(result, m)
	}
	return result, nil
}
