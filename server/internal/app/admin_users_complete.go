package app

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"golang.org/x/crypto/bcrypt"
)

var mainlandAdminPhonePattern = regexp.MustCompile(`^1[3-9][0-9]{9}$`)

const maxAdminUserBatchSize = 100

type AdminUserBatchInput struct {
	ClientRow int    `json:"clientRow"`
	Phone     string `json:"phone"`
	Name      string `json:"name"`
	Password  string `json:"password"`
	Gender    string `json:"gender"`
}

type AdminUserBatchItemResult struct {
	ClientRow int         `json:"clientRow"`
	Status    string      `json:"status"`
	User      *model.User `json:"user,omitempty"`
	Code      string      `json:"code,omitempty"`
	Message   string      `json:"message,omitempty"`
}

type preparedAdminUser struct {
	phone, name, password, gender, reason string
}

func (a *App) prepareAdminUser(phone, name, password, gender, reason string) (preparedAdminUser, string, string) {
	prepared := preparedAdminUser{
		phone: strings.TrimPrefix(strings.TrimSpace(phone), "+86"), name: strings.TrimSpace(name),
		password: password, gender: strings.TrimSpace(gender), reason: strings.TrimSpace(reason),
	}
	if !mainlandAdminPhonePattern.MatchString(prepared.phone) {
		return prepared, "INVALID_PHONE", "请输入有效的中国大陆手机号"
	}
	if prepared.name == "" || len([]rune(prepared.name)) > 40 {
		return prepared, "INVALID_NAME", "昵称不能为空且不能超过 40 个字符"
	}
	if !a.validPassword(prepared.password) {
		return prepared, "INVALID_PASSWORD", "初始密码不符合当前密码策略"
	}
	if prepared.gender != "unspecified" && prepared.gender != "male" && prepared.gender != "female" {
		return prepared, "INVALID_GENDER", "性别必须为 male、female 或 unspecified"
	}
	if prepared.reason == "" || len([]rune(prepared.reason)) > 500 {
		return prepared, "INVALID_REASON", "操作理由不能为空且不能超过 500 个字符"
	}
	return prepared, "", ""
}

func (a *App) CreateAdminUser(ctx context.Context, actor, phone, name, password, gender, reason string) (*model.User, error) {
	prepared, code, _ := a.prepareAdminUser(phone, name, password, gender, reason)
	if code != "" {
		return nil, ErrInvalid
	}
	return a.createPreparedAdminUser(ctx, actor, prepared)
}

func (a *App) createPreparedAdminUser(ctx context.Context, actor string, prepared preparedAdminUser) (*model.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(prepared.password), 12)
	if err != nil {
		return nil, err
	}
	if users, ok := a.persistence.(store.AdminUserManagementStore); ok {
		callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		user, createErr := users.CreateAdminPasswordUser(callCtx, actor, prepared.phone, prepared.name, id("usr"), string(hash), prepared.gender, prepared.reason, time.Now().UTC())
		return user, mapStoreError(createErr)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.PhoneToUser[prepared.phone] != "" {
		return nil, ErrConflict
	}
	user := &model.User{ID: id("usr"), Phone: prepared.phone, Name: prepared.name, Gender: prepared.gender, AllowSearchByHandle: true, AllowSearchByPhone: true, CreatedAt: time.Now().UTC()}
	user.Handle = defaultHandle(user.ID)
	a.state.Users[user.ID] = user
	a.state.PhoneToUser[prepared.phone] = user.ID
	a.passwordHashes[user.ID] = string(hash)
	if err = a.saveLocked(); err != nil {
		return nil, err
	}
	return user, nil
}

func (a *App) CreateAdminUsersBatch(ctx context.Context, actor string, inputs []AdminUserBatchInput, reason string) (string, []AdminUserBatchItemResult, error) {
	reason = strings.TrimSpace(reason)
	if len(inputs) == 0 || len(inputs) > maxAdminUserBatchSize || reason == "" || len([]rune(reason)) > 500 {
		return "", nil, ErrInvalid
	}
	batchID := id("batch")
	results := make([]AdminUserBatchItemResult, len(inputs))
	prepared := make([]preparedAdminUser, len(inputs))
	ready := make([]int, 0, len(inputs))
	phoneCounts := make(map[string]int, len(inputs))
	for index, input := range inputs {
		results[index] = AdminUserBatchItemResult{ClientRow: input.ClientRow, Status: "failed"}
		item, code, message := a.prepareAdminUser(input.Phone, input.Name, input.Password, input.Gender, reason)
		if code != "" {
			results[index].Code, results[index].Message = code, message
			continue
		}
		prepared[index] = item
		phoneCounts[item.phone]++
	}
	for index, item := range prepared {
		if item.phone == "" {
			continue
		}
		if phoneCounts[item.phone] > 1 {
			results[index].Code, results[index].Message = "DUPLICATE_IN_FILE", "手机号在导入文件中重复"
			continue
		}
		ready = append(ready, index)
	}

	jobs := make(chan int)
	workers := min(4, len(ready))
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			for index := range jobs {
				user, err := a.createPreparedAdminUser(ctx, actor, prepared[index])
				if err == nil {
					results[index].Status, results[index].User = "created", user
					continue
				}
				switch {
				case errors.Is(err, ErrConflict):
					results[index].Code, results[index].Message = "PHONE_ALREADY_EXISTS", "手机号已存在"
				case errors.Is(err, ErrInvalid):
					results[index].Code, results[index].Message = "INVALID_ARGUMENT", "用户资料不符合要求"
				case errors.Is(err, ErrUnavailable), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
					results[index].Code, results[index].Message = "SERVICE_UNAVAILABLE", "用户服务暂时不可用"
				default:
					results[index].Code, results[index].Message = "INTERNAL", "创建用户失败"
				}
			}
		}()
	}
	for _, index := range ready {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
	return batchID, results, nil
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
