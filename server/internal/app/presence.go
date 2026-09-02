package app

import (
	"context"
	"errors"
	"github.com/linli/im/server/internal/store"
)

func (a *App) AllowedPresenceTargets(ctx context.Context, actor string, ids []string, groupID string) (map[string]bool, error) {
	if s, ok := a.persistence.(store.PresencePermissionStore); ok {
		result, err := s.AllowedPresenceTargets(ctx, actor, ids, groupID)
		if !errors.Is(err, store.ErrUnsupported) {
			return result, err
		}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := map[string]bool{}
	member := a.state.Members[groupID][actor]
	group := a.state.Conversations[groupID]
	manages := group != nil && group.Type == "group" && member != nil && (member.Role == "owner" || member.Role == "admin")
	for _, id := range ids {
		user := a.state.Users[id]
		if user == nil || user.DeletedAt != nil {
			continue
		}
		friend := a.state.Friends[actor][id] && !a.state.Blocks[actor][id] && !a.state.Blocks[id][actor]
		if actor == id || friend || (manages && a.state.Members[groupID][id] != nil) {
			result[id] = true
		}
	}
	return result, nil
}
