package app

import (
	"context"
	"errors"
	"github.com/linli/im/server/internal/store"
	"time"
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
		if groupID != "" {
			if manages && a.state.Members[groupID][id] != nil {
				result[id] = true
			}
			continue
		}
		friend := a.state.Friends[actor][id] && !a.state.Blocks[actor][id] && !a.state.Blocks[id][actor]
		if actor == id || friend {
			result[id] = true
		}
	}
	return result, nil
}

func (a *App) PresenceLastOfflineAt(ctx context.Context, ids []string) (map[string]time.Time, error) {
	if len(ids) == 0 {
		return map[string]time.Time{}, nil
	}
	if s, ok := a.persistence.(store.PresenceLastOfflineStore); ok {
		result, err := s.PresenceLastOfflineAt(ctx, ids)
		if !errors.Is(err, store.ErrUnsupported) {
			return result, err
		}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := map[string]time.Time{}
	for _, id := range ids {
		if user := a.state.Users[id]; user != nil && user.LastOfflineAt != nil {
			result[id] = user.LastOfflineAt.UTC()
		}
	}
	return result, nil
}
