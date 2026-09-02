package app

import (
	"context"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"time"
)

func (a *App) SetGroupHistoryBoundaryReader(reader store.GroupHistoryBoundaryReader) {
	if s, ok := a.persistence.(store.GroupHistoryStore); ok {
		s.SetGroupHistoryBoundaryReader(reader)
	}
}
func (a *App) GroupHistoryAccess(ctx context.Context, uid, cid string) (*model.HistoryAccess, error) {
	if s, ok := a.persistence.(store.GroupHistoryStore); ok {
		h, err := s.GroupHistoryAccess(ctx, uid, cid)
		return h, mapStoreError(err)
	}
	// In-memory fixtures have no historical group policies. Fail closed for any
	// missing membership; initial in-memory groups retain sequence zero.
	a.mu.RLock()
	defer a.mu.RUnlock()
	m := a.state.Members[cid][uid]
	if m == nil {
		return nil, ErrForbidden
	}
	h := &model.HistoryAccess{Version: 1, AfterSeq: m.HistoryAfterSeq}
	if h.AfterSeq == nil {
		t := m.JoinedAt.Unix()
		h.AfterTimestamp = &t
	}
	return h, nil
}
func (a *App) SetAdminGroupHistoryVisibility(ctx context.Context, actor, cid string, visible bool, reason string) error {
	if s, ok := a.persistence.(store.GroupHistoryStore); ok {
		return mapStoreError(s.SetAdminGroupHistoryVisibility(ctx, actor, cid, visible, reason, time.Now()))
	}
	return ErrUnavailable
}
