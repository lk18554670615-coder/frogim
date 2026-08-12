package app

import (
	"context"

	"github.com/linli/im/server/internal/store"
)

func (a *App) SaveWukongPluginRelease(ctx context.Context, input store.WukongPluginRelease) (*store.WukongPluginRelease, error) {
	if lifecycle, ok := a.persistence.(store.WukongPluginLifecycleStore); ok {
		return lifecycle.SaveWukongPluginRelease(ctx, input)
	}
	return nil, store.ErrUnsupported
}

func (a *App) WukongPluginRelease(ctx context.Context, pluginNo string) (*store.WukongPluginRelease, error) {
	if lifecycle, ok := a.persistence.(store.WukongPluginLifecycleStore); ok {
		return lifecycle.GetWukongPluginRelease(ctx, pluginNo)
	}
	return nil, store.ErrUnsupported
}

func (a *App) WukongPluginReleases(ctx context.Context) ([]*store.WukongPluginRelease, error) {
	if lifecycle, ok := a.persistence.(store.WukongPluginLifecycleStore); ok {
		return lifecycle.ListWukongPluginReleases(ctx)
	}
	return nil, store.ErrUnsupported
}

func (a *App) RecordWukongPluginEvent(ctx context.Context, input store.WukongPluginEvent) error {
	if lifecycle, ok := a.persistence.(store.WukongPluginLifecycleStore); ok {
		return lifecycle.RecordWukongPluginEvent(ctx, input)
	}
	return store.ErrUnsupported
}

func (a *App) WukongPluginEvents(ctx context.Context, pluginNo string, limit int) ([]*store.WukongPluginEvent, error) {
	if lifecycle, ok := a.persistence.(store.WukongPluginLifecycleStore); ok {
		return lifecycle.ListWukongPluginEvents(ctx, pluginNo, limit)
	}
	return nil, store.ErrUnsupported
}
