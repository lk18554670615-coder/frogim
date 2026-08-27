package app

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"golang.org/x/crypto/bcrypt"
)

var mainlandAdminPhonePattern = regexp.MustCompile(`^1[3-9][0-9]{9}$`)

func (a *App) CreateAdminUser(ctx context.Context, actor, phone, name, password, gender, reason string) (*model.User, error) {
	phone, name, gender, reason = strings.TrimSpace(phone), strings.TrimSpace(name), strings.TrimSpace(gender), strings.TrimSpace(reason)
	phone = strings.TrimPrefix(phone, "+86")
	if !mainlandAdminPhonePattern.MatchString(phone) || name == "" || len([]rune(name)) > 40 || !a.validPassword(password) ||
		(gender != "unspecified" && gender != "male" && gender != "female") || reason == "" || len([]rune(reason)) > 500 {
		return nil, ErrInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, err
	}
	if users, ok := a.persistence.(store.AdminUserManagementStore); ok {
		callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		user, createErr := users.CreateAdminPasswordUser(callCtx, actor, phone, name, id("usr"), string(hash), gender, reason, time.Now().UTC())
		return user, mapStoreError(createErr)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.PhoneToUser[phone] != "" {
		return nil, ErrConflict
	}
	user := &model.User{ID: id("usr"), Phone: phone, Name: name, Gender: gender, AllowSearchByHandle: true, CreatedAt: time.Now().UTC()}
	user.Handle = defaultHandle(user.ID)
	a.state.Users[user.ID] = user
	a.state.PhoneToUser[phone] = user.ID
	a.passwordHashes[user.ID] = string(hash)
	if err = a.saveLocked(); err != nil {
		return nil, err
	}
	return user, nil
}

func (a *App) AdminUserFriends(ctx context.Context, userID string) ([]store.AdminUserRelation, error) {
	if users, ok := a.persistence.(store.AdminUserManagementStore); ok {
		items, err := users.ListAdminUserFriends(ctx, userID)
		return items, mapStoreError(err)
	}
	items, err := a.FriendsContext(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := make([]store.AdminUserRelation, 0, len(items))
	for _, user := range items {
		result = append(result, store.AdminUserRelation{User: user, Remark: user.Remark, Tags: user.Tags})
	}
	return result, nil
}

func (a *App) AdminUserBlocks(ctx context.Context, userID string) ([]store.AdminUserBlock, error) {
	if users, ok := a.persistence.(store.AdminUserManagementStore); ok {
		items, err := users.ListAdminUserBlocks(ctx, userID)
		return items, mapStoreError(err)
	}
	items, err := a.BlockedUsers(userID)
	if err != nil {
		return nil, err
	}
	result := make([]store.AdminUserBlock, 0, len(items))
	for _, user := range items {
		result = append(result, store.AdminUserBlock{User: user})
	}
	return result, nil
}

func (a *App) UpsertClientDevice(ctx context.Context, userID string, device store.ClientDevice) (*store.ClientDevice, error) {
	device.InstallationID = strings.TrimSpace(device.InstallationID)
	device.Platform = strings.TrimSpace(device.Platform)
	device.DeviceName = strings.TrimSpace(device.DeviceName)
	device.DeviceModel = strings.TrimSpace(device.DeviceModel)
	device.OSVersion = strings.TrimSpace(device.OSVersion)
	device.AppVersion = strings.TrimSpace(device.AppVersion)
	validPlatform := device.Platform == "android" || device.Platform == "ios" || device.Platform == "web" || device.Platform == "macos"
	if device.InstallationID == "" || len(device.InstallationID) > 128 || !validPlatform ||
		len([]rune(device.DeviceName)) > 160 || len([]rune(device.DeviceModel)) > 160 ||
		len([]rune(device.OSVersion)) > 160 || device.AppVersion == "" || len([]rune(device.AppVersion)) > 80 {
		return nil, ErrInvalid
	}
	device.LastSeenAt = time.Now().UTC()
	if devices, ok := a.persistence.(store.ClientDeviceStore); ok {
		item, err := devices.UpsertClientDevice(ctx, userID, device)
		return item, mapStoreError(err)
	}
	return nil, ErrUnavailable
}

func (a *App) ClientDevices(ctx context.Context, userID string) ([]store.ClientDevice, error) {
	if devices, ok := a.persistence.(store.ClientDeviceStore); ok {
		items, err := devices.ListClientDevices(ctx, userID)
		return items, mapStoreError(err)
	}
	return nil, ErrUnavailable
}

func (a *App) AdminDirectHistory(ctx context.Context, userID, friendID string, before int64, limit int) (*model.Conversation, []*model.Message, error) {
	userID, friendID = strings.TrimSpace(userID), strings.TrimSpace(friendID)
	if userID == "" || friendID == "" || userID == friendID {
		return nil, nil, ErrInvalid
	}
	var conversation *model.Conversation
	if users, ok := a.persistence.(store.AdminUserManagementStore); ok {
		var err error
		conversation, err = users.FindDirectConversation(ctx, userID, friendID)
		if err != nil {
			return nil, nil, mapStoreError(err)
		}
	} else {
		a.mu.RLock()
		conversation = a.state.Conversations[a.state.DirectIndex[pair(userID, friendID)]]
		a.mu.RUnlock()
		if conversation == nil {
			return nil, nil, ErrNotFound
		}
	}
	items, err := a.History(userID, conversation.ID, before, limit)
	return conversation, items, err
}

func (a *App) AdminRecallMessage(ctx context.Context, actor, userID, friendID, messageID, reason string) (bool, string, int64, error) {
	reason = strings.TrimSpace(reason)
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(messageID) == "" || reason == "" || len([]rune(reason)) > 500 {
		return false, "", 0, ErrInvalid
	}
	if users, ok := a.persistence.(store.AdminUserManagementStore); ok {
		already, conversationID, sequence, _, err := users.AdminRecallWukongMessage(ctx, userID, friendID, messageID, actor, reason, time.Now().UTC())
		return already, conversationID, sequence, mapStoreError(err)
	}
	return false, "", 0, ErrUnavailable
}
