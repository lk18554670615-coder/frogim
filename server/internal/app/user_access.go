package app

import (
	"context"
	"errors"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/netutil"
	"github.com/linli/im/server/internal/store"
	"strings"
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

func (a *App) FriendLoginIPPermission(ctx context.Context, uid string) (bool, error) {
	if s, ok := a.persistence.(store.FriendLoginIPStore); ok {
		allowed, err := s.FriendLoginIPPermission(ctx, uid)
		return allowed, mapStoreError(err)
	}
	return false, ErrForbidden
}

func (a *App) SetFriendLoginIPPermission(ctx context.Context, actor string, userIDs []string, allowed bool, reason, requestIP string) (store.FriendLoginIPPermissionUpdate, error) {
	reason = strings.TrimSpace(reason)
	if len(userIDs) == 0 || len(userIDs) > 100 || reason == "" || len([]rune(reason)) > 500 {
		return store.FriendLoginIPPermissionUpdate{}, ErrInvalid
	}
	clean := make([]string, 0, len(userIDs))
	seen := make(map[string]bool, len(userIDs))
	for _, id := range userIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return store.FriendLoginIPPermissionUpdate{}, ErrInvalid
		}
		seen[id] = true
		clean = append(clean, id)
	}
	if s, ok := a.persistence.(store.FriendLoginIPStore); ok {
		result, err := s.SetFriendLoginIPPermission(ctx, actor, clean, allowed, reason, requestIP)
		return result, mapStoreError(err)
	}
	return store.FriendLoginIPPermissionUpdate{}, ErrForbidden
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
func (a *App) AdminUsersByIP(ctx context.Context, q, status, cursor string, limit int, ip, source, friendIPPermission string) ([]*model.User, int64, string, error) {
	if ip == "" && friendIPPermission == "" {
		return a.AdminUsersPage(q, status, cursor, limit)
	}
	if s, ok := a.persistence.(store.UserAccessStore); ok {
		return s.ListAdminUsersByIP(ctx, q, status, cursor, limit, ip, source, friendIPPermission)
	}
	return nil, 0, "", store.ErrUnsupported
}
