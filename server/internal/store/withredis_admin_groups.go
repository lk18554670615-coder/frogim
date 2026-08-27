package store

import (
	"context"
	"time"
)

func (p *WithRedis) ListAdminGroupsScoped(ctx context.Context, q, scope, status, cursor string, limit int) ([]map[string]any, int64, string, error) {
	if s, ok := p.base.(AdminGroupManagementStore); ok {
		return s.ListAdminGroupsScoped(ctx, q, scope, status, cursor, limit)
	}
	return nil, 0, "", ErrUnsupported
}
func (p *WithRedis) ListAdminGroupBlacklist(ctx context.Context, groupID string) ([]AdminGroupBlacklistEntry, error) {
	if s, ok := p.base.(AdminGroupManagementStore); ok {
		return s.ListAdminGroupBlacklist(ctx, groupID)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) AdminAddGroupBlacklist(ctx context.Context, actor, groupID, userID, remark string, at time.Time) error {
	if s, ok := p.base.(AdminGroupManagementStore); ok {
		return s.AdminAddGroupBlacklist(ctx, actor, groupID, userID, remark, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) AdminRemoveGroupBlacklist(ctx context.Context, actor, groupID, userID, reason string, at time.Time) error {
	if s, ok := p.base.(AdminGroupManagementStore); ok {
		return s.AdminRemoveGroupBlacklist(ctx, actor, groupID, userID, reason, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) AdminSetGroupMuteAll(ctx context.Context, actor, groupID string, muted bool, reason string, at time.Time) error {
	if s, ok := p.base.(AdminGroupManagementStore); ok {
		return s.AdminSetGroupMuteAll(ctx, actor, groupID, muted, reason, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) AdminSetGroupBan(ctx context.Context, actor, groupID string, banned bool, reason string, at time.Time) error {
	if s, ok := p.base.(AdminGroupManagementStore); ok {
		return s.AdminSetGroupBan(ctx, actor, groupID, banned, reason, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) AdminRecallGroupWukongMessage(ctx context.Context, groupID, messageID, actor, reason string, at time.Time) (bool, int64, []string, error) {
	if s, ok := p.base.(AdminGroupManagementStore); ok {
		return s.AdminRecallGroupWukongMessage(ctx, groupID, messageID, actor, reason, at)
	}
	return false, 0, nil, ErrUnsupported
}
func (p *WithRedis) LoadAdminGroupMessageExtensions(ctx context.Context, groupID string, messageIDs []string) (map[string]map[string]any, error) {
	if s, ok := p.base.(AdminGroupManagementStore); ok {
		return s.LoadAdminGroupMessageExtensions(ctx, groupID, messageIDs)
	}
	return nil, ErrUnsupported
}
