package store

import (
	"context"
	"time"

	"github.com/linli/im/server/internal/model"
)

func (p *WithRedis) CreateAdminPasswordUser(ctx context.Context, actor, phone, name, userID, hash, gender, reason string, at time.Time) (*model.User, error) {
	if store, ok := p.base.(AdminUserManagementStore); ok {
		return store.CreateAdminPasswordUser(ctx, actor, phone, name, userID, hash, gender, reason, at)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListAdminUserFriends(ctx context.Context, userID string) ([]AdminUserRelation, error) {
	if store, ok := p.base.(AdminUserManagementStore); ok {
		return store.ListAdminUserFriends(ctx, userID)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListAdminUserBlocks(ctx context.Context, userID string) ([]AdminUserBlock, error) {
	if store, ok := p.base.(AdminUserManagementStore); ok {
		return store.ListAdminUserBlocks(ctx, userID)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) FindDirectConversation(ctx context.Context, userID, friendID string) (*model.Conversation, error) {
	if store, ok := p.base.(AdminUserManagementStore); ok {
		return store.FindDirectConversation(ctx, userID, friendID)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) AdminRecallWukongMessage(ctx context.Context, userID, friendID, messageID, actor, reason string, at time.Time) (bool, string, int64, []string, error) {
	if store, ok := p.base.(AdminUserManagementStore); ok {
		return store.AdminRecallWukongMessage(ctx, userID, friendID, messageID, actor, reason, at)
	}
	return false, "", 0, nil, ErrUnsupported
}

func (p *WithRedis) UpsertClientDevice(ctx context.Context, userID string, device ClientDevice) (*ClientDevice, error) {
	if store, ok := p.base.(ClientDeviceStore); ok {
		return store.UpsertClientDevice(ctx, userID, device)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListClientDevices(ctx context.Context, userID string) ([]ClientDevice, error) {
	if store, ok := p.base.(ClientDeviceStore); ok {
		return store.ListClientDevices(ctx, userID)
	}
	return nil, ErrUnsupported
}
