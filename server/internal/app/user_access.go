package app

import (
	"context"
	"errors"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/netutil"
	"github.com/linli/im/server/internal/store"
)

// ConversationPeerLoginIP exposes only the last successful login address of
// the other participant in an existing direct conversation. It does not create
// conversations or grant access to registration IPs and authentication logs.
func (a *App) ConversationPeerLoginIP(ctx context.Context, uid, cid, requestIP string) (string, string, error) {
	if s, ok := a.persistence.(store.FriendLoginIPStore); ok {
		peer, ip, err := s.ReadFriendLoginIP(ctx, uid, cid, requestIP)
		return peer, netutil.NormalizeIP(ip), mapStoreError(err)
	}
	return "", "", ErrForbidden
}

func (a *App) RecordUserAccess(ctx context.Context, e store.UserAccessLog) error {
	if s, ok := a.persistence.(store.UserAccessStore); ok {
		return s.RecordUserAccess(ctx, e)
	}
	return store.ErrUnsupported
}
func (a *App) UserAccessProfiles(ctx context.Context, ids []string, ip string) (map[string]store.UserAccessProfile, error) {
	if s, ok := a.persistence.(store.UserAccessStore); ok {
		v, err := s.UserAccessProfiles(ctx, ids, ip)
		if !errors.Is(err, store.ErrUnsupported) {
			return v, err
		}
	}
	return map[string]store.UserAccessProfile{}, nil
}
func (a *App) ListUserAccessLogs(ctx context.Context, q store.UserAccessQuery) (store.UserAccessPage, error) {
	if s, ok := a.persistence.(store.UserAccessStore); ok {
		return s.ListUserAccessLogs(ctx, q)
	}
	return store.UserAccessPage{}, store.ErrUnsupported
}
func (a *App) AdminUsersByIP(ctx context.Context, q, status, cursor string, limit int, ip, source, internalUser string) ([]*model.User, int64, string, error) {
	if ip == "" && internalUser == "" {
		return a.AdminUsersPage(q, status, cursor, limit)
	}
	if s, ok := a.persistence.(store.UserAccessStore); ok {
		return s.ListAdminUsersByIP(ctx, q, status, cursor, limit, ip, source, internalUser)
	}
	return nil, 0, "", store.ErrUnsupported
}
