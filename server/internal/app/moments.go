package app

import (
	"context"
	"time"

	"github.com/linli/im/server/internal/store"
)

func (a *App) CreateMoment(ctx context.Context, userID, content, mediaKind, visibility string, mediaIDs, visibleUserIDs []string, location map[string]any) (*store.Moment, error) {
	if moments, ok := a.persistence.(store.MomentStore); ok {
		item, err := moments.CreateMoment(ctx, store.MomentCreate{
			ID: id("moment"), AuthorID: userID, Content: content,
			MediaKind: mediaKind, MediaIDs: mediaIDs, Visibility: visibility,
			VisibleUserIDs: visibleUserIDs, Location: location, At: time.Now(),
		})
		return item, mapStoreError(err)
	}
	return nil, store.ErrUnsupported
}

func (a *App) Moments(ctx context.Context, viewerID, authorID, after string, limit int) ([]*store.Moment, string, error) {
	if moments, ok := a.persistence.(store.MomentStore); ok {
		items, next, err := moments.ListMoments(ctx, viewerID, authorID, after, limit)
		return items, next, mapStoreError(err)
	}
	return nil, "", store.ErrUnsupported
}

func (a *App) SetMomentLike(ctx context.Context, userID, momentID string, active bool) (*store.Moment, error) {
	if moments, ok := a.persistence.(store.MomentStore); ok {
		item, err := moments.SetMomentLike(ctx, userID, momentID, active, time.Now())
		return item, mapStoreError(err)
	}
	return nil, store.ErrUnsupported
}

func (a *App) CreateMomentComment(ctx context.Context, userID, momentID, parentID, content string) (*store.MomentComment, error) {
	if moments, ok := a.persistence.(store.MomentStore); ok {
		item, err := moments.CreateMomentComment(ctx, id("moment_comment"), userID, momentID, parentID, content, time.Now())
		return item, mapStoreError(err)
	}
	return nil, store.ErrUnsupported
}

func (a *App) DeleteMoment(ctx context.Context, userID, momentID string) error {
	if moments, ok := a.persistence.(store.MomentStore); ok {
		return mapStoreError(moments.DeleteMoment(ctx, userID, momentID, time.Now()))
	}
	return store.ErrUnsupported
}

func (a *App) DeleteMomentComment(ctx context.Context, userID, momentID, commentID string) error {
	if moments, ok := a.persistence.(store.MomentStore); ok {
		return mapStoreError(moments.DeleteMomentComment(ctx, userID, momentID, commentID, time.Now()))
	}
	return store.ErrUnsupported
}

func (a *App) MomentReminders(ctx context.Context, userID string, limit int) ([]*store.MomentReminder, error) {
	if moments, ok := a.persistence.(store.MomentStore); ok {
		items, err := moments.ListMomentReminders(ctx, userID, limit)
		return items, mapStoreError(err)
	}
	return nil, store.ErrUnsupported
}

func (a *App) MarkMomentRemindersRead(ctx context.Context, userID string, reminderIDs []int64) error {
	if moments, ok := a.persistence.(store.MomentStore); ok {
		return mapStoreError(moments.MarkMomentRemindersRead(ctx, userID, reminderIDs, time.Now()))
	}
	return store.ErrUnsupported
}
