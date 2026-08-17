package app

import (
	"context"
	"strings"
	"time"

	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
)

func (a *App) AdminGroupsScopedPage(q, scope, status, cursor string, limit int) ([]map[string]any, int64, string, error) {
	if s, ok := a.persistence.(store.AdminGroupManagementStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		items, total, next, err := s.ListAdminGroupsScoped(ctx, q, scope, status, cursor, limit)
		return items, total, next, mapStoreError(err)
	}
	return a.AdminGroupsPage(q, status, cursor, limit)
}

func (a *App) AdminGroupBlacklist(ctx context.Context, groupID string) ([]store.AdminGroupBlacklistEntry, error) {
	if s, ok := a.persistence.(store.AdminGroupManagementStore); ok {
		items, err := s.ListAdminGroupBlacklist(ctx, strings.TrimSpace(groupID))
		return items, mapStoreError(err)
	}
	return nil, ErrUnavailable
}

func (a *App) AdminAddGroupBlacklist(ctx context.Context, actor, groupID, userID, remark string) error {
	actor, groupID, userID, remark = strings.TrimSpace(actor), strings.TrimSpace(groupID), strings.TrimSpace(userID), strings.TrimSpace(remark)
	if actor == "" || groupID == "" || userID == "" || len([]rune(remark)) > 500 {
		return ErrInvalid
	}
	if s, ok := a.persistence.(store.AdminGroupManagementStore); ok {
		err := s.AdminAddGroupBlacklist(ctx, actor, groupID, userID, remark, time.Now().UTC())
		if err == nil {
			a.publish([]string{userID}, "group.members.updated", map[string]any{"conversationId": groupID, "userId": userID, "action": "blacklisted"})
		}
		return mapStoreError(err)
	}
	return ErrUnavailable
}
func (a *App) AdminRemoveGroupBlacklist(ctx context.Context, actor, groupID, userID, reason string) error {
	if s, ok := a.persistence.(store.AdminGroupManagementStore); ok {
		return mapStoreError(s.AdminRemoveGroupBlacklist(ctx, strings.TrimSpace(actor), strings.TrimSpace(groupID), strings.TrimSpace(userID), strings.TrimSpace(reason), time.Now().UTC()))
	}
	return ErrUnavailable
}
func (a *App) AdminSetGroupMuteAll(ctx context.Context, actor, groupID string, muted bool, reason string) error {
	if s, ok := a.persistence.(store.AdminGroupManagementStore); ok {
		err := s.AdminSetGroupMuteAll(ctx, strings.TrimSpace(actor), strings.TrimSpace(groupID), muted, strings.TrimSpace(reason), time.Now().UTC())
		if err == nil {
			ids, _ := a.InternalConversationMemberIDs(ctx, groupID)
			a.publish(ids, "group.profile.updated", map[string]any{"conversationId": groupID, "allMuted": muted})
		}
		return mapStoreError(err)
	}
	return ErrUnavailable
}
func (a *App) AdminSetGroupBan(ctx context.Context, actor, groupID string, banned bool, reason string) error {
	if s, ok := a.persistence.(store.AdminGroupManagementStore); ok {
		err := s.AdminSetGroupBan(ctx, strings.TrimSpace(actor), strings.TrimSpace(groupID), banned, strings.TrimSpace(reason), time.Now().UTC())
		if err == nil {
			ids, _ := a.InternalConversationMemberIDs(ctx, groupID)
			a.publish(ids, "group.profile.updated", map[string]any{"conversationId": groupID, "banned": banned})
		}
		return mapStoreError(err)
	}
	return ErrUnavailable
}
func (a *App) AdminRecallGroupMessage(ctx context.Context, actor, groupID, messageID, reason string) (bool, int64, error) {
	if s, ok := a.persistence.(store.AdminGroupManagementStore); ok {
		already, seq, _, err := s.AdminRecallGroupWukongMessage(ctx, strings.TrimSpace(groupID), strings.TrimSpace(messageID), strings.TrimSpace(actor), strings.TrimSpace(reason), time.Now().UTC())
		return already, seq, mapStoreError(err)
	}
	return false, 0, ErrUnavailable
}
func (a *App) EnrichAdminGroupMessages(ctx context.Context, groupID string, messages []*model.Message) error {
	if len(messages) == 0 {
		return nil
	}
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		if message != nil {
			ids = append(ids, message.ID)
		}
	}
	if s, ok := a.persistence.(store.AdminGroupManagementStore); ok {
		extensions, err := s.LoadAdminGroupMessageExtensions(ctx, strings.TrimSpace(groupID), ids)
		if err != nil {
			return mapStoreError(err)
		}
		for _, message := range messages {
			if message != nil {
				applyModelMessageExtension(message, extensions[message.ID])
			}
		}
		return nil
	}
	return ErrUnavailable
}
func (a *App) AdminSendGroupMessage(ctx context.Context, senderID, groupID, content string) (*model.Message, bool, error) {
	senderID, groupID, content = strings.TrimSpace(senderID), strings.TrimSpace(groupID), strings.TrimSpace(content)
	if senderID == "" || groupID == "" || content == "" || len([]rune(content)) > a.settingInt("maxMessageTextLength", 5000) {
		return nil, false, ErrInvalid
	}
	return a.sendMessage(ctx, senderID, groupID, id("admin-proxy"), "text", map[string]any{"text": content, "adminProxy": true}, "", 0, true)
}
