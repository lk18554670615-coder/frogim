package app

import (
	"context"
	"github.com/linli/im/server/internal/store"
)

func (a *App) MessageDeletionPermission(ctx context.Context, uid string) (bool, error) {
	if s, ok := a.persistence.(store.MessageDeletionStore); ok {
		return s.MessageDeletionPermission(ctx, uid)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	u := a.state.Users[uid]
	if u == nil {
		return false, ErrNotFound
	}
	return u.CanDeleteMessagesForEveryone, nil
}

func (a *App) DeletedUnreadCount(ctx context.Context, uid, ch string, kind uint8, after, last int64) (int, error) {
	if s, ok := a.persistence.(store.MessageDeletionStore); ok {
		return s.DeletedUnreadCount(ctx, uid, ch, kind, after, last)
	}
	return 0, nil
}
func (a *App) SetMessageDeletionPermission(ctx context.Context, actor, uid string, allowed bool, reason, ip string) error {
	if s, ok := a.persistence.(store.MessageDeletionStore); ok {
		return mapStoreError(s.SetMessageDeletionPermission(ctx, actor, uid, allowed, reason, ip))
	}
	return ErrForbidden
}
func (a *App) DeleteMessagesForEveryone(ctx context.Context, uid, cid string, ids []string, ip string) (store.MessageDeletionResult, error) {
	if s, ok := a.persistence.(store.MessageDeletionStore); ok {
		result, err := s.DeleteMessagesForEveryone(ctx, uid, cid, ids, ip)
		return result, mapStoreError(err)
	}
	return store.MessageDeletionResult{}, ErrForbidden
}
