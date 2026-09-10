package app

import (
	"context"
	"github.com/linli/im/server/internal/store"
)

func (a *App) DeletedUnreadCount(ctx context.Context, uid, ch string, kind uint8, after, last int64) (int, error) {
	if s, ok := a.persistence.(store.MessageDeletionStore); ok {
		return s.DeletedUnreadCount(ctx, uid, ch, kind, after, last)
	}
	return 0, nil
}
func (a *App) DeleteMessagesForEveryone(ctx context.Context, uid, cid string, ids []string, ip string) (store.MessageDeletionResult, error) {
	if s, ok := a.persistence.(store.MessageDeletionStore); ok {
		result, err := s.DeleteMessagesForEveryone(ctx, uid, cid, ids, ip)
		return result, mapStoreError(err)
	}
	return store.MessageDeletionResult{}, ErrForbidden
}
