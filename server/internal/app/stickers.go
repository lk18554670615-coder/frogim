package app

import (
	"context"
	"time"

	"github.com/linli/im/server/internal/store"
)

func (a *App) StickerCategories(ctx context.Context, includeDisabled bool) ([]*store.StickerCategory, error) {
	if stickers, ok := a.persistence.(store.StickerStore); ok {
		items, err := stickers.ListStickerCategories(ctx, includeDisabled)
		return items, mapStoreError(err)
	}
	return nil, store.ErrUnsupported
}

func (a *App) StickerPacks(ctx context.Context, userID, categoryID string, includeUnpublished bool) ([]*store.StickerPack, error) {
	if stickers, ok := a.persistence.(store.StickerStore); ok {
		items, err := stickers.ListStickerPacks(ctx, userID, categoryID, includeUnpublished)
		return items, mapStoreError(err)
	}
	return nil, store.ErrUnsupported
}

func (a *App) StickerPack(ctx context.Context, userID, packID string, includeUnpublished bool) (*store.StickerPack, error) {
	if stickers, ok := a.persistence.(store.StickerStore); ok {
		item, err := stickers.GetStickerPack(ctx, userID, packID, includeUnpublished)
		return item, mapStoreError(err)
	}
	return nil, store.ErrUnsupported
}

func (a *App) SetStickerPackFavorite(ctx context.Context, userID, packID string, active bool) error {
	if stickers, ok := a.persistence.(store.StickerStore); ok {
		return mapStoreError(stickers.SetStickerPackFavorite(ctx, userID, packID, active, time.Now()))
	}
	return store.ErrUnsupported
}

func (a *App) SetStickerFavorite(ctx context.Context, userID, stickerID string, active bool) error {
	if stickers, ok := a.persistence.(store.StickerStore); ok {
		return mapStoreError(stickers.SetStickerFavorite(ctx, userID, stickerID, active, time.Now()))
	}
	return store.ErrUnsupported
}

func (a *App) RecordStickerUse(ctx context.Context, userID, stickerID string) error {
	if stickers, ok := a.persistence.(store.StickerStore); ok {
		return mapStoreError(stickers.RecordStickerUse(ctx, userID, stickerID, time.Now()))
	}
	return store.ErrUnsupported
}

func (a *App) RecentStickers(ctx context.Context, userID string, limit int) ([]*store.StickerItem, error) {
	if stickers, ok := a.persistence.(store.StickerStore); ok {
		items, err := stickers.ListRecentStickers(ctx, userID, limit)
		return items, mapStoreError(err)
	}
	return nil, store.ErrUnsupported
}

func (a *App) FavoriteStickers(ctx context.Context, userID string, limit int) ([]*store.StickerItem, error) {
	if stickers, ok := a.persistence.(store.StickerStore); ok {
		items, err := stickers.ListFavoriteStickers(ctx, userID, limit)
		return items, mapStoreError(err)
	}
	return nil, store.ErrUnsupported
}

func (a *App) SaveStickerCategory(ctx context.Context, input store.StickerCategoryInput) (*store.StickerCategory, error) {
	if stickers, ok := a.persistence.(store.StickerStore); ok {
		if input.ID == "" {
			input.ID = id("sticker_category")
		}
		input.At = time.Now()
		item, err := stickers.SaveStickerCategory(ctx, input)
		return item, mapStoreError(err)
	}
	return nil, store.ErrUnsupported
}

func (a *App) SaveStickerPack(ctx context.Context, input store.StickerPackInput) (*store.StickerPack, error) {
	if stickers, ok := a.persistence.(store.StickerStore); ok {
		if input.ID == "" {
			input.ID = id("sticker_pack")
		}
		input.At = time.Now()
		item, err := stickers.SaveStickerPack(ctx, input)
		return item, mapStoreError(err)
	}
	return nil, store.ErrUnsupported
}

func (a *App) ReviewStickerPack(ctx context.Context, packID, status, reason, actorID string) (*store.StickerPack, error) {
	if stickers, ok := a.persistence.(store.StickerStore); ok {
		item, err := stickers.ReviewStickerPack(ctx, packID, status, reason, actorID, time.Now())
		return item, mapStoreError(err)
	}
	return nil, store.ErrUnsupported
}

func (a *App) SaveStickerItem(ctx context.Context, input store.StickerItemInput) (*store.StickerItem, error) {
	if stickers, ok := a.persistence.(store.StickerStore); ok {
		if input.ID == "" {
			input.ID = id("sticker")
		}
		input.At = time.Now()
		item, err := stickers.SaveStickerItem(ctx, input)
		return item, mapStoreError(err)
	}
	return nil, store.ErrUnsupported
}
