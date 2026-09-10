package app

import (
	"context"
	"strings"

	"github.com/linli/im/server/internal/store"
)

func (a *App) InternalUser(ctx context.Context, uid string) (bool, error) {
	if s, ok := a.persistence.(store.InternalUserStore); ok {
		internal, err := s.InternalUser(ctx, uid)
		return internal, mapStoreError(err)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	user := a.state.Users[uid]
	if user == nil {
		return false, ErrNotFound
	}
	return user.IsInternalUser, nil
}

func (a *App) SetInternalUser(ctx context.Context, actor, uid string, internal bool, reason, requestIP string) (store.InternalUserUpdate, error) {
	reason = strings.TrimSpace(reason)
	if strings.TrimSpace(uid) == "" || reason == "" || len([]rune(reason)) > 500 {
		return store.InternalUserUpdate{}, ErrInvalid
	}
	if s, ok := a.persistence.(store.InternalUserStore); ok {
		result, err := s.SetInternalUser(ctx, actor, uid, internal, reason, requestIP)
		return result, mapStoreError(err)
	}
	return store.InternalUserUpdate{}, ErrForbidden
}
