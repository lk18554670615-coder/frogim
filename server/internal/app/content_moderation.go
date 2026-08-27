package app

import (
	"context"
	"strings"
	"time"

	"github.com/linli/im/server/internal/store"
)

func (a *App) AdminMomentsPage(ctx context.Context, query, status, cursor string, limit int) ([]*store.Moment, int64, string, error) {
	moderation, ok := a.persistence.(store.ContentModerationStore)
	if !ok {
		return nil, 0, "", store.ErrUnsupported
	}
	items, total, next, err := moderation.ListAdminMoments(ctx, strings.TrimSpace(query), strings.TrimSpace(status), strings.TrimSpace(cursor), limit)
	return items, total, next, mapStoreError(err)
}

func (a *App) ModerateMoment(ctx context.Context, momentID, status, actor, reason string) error {
	moderation, ok := a.persistence.(store.ContentModerationStore)
	if !ok {
		return store.ErrUnsupported
	}
	return mapStoreError(moderation.ModerateMoment(ctx, momentID, status, actor, reason, time.Now()))
}

func (a *App) AdminStickerPacksPage(ctx context.Context, query, status, cursor string, limit int) ([]*store.StickerPack, int64, string, error) {
	moderation, ok := a.persistence.(store.ContentModerationStore)
	if !ok {
		return nil, 0, "", store.ErrUnsupported
	}
	items, total, next, err := moderation.ListAdminStickerPacks(ctx, strings.TrimSpace(query), strings.TrimSpace(status), strings.TrimSpace(cursor), limit)
	return items, total, next, mapStoreError(err)
}
