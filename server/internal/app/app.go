package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
	ErrConflict  = errors.New("conflict")
	ErrInvalid   = errors.New("invalid input")
)

var handlePattern = regexp.MustCompile(`^[a-z0-9_]{6,24}$`)
var callIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
var allowedMessageReactions = map[string]bool{"👍": true, "❤️": true, "😂": true, "😮": true, "😢": true, "😡": true, "👏": true, "🎉": true, "🙏": true}

var reservedHandles = map[string]struct{}{
	"admin": {}, "administrator": {}, "system": {}, "official": {}, "support": {},
	"security": {}, "service": {}, "customer_service": {}, "wechat": {}, "weixin": {},
	"linli": {}, "linlitong": {}, "neighborhood": {},
}

func defaultHandle(userID string) string {
	compact := strings.ToLower(strings.ReplaceAll(userID, "_", ""))
	if len(compact) > 20 {
		compact = compact[len(compact)-20:]
	}
	return "ll_" + compact
}

func validHandle(handle string) bool {
	if !handlePattern.MatchString(handle) {
		return false
	}
	_, reserved := reservedHandles[handle]
	return !reserved && !strings.HasPrefix(handle, "admin_") && !strings.HasPrefix(handle, "official_") && !strings.HasPrefix(handle, "system_")
}

type Metrics struct{ Requests, Messages, WSConnections, Errors atomic.Int64 }

type EventSink func(userIDs []string, typ string, payload any)

type App struct {
	mu                sync.RWMutex
	state             *model.State
	persistence       store.Persistence
	emitMu            sync.RWMutex
	emit              EventSink
	Metrics           Metrics
	refreshSessions   map[string]refreshSession
	passwordHashes    map[string]string
	callMu            sync.Mutex
	calls             map[string]*model.CallSession
	announcements     map[string]*model.Announcement
	announcementReads map[string]map[string]time.Time
	callInviteTTL     time.Duration
	friendMetadata    map[string]store.FriendMetadata
	policyMu          sync.Mutex
	policyLoadedAt    time.Time
}
type refreshSession struct {
	UserID    string
	Hash      []byte
	ExpiresAt time.Time
	Revoked   bool
}

func New(ctx context.Context, p store.Persistence) (*App, error) {
	s := model.NewState()
	if n, ok := p.(store.Normalized); !ok || !n.IsNormalized() {
		loaded, err := p.Load(ctx)
		if err != nil {
			return nil, err
		}
		s = loaded
	}
	a := &App{state: s, persistence: p, refreshSessions: map[string]refreshSession{}, passwordHashes: map[string]string{}, calls: map[string]*model.CallSession{}, announcements: map[string]*model.Announcement{}, announcementReads: map[string]map[string]time.Time{}, callInviteTTL: 30 * time.Second, friendMetadata: map[string]store.FriendMetadata{}}
	a.ensureMaps()
	return a, nil
}

func (a *App) SetEventSink(s EventSink) {
	a.emitMu.Lock()
	a.emit = s
	a.emitMu.Unlock()
}
func (a *App) SetCallInviteTTL(ttl time.Duration) {
	if ttl >= 15*time.Second && ttl <= 2*time.Minute {
		a.callInviteTTL = ttl
	}
}
func (a *App) Ready(ctx context.Context) error { return a.persistence.Ping(ctx) }
func (a *App) Close()                          { a.persistence.Close() }

func (a *App) AllowRate(ctx context.Context, key string, max int, window time.Duration) (bool, error) {
	if limiter, ok := a.persistence.(store.RateLimiterStore); ok {
		return limiter.AllowRate(ctx, key, max, window)
	}
	return false, store.ErrUnsupported
}

func (a *App) ensureMaps() {
	if a.state == nil {
		a.state = model.NewState()
		return
	}
	fresh := model.NewState()
	if a.state.Users == nil {
		a.state.Users = fresh.Users
	}
	if a.state.PhoneToUser == nil {
		a.state.PhoneToUser = fresh.PhoneToUser
	}
	if a.state.FriendRequests == nil {
		a.state.FriendRequests = fresh.FriendRequests
	}
	if a.state.Friends == nil {
		a.state.Friends = fresh.Friends
	}
	if a.state.Blocks == nil {
		a.state.Blocks = fresh.Blocks
	}
	if a.state.Conversations == nil {
		a.state.Conversations = fresh.Conversations
	}
	if a.state.Members == nil {
		a.state.Members = fresh.Members
	}
	if a.state.DirectIndex == nil {
		a.state.DirectIndex = fresh.DirectIndex
	}
	if a.state.Messages == nil {
		a.state.Messages = fresh.Messages
	}
	if a.state.MessageByID == nil {
		a.state.MessageByID = fresh.MessageByID
	}
	if a.state.MessageIdempotency == nil {
		a.state.MessageIdempotency = fresh.MessageIdempotency
	}
	if a.state.MessageEdits == nil {
		a.state.MessageEdits = fresh.MessageEdits
	}
	if a.state.MessageEditRequests == nil {
		a.state.MessageEditRequests = fresh.MessageEditRequests
	}
	if a.state.MessageReactions == nil {
		a.state.MessageReactions = fresh.MessageReactions
	}
	if a.state.GroupMessagePins == nil {
		a.state.GroupMessagePins = fresh.GroupMessagePins
	}
	if a.state.SyncEvents == nil {
		a.state.SyncEvents = fresh.SyncEvents
	}
	if a.state.UserSyncSeq == nil {
		a.state.UserSyncSeq = fresh.UserSyncSeq
	}
	if a.state.Reports == nil {
		a.state.Reports = fresh.Reports
	}
	if a.state.Audits == nil {
		a.state.Audits = fresh.Audits
	}
	if a.state.SensitiveWords == nil {
		a.state.SensitiveWords = fresh.SensitiveWords
	}
	if a.state.Settings == nil {
		a.state.Settings = fresh.Settings
	}
	for key, value := range fresh.Settings {
		if _, exists := a.state.Settings[key]; !exists {
			a.state.Settings[key] = value
		}
	}
	if a.state.Devices == nil {
		a.state.Devices = fresh.Devices
	}
	if a.state.Media == nil {
		a.state.Media = fresh.Media
	}
}

func id(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}
func pair(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + ":" + b
}
func memberIDs(ms map[string]*model.ConversationMember) []string {
	out := make([]string, 0, len(ms))
	for uid := range ms {
		out = append(out, uid)
	}
	return out
}

func (a *App) saveLocked() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.persistence.Save(ctx, a.state)
}
func (a *App) refresh() error {
	n, ok := a.persistence.(store.Normalized)
	if !ok || !n.IsNormalized() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := a.persistence.Load(ctx)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.state = s
	a.ensureMaps()
	a.mu.Unlock()
	return nil
}

func (a *App) SeedDemo() error {
	if s, ok := a.persistence.(store.AuthStore); ok {
		now := time.Now()
		for _, u := range []struct{ id, phone, name string }{{"usr_alice", "13800000001", "Alice"}, {"usr_bob", "13800000002", "Bob"}, {"usr_admin", "13800000000", "Admin"}} {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_, err := s.LoginOrCreateUser(ctx, u.phone, u.name, u.id, now)
			cancel()
			if err != nil {
				return err
			}
		}
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.state.Users) > 0 {
		return nil
	}
	now := time.Now()
	users := []*model.User{{ID: "usr_alice", Phone: "13800000001", Name: "Alice", CreatedAt: now}, {ID: "usr_bob", Phone: "13800000002", Name: "Bob", CreatedAt: now}, {ID: "usr_admin", Phone: "13800000000", Name: "Admin", CreatedAt: now}}
	for _, u := range users {
		a.state.Users[u.ID] = u
		a.state.PhoneToUser[u.Phone] = u.ID
	}
	return a.saveLocked()
}

func (a *App) Login(phone, name string) (*model.User, error) {
	phone = strings.TrimSpace(phone)
	if phone == "" || len(phone) > 32 {
		return nil, ErrInvalid
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) > 80 {
		return nil, ErrInvalid
	}
	if name == "" {
		name = "用户" + phone[max(0, len(phone)-4):]
	}
	if s, ok := a.persistence.(store.AuthStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		u, err := s.LoginOrCreateUser(ctx, phone, name, id("usr"), time.Now())
		if err == store.ErrForbidden {
			return nil, ErrForbidden
		}
		return u, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if uid := a.state.PhoneToUser[phone]; uid != "" {
		u := a.state.Users[uid]
		if u.Banned {
			return nil, ErrForbidden
		}
		return u, nil
	}
	u := &model.User{ID: id("usr"), Phone: phone, Name: name, CreatedAt: time.Now()}
	u.Handle = defaultHandle(u.ID)
	a.state.Users[u.ID] = u
	a.state.PhoneToUser[phone] = u.ID
	if err := a.saveLocked(); err != nil {
		return nil, err
	}
	return u, nil
}

func (a *App) settingBool(key string, fallback bool) bool {
	a.refreshPolicySettings(false)
	a.mu.RLock()
	defer a.mu.RUnlock()
	value, ok := a.state.Settings[key].(bool)
	if !ok {
		return fallback
	}
	return value
}

func (a *App) settingInt(key string, fallback int) int {
	a.refreshPolicySettings(false)
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.settingIntLocked(key, fallback)
}

func (a *App) refreshPolicySettings(force bool) {
	policies, ok := a.persistence.(store.PolicyStore)
	if !ok {
		return
	}
	a.policyMu.Lock()
	defer a.policyMu.Unlock()
	if !force && time.Since(a.policyLoadedAt) < 2*time.Second {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	settings, err := policies.RuntimeSettings(ctx)
	if err != nil {
		return
	}
	a.mu.Lock()
	a.state.Settings = settings
	a.mu.Unlock()
	a.policyLoadedAt = time.Now()
}

func (a *App) settingIntLocked(key string, fallback int) int {
	value, ok := a.state.Settings[key].(float64)
	if ok {
		return int(value)
	}
	integer, ok := a.state.Settings[key].(int)
	if ok {
		return integer
	}
	return fallback
}

func (a *App) validPassword(password string) bool {
	return len(password) >= a.settingInt("passwordMinLength", 8) && len(password) <= 72
}

// MaintenanceStatus exposes only the public maintenance state. It deliberately
// excludes the rest of the runtime settings so user-facing middleware cannot
// leak operational policy or secret configuration.
func (a *App) MaintenanceStatus() (bool, string) {
	enabled := a.settingBool("maintenanceMode", false)
	a.mu.RLock()
	announcement, _ := a.state.Settings["announcement"].(string)
	a.mu.RUnlock()
	return enabled, strings.TrimSpace(announcement)
}

func (a *App) RegisterWithPassword(phone, name, password string) (*model.User, error) {
	phone = strings.TrimSpace(phone)
	name = strings.TrimSpace(name)
	if phone == "" || len(phone) > 32 || name == "" || len([]rune(name)) > 40 || !a.validPassword(password) {
		return nil, ErrInvalid
	}
	settings := a.Settings()
	if enabled, ok := settings["registrationEnabled"].(bool); ok && !enabled {
		return nil, ErrForbidden
	}
	if enabled, ok := settings["allowRegistration"].(bool); ok && !enabled {
		return nil, ErrForbidden
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, err
	}
	if s, ok := a.persistence.(store.PasswordAuthStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		u, err := s.RegisterPasswordUser(ctx, phone, name, id("usr"), string(hash), time.Now())
		if err == store.ErrConflict {
			return nil, ErrConflict
		}
		return u, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.PhoneToUser[phone] != "" {
		return nil, ErrConflict
	}
	u := &model.User{ID: id("usr"), Phone: phone, Name: name, CreatedAt: time.Now()}
	u.Handle = defaultHandle(u.ID)
	a.state.Users[u.ID] = u
	a.state.PhoneToUser[phone] = u.ID
	a.passwordHashes[u.ID] = string(hash)
	if err := a.saveLocked(); err != nil {
		return nil, err
	}
	return u, nil
}

func (a *App) PasswordLogin(phone, password string) (*model.User, error) {
	phone = strings.TrimSpace(phone)
	if phone == "" || password == "" {
		return nil, ErrForbidden
	}
	const dummyHash = "$2a$12$rAyv6obDffJSqZ1aaqOCR.ER2UXp8ZPsEl2bJCTovnsJJrFshtxNW"
	if s, ok := a.persistence.(store.PasswordAuthStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		u, hash, err := s.PasswordCredentials(ctx, phone)
		if err != nil || hash == "" {
			_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
			return nil, ErrForbidden
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || u.Banned {
			return nil, ErrForbidden
		}
		return u, nil
	}
	a.mu.RLock()
	uid := a.state.PhoneToUser[phone]
	u := a.state.Users[uid]
	hash := a.passwordHashes[uid]
	a.mu.RUnlock()
	if u == nil || hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || u.Banned {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
		return nil, ErrForbidden
	}
	copy := *u
	return &copy, nil
}

func (a *App) ResetPassword(phone, password string) error {
	phone = strings.TrimSpace(phone)
	if phone == "" || !a.validPassword(password) {
		return ErrInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	if s, ok := a.persistence.(store.PasswordAuthStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		u, _, lookupErr := s.PasswordCredentials(ctx, phone)
		if lookupErr != nil {
			if lookupErr == store.ErrNotFound {
				return ErrNotFound
			}
			return lookupErr
		}
		if err = s.UpdatePassword(ctx, phone, string(hash), time.Now()); err != nil {
			if err == store.ErrNotFound {
				return ErrNotFound
			}
			return err
		}
		return a.RevokeAllRefreshSessions(u.ID)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	uid := a.state.PhoneToUser[phone]
	if uid == "" {
		return ErrNotFound
	}
	a.passwordHashes[uid] = string(hash)
	for id, session := range a.refreshSessions {
		if session.UserID == uid {
			session.Revoked = true
			a.refreshSessions[id] = session
		}
	}
	return nil
}

func (a *App) User(uid string) (*model.User, error) {
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		u, err := q.GetUser(ctx, uid)
		if err == store.ErrNotFound {
			return nil, ErrNotFound
		}
		return u, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	u := a.state.Users[uid]
	if u == nil {
		return nil, ErrNotFound
	}
	c := *u
	return &c, nil
}

func (a *App) AccountDeleted(uid string) (bool, error) {
	if accounts, ok := a.persistence.(store.AccountStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		deleted, err := accounts.AccountDeleted(ctx, uid)
		return deleted, mapStoreError(err)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	u := a.state.Users[uid]
	if u == nil {
		return false, ErrNotFound
	}
	return u.DeletedAt != nil, nil
}

func (a *App) IsActiveUser(uid string) bool {
	u, err := a.User(uid)
	return err == nil && !u.Banned && u.DeletedAt == nil
}

func (a *App) DeleteAccount(uid string) (bool, error) {
	if accounts, ok := a.persistence.(store.AccountStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		duplicate, err := accounts.DeleteAccount(ctx, uid, time.Now())
		return duplicate, mapStoreError(err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u := a.state.Users[uid]
	if u == nil {
		return false, ErrNotFound
	}
	if u.DeletedAt != nil {
		return true, nil
	}
	for cid, members := range a.state.Members {
		if conversation := a.state.Conversations[cid]; conversation != nil && conversation.Type == "group" {
			if membership := members[uid]; membership != nil && membership.Role == "owner" {
				return false, ErrConflict
			}
		}
	}
	now := time.Now()
	oldPhone := u.Phone
	anonymousToken := id("deleted")
	anonymousSuffix := anonymousToken[len(anonymousToken)-16:]
	anonymousPhone := "deleted_" + anonymousSuffix
	anonymousHandle := "deleted_" + anonymousSuffix
	delete(a.state.PhoneToUser, oldPhone)
	u.Phone, u.Handle, u.Name, u.Signature, u.AvatarMediaID, u.AvatarURL = anonymousPhone, anonymousHandle, "已注销用户", "", "", ""
	u.Banned, u.HandleChangeCount, u.DeletedAt = true, 2, &now
	a.state.PhoneToUser[anonymousPhone] = uid
	delete(a.passwordHashes, uid)
	for id, session := range a.refreshSessions {
		if session.UserID == uid {
			session.Revoked = true
			a.refreshSessions[id] = session
		}
	}
	for id, device := range a.state.Devices {
		if device.UserID == uid {
			delete(a.state.Devices, id)
		}
	}
	for id, request := range a.state.FriendRequests {
		if (request.FromUserID == uid || request.ToUserID == uid) && request.Status == "pending" {
			request.Status, request.UpdatedAt, request.ResolvedAt = "cancelled", now, &now
			a.state.FriendRequests[id] = request
		}
	}
	delete(a.state.Friends, uid)
	delete(a.state.Blocks, uid)
	for other := range a.state.Friends {
		delete(a.state.Friends[other], uid)
	}
	for other := range a.state.Blocks {
		delete(a.state.Blocks[other], uid)
	}
	for _, members := range a.state.Members {
		delete(members, uid)
	}
	a.auditLocked(uid, "account.deleted", "user", uid, map[string]any{"mode": "immediate_anonymization"})
	return false, a.saveLocked()
}

func (a *App) UpdateUserProfile(uid string, update store.UserProfileUpdate) (*model.User, error) {
	if update.Name == nil && update.Handle == nil && update.Signature == nil && update.AvatarMediaID == nil {
		return nil, ErrInvalid
	}
	if update.Name != nil {
		v := strings.TrimSpace(*update.Name)
		if v == "" || len([]rune(v)) > 40 {
			return nil, ErrInvalid
		}
		update.Name = &v
	}
	if update.Handle != nil {
		v := strings.ToLower(strings.TrimSpace(*update.Handle))
		if !validHandle(v) {
			return nil, ErrInvalid
		}
		update.Handle = &v
	}
	if update.Signature != nil {
		v := strings.TrimSpace(*update.Signature)
		if len([]rune(v)) > 160 {
			return nil, ErrInvalid
		}
		update.Signature = &v
	}
	if s, ok := a.persistence.(store.ProfileStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		u, err := s.UpdateUserProfile(ctx, uid, update)
		switch err {
		case store.ErrNotFound:
			return nil, ErrNotFound
		case store.ErrForbidden:
			return nil, ErrForbidden
		case store.ErrConflict:
			return nil, ErrConflict
		}
		return u, err
	}
	if err := a.refresh(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u := a.state.Users[uid]
	if u == nil {
		return nil, ErrNotFound
	}
	if update.Handle != nil {
		if u.Handle != *update.Handle && u.HandleChangeCount >= 2 {
			return nil, ErrForbidden
		}
		for id, candidate := range a.state.Users {
			if id != uid && strings.EqualFold(candidate.Handle, *update.Handle) {
				return nil, ErrConflict
			}
		}
		if u.Handle != *update.Handle {
			u.Handle = *update.Handle
			u.HandleChangeCount++
		}
	}
	if update.Name != nil {
		u.Name = *update.Name
	}
	if update.Signature != nil {
		u.Signature = *update.Signature
	}
	if update.AvatarMediaID != nil {
		if *update.AvatarMediaID != "" {
			m := a.state.Media[*update.AvatarMediaID]
			if m == nil {
				return nil, ErrNotFound
			}
			if m.OwnerID != uid || m.Status != "ready" {
				return nil, ErrForbidden
			}
		}
		u.AvatarMediaID = *update.AvatarMediaID
		u.AvatarURL = ""
		if u.AvatarMediaID != "" {
			u.AvatarURL = "/v1/media/" + u.AvatarMediaID
		}
	}
	if err := a.saveLocked(); err != nil {
		return nil, err
	}
	copy := *u
	return &copy, nil
}

func (a *App) UpdateUserPhone(uid, phone string) (*model.User, error) {
	phone = strings.TrimSpace(phone)
	if phone == "" || len(phone) > 32 {
		return nil, ErrInvalid
	}
	if s, ok := a.persistence.(store.ProfileStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		u, err := s.UpdateUserPhone(ctx, uid, phone)
		if err == store.ErrConflict {
			return nil, ErrConflict
		}
		if err == store.ErrNotFound {
			return nil, ErrNotFound
		}
		return u, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u := a.state.Users[uid]
	if u == nil {
		return nil, ErrNotFound
	}
	if other := a.state.PhoneToUser[phone]; other != "" && other != uid {
		return nil, ErrConflict
	}
	delete(a.state.PhoneToUser, u.Phone)
	u.Phone = phone
	a.state.PhoneToUser[phone] = uid
	if err := a.saveLocked(); err != nil {
		return nil, err
	}
	copy := *u
	return &copy, nil
}

func (a *App) UserDevices(uid string) ([]*model.Device, error) {
	if s, ok := a.persistence.(store.ProfileStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		return s.ListUserDevices(ctx, uid)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	items := []*model.Device{}
	for _, device := range a.state.Devices {
		if device.UserID == uid {
			copy := *device
			copy.PushToken = ""
			items = append(items, &copy)
		}
	}
	return items, nil
}

func (a *App) Favorites(uid string, limit int) ([]*model.Message, error) {
	if s, ok := a.persistence.(store.ProfileStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		return s.ListFavorites(ctx, uid, limit)
	}
	return []*model.Message{}, nil
}

func (a *App) SetFavorite(uid, messageID string, enabled bool) error {
	uid = strings.TrimSpace(uid)
	messageID = strings.TrimSpace(messageID)
	if uid == "" || messageID == "" || len(messageID) > 160 {
		return ErrInvalid
	}
	if s, ok := a.persistence.(store.ProfileStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := s.SetFavorite(ctx, uid, messageID, enabled); err != nil {
			if errors.Is(err, store.ErrForbidden) {
				return ErrForbidden
			}
			if errors.Is(err, store.ErrNotFound) {
				return ErrNotFound
			}
			return err
		}
		return nil
	}
	if !enabled {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, messages := range a.state.Messages {
		for _, message := range messages {
			if message.ID == messageID {
				if a.state.Members[message.ConversationID][uid] == nil {
					return ErrForbidden
				}
				return nil
			}
		}
	}
	return ErrNotFound
}

func (a *App) CreateFeedback(uid, category, content, contact string) (string, error) {
	category = strings.TrimSpace(category)
	content = strings.TrimSpace(content)
	contact = strings.TrimSpace(contact)
	if category == "" {
		category = "other"
	}
	if content == "" || len([]rune(content)) > 2000 || len([]rune(contact)) > 120 || len(category) > 40 {
		return "", ErrInvalid
	}
	id := id("feedback")
	if s, ok := a.persistence.(store.ProfileStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := s.CreateFeedback(ctx, id, uid, category, content, contact, time.Now()); err != nil {
			return "", err
		}
	}
	return id, nil
}
func (a *App) SearchUsers(query string) []*model.User {
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := q.SearchUsers(ctx, strings.TrimSpace(query), 50)
		if err == nil {
			return out
		}
		return []*model.User{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(query))
	out := []*model.User{}
	for _, u := range a.state.Users {
		if q == "" || strings.Contains(strings.ToLower(u.Name), q) || strings.Contains(u.Phone, q) {
			c := *u
			c.Phone = ""
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

func (a *App) SearchUsersByIdentifier(query, by string) ([]*model.User, error) {
	query, by = strings.TrimSpace(query), strings.ToLower(strings.TrimSpace(by))
	if by == "" {
		by = "handle"
	}
	if query == "" || (by != "handle" && by != "phone") {
		return nil, ErrInvalid
	}
	if (by == "handle" && !a.settingBool("allowSearchByHandle", true)) || (by == "phone" && !a.settingBool("allowSearchByPhone", false)) {
		return nil, ErrForbidden
	}
	if by == "handle" {
		query = strings.ToLower(query)
		if !validHandle(query) {
			return nil, ErrInvalid
		}
	} else if len(query) > 32 {
		return nil, ErrInvalid
	}
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := q.SearchUsersByIdentifier(ctx, query, by, 20)
		if err != nil {
			return nil, mapStoreError(err)
		}
		return out, nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := []*model.User{}
	for _, u := range a.state.Users {
		matched := by == "handle" && strings.EqualFold(u.Handle, query)
		matched = matched || (by == "phone" && u.Phone == query)
		if matched {
			copy := *u
			copy.Phone = ""
			out = append(out, &copy)
		}
	}
	return out, nil
}

func (a *App) SearchCapabilities() map[string]bool {
	return map[string]bool{
		"allowSearchByHandle": a.settingBool("allowSearchByHandle", true),
		"allowSearchByPhone":  a.settingBool("allowSearchByPhone", false),
	}
}

func (a *App) DecorateOwnProfile(u *model.User) {
	if u == nil {
		return
	}
	u.HandleChangesRemaining = max(0, 2-u.HandleChangeCount)
	u.AllowSearchByHandle = a.settingBool("allowSearchByHandle", true)
	u.AllowSearchByPhone = a.settingBool("allowSearchByPhone", false)
}

func (a *App) RequestFriend(from, to, message string) (*model.FriendRequest, error) {
	return a.RequestFriendWithSource(from, to, message, "search")
}
func (a *App) RequestFriendWithSource(from, to, message, source string) (*model.FriendRequest, error) {
	return a.RequestFriendWithContext(from, to, message, source, "")
}
func (a *App) RequestFriendWithContext(from, to, message, source, sourceID string) (*model.FriendRequest, error) {
	message, source = strings.TrimSpace(message), strings.ToLower(strings.TrimSpace(source))
	sourceID = strings.TrimSpace(sourceID)
	if source == "" {
		source = "search"
	}
	allowed := map[string]bool{"search": true, "qr": true, "contacts": true, "group": true, "card": true}
	if from == to || len([]rune(message)) > 300 || !allowed[source] || (source == "group" && sourceID == "") {
		return nil, ErrInvalid
	}
	if !a.settingBool("allowFriendRequests", true) {
		return nil, ErrForbidden
	}
	now := time.Now()
	expiryDays := a.settingInt("friendRequestExpiryDays", 7)
	request := &model.FriendRequest{ID: id("fr"), FromUserID: from, ToUserID: to, Message: message, Source: source, SourceID: sourceID, Status: "pending", CreatedAt: now, ExpiresAt: now.Add(time.Duration(expiryDays) * 24 * time.Hour), UpdatedAt: now}
	if friends, ok := a.persistence.(store.FriendStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		created, duplicate, err := friends.CreateFriendRequest(ctx, request)
		if err != nil {
			return nil, mapStoreError(err)
		}
		if !duplicate {
			a.publish([]string{from}, "friend.request.sent", map[string]any{"requestId": created.ID, "userId": to, "status": "pending"})
			a.publish([]string{to}, "friend.request", map[string]any{"request": created})
		}
		return created, nil
	}
	if err := a.refresh(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Users[to] == nil {
		return nil, ErrNotFound
	}
	if a.blockedLocked(from, to) {
		return nil, ErrForbidden
	}
	for _, r := range a.state.FriendRequests {
		if r.FromUserID == from && r.ToUserID == to && r.Status == "pending" {
			return r, nil
		}
	}
	a.state.FriendRequests[request.ID] = request
	e1 := a.syncLocked(from, "friend.request.sent", map[string]any{"requestId": request.ID, "userId": to, "status": "pending"})
	requestSnapshot := *request
	e2 := a.syncLocked(to, "friend.request", map[string]any{"request": &requestSnapshot})
	if err := a.saveLocked(); err != nil {
		return nil, err
	}
	go a.publish([]string{from}, e1.Type, e1)
	go a.publish([]string{to}, e2.Type, e2)
	return request, nil
}
func (a *App) FriendRequests(uid string) []*model.FriendRequest {
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := q.ListFriendRequests(ctx, uid)
		if err == nil {
			return out
		}
		return []*model.FriendRequest{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := []*model.FriendRequest{}
	for _, r := range a.state.FriendRequests {
		if r.ToUserID == uid || r.FromUserID == uid {
			c := *r
			out = append(out, &c)
		}
	}
	return out
}
func (a *App) Friends(uid string) []*model.User {
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := q.ListFriends(ctx, uid)
		if err == nil {
			return out
		}
		return []*model.User{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := []*model.User{}
	for friend := range a.state.Friends[uid] {
		if u := a.state.Users[friend]; u != nil {
			c := *u
			c.Phone = ""
			metadata := a.friendMetadata[uid+":"+friend]
			c.Remark, c.Tags = metadata.Remark, append([]string(nil), metadata.Tags...)
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (a *App) BlockedUsers(uid string) ([]*model.User, error) {
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		return q.ListBlockedUsers(ctx, uid)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	items := []*model.User{}
	for targetID := range a.state.Blocks[uid] {
		if user := a.state.Users[targetID]; user != nil {
			copy := *user
			copy.Phone = ""
			items = append(items, &copy)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}
func (a *App) AcceptFriend(uid, rid string) error {
	_, _, err := a.TransitionFriendRequest(uid, rid, "accept")
	return err
}
func (a *App) RejectFriend(uid, rid string) error {
	_, _, err := a.TransitionFriendRequest(uid, rid, "reject")
	return err
}
func (a *App) CancelFriendRequest(uid, rid string) error {
	_, _, err := a.TransitionFriendRequest(uid, rid, "cancel")
	return err
}
func (a *App) TransitionFriendRequest(uid, rid, action string) (*model.FriendRequest, bool, error) {
	if action != "accept" && action != "reject" && action != "cancel" {
		return nil, false, ErrInvalid
	}
	if friends, ok := a.persistence.(store.FriendStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r, duplicate, err := friends.TransitionFriendRequest(ctx, rid, uid, action, time.Now())
		if err != nil {
			return r, false, mapStoreError(err)
		}
		if !duplicate {
			typ := "friend.request.updated"
			if r.Status == "accepted" {
				typ = "friend.accepted"
			}
			a.publish([]string{r.FromUserID, r.ToUserID}, typ, map[string]any{"requestId": r.ID, "status": r.Status})
		}
		return r, duplicate, nil
	}
	if err := a.refresh(); err != nil {
		return nil, false, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.state.FriendRequests[rid]
	if r == nil {
		return nil, false, ErrNotFound
	}
	if (action == "cancel" && r.FromUserID != uid) || (action != "cancel" && r.ToUserID != uid) {
		return nil, false, ErrForbidden
	}
	target := map[string]string{"accept": "accepted", "reject": "rejected", "cancel": "cancelled"}[action]
	if r.Status == target {
		return r, true, nil
	}
	if r.Status != "pending" || !time.Now().Before(r.ExpiresAt) {
		if r.Status == "pending" {
			now := time.Now()
			r.Status, r.UpdatedAt, r.ResolvedAt = "expired", now, &now
		}
		return r, false, ErrConflict
	}
	now := time.Now()
	r.Status, r.UpdatedAt, r.ResolvedAt = target, now, &now
	if target == "accepted" {
		if a.state.Friends[r.FromUserID] == nil {
			a.state.Friends[r.FromUserID] = map[string]bool{}
		}
		if a.state.Friends[r.ToUserID] == nil {
			a.state.Friends[r.ToUserID] = map[string]bool{}
		}
		a.state.Friends[r.FromUserID][r.ToUserID], a.state.Friends[r.ToUserID][r.FromUserID] = true, true
	}
	typ := "friend.request.updated"
	if target == "accepted" {
		typ = "friend.accepted"
	}
	e1 := a.syncLocked(r.FromUserID, typ, map[string]any{"requestId": rid, "userId": r.ToUserID, "status": target})
	e2 := a.syncLocked(r.ToUserID, typ, map[string]any{"requestId": rid, "userId": r.FromUserID, "status": target})
	if err := a.saveLocked(); err != nil {
		return nil, false, err
	}
	go a.publish([]string{r.FromUserID}, e1.Type, e1)
	go a.publish([]string{r.ToUserID}, e2.Type, e2)
	return r, false, nil
}
func (a *App) Block(uid, target string, blocked bool) error {
	if uid == target {
		return ErrInvalid
	}
	if friends, ok := a.persistence.(store.FriendStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := friends.SetFriendBlock(ctx, uid, target, blocked, time.Now()); err != nil {
			return mapStoreError(err)
		}
		a.publish([]string{uid}, "block.updated", map[string]any{"userId": target, "blocked": blocked})
		return nil
	}
	if err := a.refresh(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Users[target] == nil {
		return ErrNotFound
	}
	if a.state.Blocks[uid] == nil {
		a.state.Blocks[uid] = map[string]bool{}
	}
	if blocked {
		a.state.Blocks[uid][target] = true
		wasFriend := a.state.Friends[uid][target]
		delete(a.state.Friends[uid], target)
		delete(a.state.Friends[target], uid)
		if wasFriend {
			e := a.syncLocked(target, "friend.removed", map[string]any{"userId": uid})
			go a.publish([]string{target}, e.Type, e)
		}
	} else {
		delete(a.state.Blocks[uid], target)
	}
	a.syncLocked(uid, "block.updated", map[string]any{"userId": target, "blocked": blocked})
	if ms, ok := a.persistence.(store.MutationStore); ok {
		if err := ms.SetBlock(context.Background(), uid, target, blocked); err != nil {
			return err
		}
	}
	return a.saveLocked()
}

func (a *App) DeleteFriend(uid, target string) error {
	if uid == target {
		return ErrInvalid
	}
	if friends, ok := a.persistence.(store.FriendStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := friends.DeleteFriend(ctx, uid, target, time.Now()); err != nil {
			return mapStoreError(err)
		}
		a.publish([]string{uid, target}, "friend.removed", map[string]any{})
		return nil
	}
	if err := a.refresh(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.state.Friends[uid][target] {
		return ErrNotFound
	}
	delete(a.state.Friends[uid], target)
	delete(a.state.Friends[target], uid)
	delete(a.friendMetadata, uid+":"+target)
	delete(a.friendMetadata, target+":"+uid)
	e1 := a.syncLocked(uid, "friend.removed", map[string]any{"userId": target})
	e2 := a.syncLocked(target, "friend.removed", map[string]any{"userId": uid})
	if err := a.saveLocked(); err != nil {
		return err
	}
	go a.publish([]string{uid}, e1.Type, e1)
	go a.publish([]string{target}, e2.Type, e2)
	return nil
}

func (a *App) UpdateFriendMetadata(uid, target, remark string, tags []string) error {
	remark = strings.TrimSpace(remark)
	if len([]rune(remark)) > 40 || len(tags) > 20 {
		return ErrInvalid
	}
	clean := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len([]rune(tag)) > 24 {
			return ErrInvalid
		}
		if !seen[tag] {
			seen[tag] = true
			clean = append(clean, tag)
		}
	}
	metadata := store.FriendMetadata{Remark: remark, Tags: clean}
	if friends, ok := a.persistence.(store.FriendStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := friends.UpdateFriendMetadata(ctx, uid, target, metadata, time.Now()); err != nil {
			return mapStoreError(err)
		}
		a.publish([]string{uid}, "friend.metadata.updated", map[string]any{"userId": target, "remark": remark, "tags": clean})
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.state.Friends[uid][target] {
		return ErrNotFound
	}
	a.friendMetadata[uid+":"+target] = metadata
	e := a.syncLocked(uid, "friend.metadata.updated", map[string]any{"userId": target, "remark": remark, "tags": clean})
	if err := a.saveLocked(); err != nil {
		return err
	}
	go a.publish([]string{uid}, e.Type, e)
	return nil
}

func (a *App) RunFriendRequestTimeouts(ctx context.Context) {
	friends, ok := a.persistence.(store.FriendStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			items, err := friends.ExpireFriendRequests(ctx, now, 100)
			if err != nil {
				continue
			}
			for _, r := range items {
				a.publish([]string{r.FromUserID, r.ToUserID}, "friend.request.updated", map[string]any{"requestId": r.ID, "status": "expired"})
			}
		}
	}
}
func (a *App) blockedLocked(aID, bID string) bool {
	return a.state.Blocks[aID][bID] || a.state.Blocks[bID][aID]
}

func (a *App) DirectConversation(uid, other string) (*model.Conversation, error) {
	if uid == other {
		return nil, ErrInvalid
	}
	if err := a.refresh(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Users[other] == nil {
		return nil, ErrNotFound
	}
	if a.blockedLocked(uid, other) {
		return nil, ErrForbidden
	}
	key := pair(uid, other)
	if cid := a.state.DirectIndex[key]; cid != "" {
		existing := *a.state.Conversations[cid]
		return &existing, nil
	}
	now := time.Now()
	c := &model.Conversation{ID: id("conv"), Type: "direct", CreatedAt: now, UpdatedAt: now}
	a.state.Conversations[c.ID] = c
	a.state.DirectIndex[key] = c.ID
	a.state.Members[c.ID] = map[string]*model.ConversationMember{uid: {ConversationID: c.ID, UserID: uid, Role: "member", JoinedAt: now}, other: {ConversationID: c.ID, UserID: other, Role: "member", JoinedAt: now}}
	snapshot := *c
	for _, x := range []string{uid, other} {
		a.syncLocked(x, "conversation.created", map[string]any{"conversation": &snapshot})
	}
	if err := a.saveLocked(); err != nil {
		return nil, err
	}
	go a.publish([]string{uid, other}, "conversation.created", &snapshot)
	return &snapshot, nil
}

func (a *App) CreateGroup(owner, name string, members []string) (*model.Conversation, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return nil, ErrInvalid
	}
	if len(members)+1 > a.settingInt("maxGroupMembers", 500) {
		return nil, ErrInvalid
	}
	if groups, ok := a.persistence.(store.GroupStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		c, err := groups.CreateGroupRecord(ctx, id("conv"), owner, name, members, time.Now())
		if err != nil {
			return nil, mapStoreError(err)
		}
		a.publish(append([]string{owner}, members...), "group.created", c)
		return c, nil
	}
	if err := a.refresh(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	c := &model.Conversation{ID: id("conv"), Type: "group", Title: name, CreatedAt: now, UpdatedAt: now}
	a.state.Conversations[c.ID] = c
	a.state.Members[c.ID] = map[string]*model.ConversationMember{owner: {ConversationID: c.ID, UserID: owner, Role: "owner", JoinedAt: now}}
	for _, uid := range members {
		if uid != owner && a.state.Users[uid] != nil {
			a.state.Members[c.ID][uid] = &model.ConversationMember{ConversationID: c.ID, UserID: uid, Role: "member", JoinedAt: now}
		}
	}
	ids := memberIDs(a.state.Members[c.ID])
	for _, uid := range ids {
		a.syncLocked(uid, "group.created", map[string]any{"conversation": c})
	}
	if err := a.saveLocked(); err != nil {
		return nil, err
	}
	snapshot := *c
	go a.publish(ids, "group.created", &snapshot)
	return &snapshot, nil
}
func (a *App) AddGroupMembers(actor, cid string, users []string) error {
	if len(users) == 0 || len(users) > 500 {
		return ErrInvalid
	}
	if groups, ok := a.persistence.(store.GroupStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		if err := groups.AddGroupMembers(ctx, actor, cid, users, time.Now()); err != nil {
			return mapStoreError(err)
		}
		a.publish(users, "group.members.updated", map[string]any{"conversationId": cid})
		return nil
	}
	if err := a.refresh(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.state.Conversations[cid]
	if c == nil || c.Type != "group" {
		return ErrNotFound
	}
	actorM := a.state.Members[cid][actor]
	if actorM == nil || (actorM.Role != "owner" && actorM.Role != "admin") {
		return ErrForbidden
	}
	if len(a.state.Members[cid])+len(users) > a.settingIntLocked("maxGroupMembers", 500) {
		return ErrInvalid
	}
	now := time.Now()
	for _, uid := range users {
		if a.state.Users[uid] != nil && a.state.Members[cid][uid] == nil {
			a.state.Members[cid][uid] = &model.ConversationMember{ConversationID: cid, UserID: uid, Role: "member", JoinedAt: now}
			a.syncLocked(uid, "group.joined", map[string]any{"conversation": c})
		}
	}
	ids := memberIDs(a.state.Members[cid])
	if err := a.saveLocked(); err != nil {
		return err
	}
	go a.publish(ids, "group.members.updated", map[string]any{"conversationId": cid, "memberIds": ids})
	return nil
}
func (a *App) GroupMembers(uid, cid string) ([]*model.ConversationMember, error) {
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := q.ListConversationMembers(ctx, uid, cid)
		if err == store.ErrForbidden {
			return nil, ErrForbidden
		}
		return out, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.state.Members[cid][uid] == nil {
		return nil, ErrForbidden
	}
	out := []*model.ConversationMember{}
	for _, m := range a.state.Members[cid] {
		c := *m
		c.ID = c.UserID
		if u := a.state.Users[m.UserID]; u != nil {
			c.Name, c.Handle, c.AvatarURL = u.Name, u.Handle, u.AvatarURL
		}
		out = append(out, &c)
	}
	return out, nil
}
func (a *App) RemoveGroupMember(actor, cid, target string) error {
	if groups, ok := a.persistence.(store.GroupStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		action := "remove"
		if actor == target {
			action = "leave"
		}
		if err := groups.ApplyGroupMemberAction(ctx, store.GroupMemberAction{ActorID: actor, ConversationID: cid, TargetID: target, Action: action, At: time.Now()}); err != nil {
			return mapStoreError(err)
		}
		a.publish([]string{actor, target}, "group.members.updated", map[string]any{"conversationId": cid})
		return nil
	}
	if err := a.refresh(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	members := a.state.Members[cid]
	am := members[actor]
	tm := members[target]
	if am == nil || tm == nil {
		return ErrNotFound
	}
	if actor != target && (am.Role != "owner" && am.Role != "admin") {
		return ErrForbidden
	}
	if tm.Role == "owner" {
		return ErrForbidden
	}
	delete(members, target)
	a.syncLocked(target, "group.left", map[string]any{"conversationId": cid})
	ids := memberIDs(members)
	if err := a.saveLocked(); err != nil {
		return err
	}
	if ms, ok := a.persistence.(store.MutationStore); ok {
		if err := ms.RemoveMember(context.Background(), cid, target); err != nil {
			return err
		}
	}
	go a.publish(ids, "group.members.updated", map[string]any{"conversationId": cid, "memberIds": ids})
	return nil
}
func (a *App) SetGroupRole(actor, cid, uid, role string) error {
	if role != "member" && role != "admin" {
		return ErrInvalid
	}
	if groups, ok := a.persistence.(store.GroupStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := groups.ApplyGroupMemberAction(ctx, store.GroupMemberAction{ActorID: actor, ConversationID: cid, TargetID: uid, Action: "role", Role: role, At: time.Now()}); err != nil {
			return mapStoreError(err)
		}
		return nil
	}
	if err := a.refresh(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	am := a.state.Members[cid][actor]
	m := a.state.Members[cid][uid]
	if am == nil || am.Role != "owner" || m == nil {
		return ErrForbidden
	}
	if m.Role == "owner" {
		return ErrForbidden
	}
	m.Role = role
	a.syncLocked(uid, "group.role", map[string]any{"conversationId": cid, "role": role})
	return a.saveLocked()
}
func (a *App) MuteMember(actor, cid, uid string, until *time.Time) error {
	if groups, ok := a.persistence.(store.GroupStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := groups.ApplyGroupMemberAction(ctx, store.GroupMemberAction{ActorID: actor, ConversationID: cid, TargetID: uid, Action: "mute", MutedUntil: until, At: time.Now()}); err != nil {
			return mapStoreError(err)
		}
		return nil
	}
	if err := a.refresh(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	am := a.state.Members[cid][actor]
	m := a.state.Members[cid][uid]
	if am == nil || (am.Role != "owner" && am.Role != "admin") || m == nil {
		return ErrForbidden
	}
	if m.Role == "owner" || (am.Role == "admin" && m.Role == "admin") {
		return ErrForbidden
	}
	m.MutedUntil = until
	a.syncLocked(uid, "group.mute", map[string]any{"conversationId": cid, "mutedUntil": until})
	return a.saveLocked()
}

func (a *App) GroupProfile(uid, cid string) (*model.GroupProfile, error) {
	groups, ok := a.persistence.(store.GroupStore)
	if !ok {
		return nil, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	g, err := groups.GetGroupProfile(ctx, uid, cid)
	return g, mapStoreError(err)
}
func (a *App) UpdateGroupProfile(actor, cid string, u store.GroupProfileUpdate) (*model.GroupProfile, error) {
	if u.Name != nil {
		v := strings.TrimSpace(*u.Name)
		if v == "" || len([]rune(v)) > 80 {
			return nil, ErrInvalid
		}
		u.Name = &v
	}
	if u.AvatarMediaID != nil && len(*u.AvatarMediaID) > 200 {
		return nil, ErrInvalid
	}
	if u.JoinPolicy != nil && *u.JoinPolicy != "invite" && *u.JoinPolicy != "qr" && *u.JoinPolicy != "closed" {
		return nil, ErrInvalid
	}
	groups, ok := a.persistence.(store.GroupStore)
	if !ok {
		return nil, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g, err := groups.UpdateGroupProfile(ctx, actor, cid, u, time.Now())
	if err != nil {
		return nil, mapStoreError(err)
	}
	a.publish([]string{actor}, "group.profile.updated", g)
	return g, nil
}
func (a *App) SetGroupAnnouncement(actor, cid, content string) (*model.GroupProfile, error) {
	content = strings.TrimSpace(content)
	if len([]rune(content)) > 5000 {
		return nil, ErrInvalid
	}
	groups, ok := a.persistence.(store.GroupStore)
	if !ok {
		return nil, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g, err := groups.SetGroupAnnouncement(ctx, actor, cid, content, time.Now())
	if err != nil {
		return nil, mapStoreError(err)
	}
	a.publish([]string{actor}, "group.announcement.updated", g)
	return g, nil
}
func (a *App) ReadGroupAnnouncement(uid, cid string) error {
	groups, ok := a.persistence.(store.GroupStore)
	if !ok {
		return ErrNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return mapStoreError(groups.MarkGroupAnnouncementRead(ctx, uid, cid, time.Now()))
}
func (a *App) InviteGroupMember(actor, cid, invitee string) (*model.GroupInvite, bool, error) {
	if invitee == "" || invitee == actor {
		return nil, false, ErrInvalid
	}
	now := time.Now()
	i := &model.GroupInvite{ID: id("ginv"), ConversationID: cid, InviterID: actor, InviteeID: invitee, Source: "invite", Status: "pending", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour), UpdatedAt: now}
	groups, ok := a.persistence.(store.GroupStore)
	if !ok {
		return nil, false, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, dup, err := groups.CreateGroupInvite(ctx, i)
	if err != nil {
		return nil, false, mapStoreError(err)
	}
	if !dup {
		a.publish([]string{invitee}, "group.invite", map[string]any{"invite": created})
	}
	return created, dup, nil
}
func (a *App) GroupInvites(uid, status string, limit int) ([]map[string]any, error) {
	if status != "" && status != "pending" && status != "accepted" && status != "rejected" && status != "cancelled" {
		return nil, ErrInvalid
	}
	queries, ok := a.persistence.(store.QueryStore)
	if !ok {
		return []map[string]any{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return queries.ListGroupInvites(ctx, uid, status, limit)
}
func (a *App) TransitionGroupInvite(uid, inviteID, action string) (*model.GroupInvite, bool, error) {
	if action != "accept" && action != "reject" && action != "cancel" {
		return nil, false, ErrInvalid
	}
	groups, ok := a.persistence.(store.GroupStore)
	if !ok {
		return nil, false, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	i, dup, err := groups.TransitionGroupInvite(ctx, inviteID, uid, action, time.Now())
	if err != nil {
		return i, false, mapStoreError(err)
	}
	if !dup {
		a.publish([]string{i.InviterID, i.InviteeID}, "group.invite.updated", map[string]any{"inviteId": i.ID, "status": i.Status})
	}
	return i, dup, nil
}
func (a *App) JoinGroupByQR(uid, token string) error {
	if token == "" || len(token) > 200 {
		return ErrInvalid
	}
	groups, ok := a.persistence.(store.GroupStore)
	if !ok {
		return ErrNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return mapStoreError(groups.JoinGroupByQR(ctx, uid, token, time.Now()))
}
func (a *App) TransferGroupOwner(actor, cid, target string) error {
	return a.groupMemberAction(store.GroupMemberAction{ActorID: actor, ConversationID: cid, TargetID: target, Action: "transfer", At: time.Now()})
}
func (a *App) SetGroupNickname(uid, cid, nickname string) error {
	nickname = strings.TrimSpace(nickname)
	if len([]rune(nickname)) > 40 {
		return ErrInvalid
	}
	return a.groupMemberAction(store.GroupMemberAction{ActorID: uid, ConversationID: cid, TargetID: uid, Action: "nickname", Nickname: nickname, At: time.Now()})
}
func (a *App) groupMemberAction(action store.GroupMemberAction) error {
	groups, ok := a.persistence.(store.GroupStore)
	if !ok {
		return ErrNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := groups.ApplyGroupMemberAction(ctx, action); err != nil {
		return mapStoreError(err)
	}
	return nil
}

func (a *App) Conversations(uid string) []map[string]any {
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		out, err := q.ListConversations(ctx, uid, 100)
		if err == nil {
			return out
		}
		return []map[string]any{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := []map[string]any{}
	for cid, members := range a.state.Members {
		m := members[uid]
		if m == nil {
			continue
		}
		c := a.state.Conversations[cid]
		if m.HiddenUntilSeq != nil && c.LastMessageSeq <= *m.HiddenUntilSeq {
			continue
		}
		var last *model.Message
		if xs := a.state.Messages[cid]; len(xs) > 0 {
			last = xs[len(xs)-1]
		}
		safeMembers := make([]*model.ConversationMember, 0, len(members))
		for memberID, membership := range members {
			memberCopy := *membership
			memberCopy.ID = memberID
			if user := a.state.Users[memberID]; user != nil {
				memberCopy.Name, memberCopy.Handle, memberCopy.AvatarURL = user.Name, user.Handle, user.AvatarURL
			}
			safeMembers = append(safeMembers, &memberCopy)
		}
		sort.Slice(safeMembers, func(i, j int) bool { return safeMembers[i].UserID < safeMembers[j].UserID })
		conversationCopy := *c
		mentionUnreadCount := int64(0)
		if c.Type == "group" {
			for _, message := range a.state.Messages[c.ID] {
				if message.Seq > m.LastReadSeq && message.SenderID != uid && message.RecalledAt == nil && messageMentionsUser(message, uid) {
					mentionUnreadCount++
				}
			}
		}
		out = append(out, map[string]any{"conversation": &conversationCopy, "membership": m, "lastMessage": last, "unreadCount": max(int64(0), c.LastMessageSeq-m.LastReadSeq), "mentionUnreadCount": mentionUnreadCount, "members": safeMembers})
	}
	sort.Slice(out, func(i, j int) bool {
		left := out[i]["membership"].(*model.ConversationMember)
		right := out[j]["membership"].(*model.ConversationMember)
		if left.Pinned != right.Pinned {
			return left.Pinned
		}
		return out[i]["conversation"].(*model.Conversation).UpdatedAt.After(out[j]["conversation"].(*model.Conversation).UpdatedAt)
	})
	return out
}

func messageMentionsUser(message *model.Message, uid string) bool {
	if message == nil || message.Type != "text" || uid == "" {
		return false
	}
	if mentionAll, _ := message.Body["mentionAll"].(bool); mentionAll {
		return true
	}
	switch mentions := message.Body["mentions"].(type) {
	case []string:
		for _, mentionedID := range mentions {
			if mentionedID == uid {
				return true
			}
		}
	case []any:
		for _, value := range mentions {
			if mentionedID, _ := value.(string); mentionedID == uid {
				return true
			}
		}
	}
	return false
}

func (a *App) SendMessage(uid, cid, clientID, typ string, body map[string]any, reply string) (*model.Message, bool, error) {
	return a.sendMessage(uid, cid, clientID, typ, body, reply, 0, false)
}

func validateScheduledMessage(clientID, typ string, body map[string]any, expiresInSeconds int64, scheduledAt time.Time, now time.Time) error {
	if strings.TrimSpace(clientID) == "" || len(clientID) > 100 || scheduledAt.Before(now.Add(5*time.Second)) || scheduledAt.After(now.Add(365*24*time.Hour)) {
		return ErrInvalid
	}
	if typ == "" {
		typ = "text"
	}
	if !map[string]bool{"text": true, "image": true, "audio": true, "video": true, "file": true, "location": true, "contact": true}[typ] || body == nil {
		return ErrInvalid
	}
	if typ == "text" {
		text, _ := body["text"].(string)
		if strings.TrimSpace(text) == "" || len([]rune(text)) > 5000 {
			return ErrInvalid
		}
	}
	if expiresInSeconds != 0 && (expiresInSeconds < 60 || expiresInSeconds > 30*24*60*60) {
		return ErrInvalid
	}
	return nil
}

func (a *App) CreateScheduledMessage(uid, cid, clientID, typ string, body map[string]any, reply string, expiresInSeconds int64, scheduledAt time.Time) (*model.ScheduledMessage, bool, error) {
	now := time.Now()
	if typ == "" {
		typ = "text"
	}
	if err := validateScheduledMessage(clientID, typ, body, expiresInSeconds, scheduledAt, now); err != nil {
		return nil, false, err
	}
	s, ok := a.persistence.(store.ScheduledMessageStore)
	if !ok {
		return nil, false, ErrInvalid
	}
	item := &model.ScheduledMessage{ID: id("sch"), UserID: uid, ConversationID: cid, ClientMsgID: strings.TrimSpace(clientID), Type: typ, Body: body, ReplyToID: reply, ExpiresInSeconds: expiresInSeconds, ScheduledAt: scheduledAt, Status: "pending", CreatedAt: now, UpdatedAt: now}
	created, duplicate, err := s.CreateScheduledMessage(context.Background(), item)
	if err == store.ErrForbidden {
		return nil, false, ErrForbidden
	}
	if err == store.ErrConflict {
		return nil, false, ErrConflict
	}
	return created, duplicate, err
}

func (a *App) ListScheduledMessages(uid, status string, limit int) ([]*model.ScheduledMessage, error) {
	if status != "" && !map[string]bool{"pending": true, "processing": true, "sent": true, "cancelled": true, "failed": true}[status] {
		return nil, ErrInvalid
	}
	s, ok := a.persistence.(store.ScheduledMessageStore)
	if !ok {
		return nil, ErrInvalid
	}
	return s.ListScheduledMessages(context.Background(), uid, status, limit)
}

func (a *App) UpdateScheduledMessage(uid, scheduledID string, update store.ScheduledMessageUpdate) (*model.ScheduledMessage, error) {
	now := time.Now()
	if update.ScheduledAt != nil && (update.ScheduledAt.Before(now.Add(5*time.Second)) || update.ScheduledAt.After(now.Add(365*24*time.Hour))) {
		return nil, ErrInvalid
	}
	if update.ExpiresInSeconds != nil && *update.ExpiresInSeconds != 0 && (*update.ExpiresInSeconds < 60 || *update.ExpiresInSeconds > 30*24*60*60) {
		return nil, ErrInvalid
	}
	if update.Type != nil && !map[string]bool{"text": true, "image": true, "audio": true, "video": true, "file": true, "location": true, "contact": true}[*update.Type] {
		return nil, ErrInvalid
	}
	if update.BodySet && update.Body == nil {
		return nil, ErrInvalid
	}
	s, ok := a.persistence.(store.ScheduledMessageStore)
	if !ok {
		return nil, ErrInvalid
	}
	item, err := s.UpdateScheduledMessage(context.Background(), uid, scheduledID, update, now)
	return item, mapStoreError(err)
}

func (a *App) CancelScheduledMessage(uid, scheduledID string) (*model.ScheduledMessage, error) {
	s, ok := a.persistence.(store.ScheduledMessageStore)
	if !ok {
		return nil, ErrInvalid
	}
	item, err := s.CancelScheduledMessage(context.Background(), uid, scheduledID, time.Now())
	return item, mapStoreError(err)
}

func (a *App) RunScheduledMessages(ctx context.Context) {
	s, ok := a.persistence.(store.ScheduledMessageStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			items, err := s.LeaseScheduledMessages(ctx, now, 2*time.Minute, 25)
			if err != nil {
				continue
			}
			for _, item := range items {
				message, _, sendErr := a.SendMessageWithExpiry(item.UserID, item.ConversationID, item.ClientMsgID, item.Type, item.Body, item.ReplyToID, item.ExpiresInSeconds)
				messageID := ""
				if message != nil {
					messageID = message.ID
				}
				_ = s.CompleteScheduledMessage(ctx, item.ID, messageID, sendErr, time.Now())
			}
		}
	}
}

func (a *App) RunMessageExpirations(ctx context.Context) {
	s, ok := a.persistence.(store.MessageExpiryStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for {
				items, err := s.ExpireMessages(ctx, now, 100)
				if err != nil || len(items) < 100 {
					break
				}
			}
		}
	}
}

func (a *App) SendMessageWithExpiry(uid, cid, clientID, typ string, body map[string]any, reply string, expiresInSeconds int64) (*model.Message, bool, error) {
	return a.sendMessage(uid, cid, clientID, typ, body, reply, expiresInSeconds, false)
}

func (a *App) sendMessage(uid, cid, clientID, typ string, body map[string]any, reply string, expiresInSeconds int64, generated bool) (*model.Message, bool, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || len(clientID) > 100 {
		return nil, false, ErrInvalid
	}
	if expiresInSeconds != 0 && (expiresInSeconds < 60 || expiresInSeconds > 30*24*60*60) {
		return nil, false, ErrInvalid
	}
	if typ == "" {
		typ = "text"
	}
	// System events are generated only by trusted server workflows. Allowing a
	// user transport to submit them would let any member impersonate moderation
	// and membership notices.
	allowed := map[string]bool{"text": true, "image": true, "audio": true, "video": true, "file": true, "location": true, "contact": true}
	if generated {
		allowed["chat_history"] = true
	}
	if !allowed[typ] || body == nil {
		return nil, false, ErrInvalid
	}
	var mentions []string
	var mentionAll bool
	if typ == "text" {
		text, _ := body["text"].(string)
		if strings.TrimSpace(text) == "" || len([]rune(text)) > a.settingInt("maxMessageTextLength", 5000) {
			return nil, false, ErrInvalid
		}
		if !generated {
			var normalizeErr error
			body, mentions, mentionAll, normalizeErr = normalizeTextBody(body)
			if normalizeErr != nil {
				return nil, false, normalizeErr
			}
		}
		if a.settingBool("sensitiveWordEnabled", true) {
			lower := strings.ToLower(text)
			a.mu.RLock()
			for _, entry := range a.state.SensitiveWords {
				word := strings.SplitN(entry, "|", 2)[0]
				if word != "" && strings.Contains(lower, strings.ToLower(word)) {
					a.mu.RUnlock()
					return nil, false, ErrForbidden
				}
			}
			a.mu.RUnlock()
		}
	} else if !generated && typ == "contact" {
		if !onlyBodyKeys(body, "userId", "name", "handle", "avatarUrl") {
			return nil, false, ErrInvalid
		}
		contactID, ok := body["userId"].(string)
		contactID = strings.TrimSpace(contactID)
		if !ok || contactID == "" {
			return nil, false, ErrInvalid
		}
		contact, err := a.User(contactID)
		if err != nil || contact.Banned {
			return nil, false, ErrNotFound
		}
		canonical := map[string]any{"userId": contact.ID, "name": contact.Name, "handle": contact.Handle, "avatarUrl": contact.AvatarURL}
		for _, key := range []string{"name", "handle", "avatarUrl"} {
			if supplied, exists := body[key]; exists {
				text, valid := supplied.(string)
				if !valid || text != canonical[key] {
					return nil, false, ErrInvalid
				}
			}
		}
		body = canonical
	} else if !generated && typ == "location" {
		if !onlyBodyKeys(body, "latitude", "longitude", "name", "address") {
			return nil, false, ErrInvalid
		}
		latitude, latOK := numericBodyValue(body["latitude"])
		longitude, lngOK := numericBodyValue(body["longitude"])
		name, nameOK := body["name"].(string)
		address, addressOK := body["address"].(string)
		name, address = strings.TrimSpace(name), strings.TrimSpace(address)
		if !latOK || !lngOK || !nameOK || !addressOK || math.IsNaN(latitude) || math.IsNaN(longitude) || math.IsInf(latitude, 0) || math.IsInf(longitude, 0) || latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 || name == "" || len([]rune(name)) > 80 || len([]rune(address)) > 240 {
			return nil, false, ErrInvalid
		}
		body = map[string]any{"latitude": latitude, "longitude": longitude, "name": name, "address": address}
	} else if !generated && (typ == "image" || typ == "audio" || typ == "video" || typ == "file") {
		if !onlyBodyKeys(body, "type", "mediaId", "mime", "fileName", "size", "checksum", "duration") {
			return nil, false, ErrInvalid
		}
		mediaID, _ := body["mediaId"].(string)
		if mediaID == "" {
			return nil, false, ErrInvalid
		}
		m, err := a.GetMedia(mediaID)
		if err != nil || m.Status != "ready" || m.OwnerID != uid {
			return nil, false, ErrForbidden
		}
		mime := strings.ToLower(strings.TrimSpace(m.MIME))
		if (typ == "image" && !strings.HasPrefix(mime, "image/")) ||
			(typ == "audio" && !strings.HasPrefix(mime, "audio/")) ||
			(typ == "video" && !strings.HasPrefix(mime, "video/")) {
			return nil, false, ErrInvalid
		}
		fileName, _ := body["fileName"].(string)
		fileName = strings.TrimSpace(fileName)
		if len([]rune(fileName)) > 255 {
			return nil, false, ErrInvalid
		}
		canonical := map[string]any{"mediaId": m.ID, "mime": m.MIME, "size": m.Size, "checksum": m.Checksum}
		if fileName != "" {
			canonical["fileName"] = fileName
		}
		if duration, exists := body["duration"]; exists {
			seconds, ok := numericBodyValue(duration)
			if !ok || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > 86400 {
				return nil, false, ErrInvalid
			}
			canonical["duration"] = int64(seconds)
		}
		body = canonical
	}
	now := time.Now()
	var expiresAt *time.Time
	if expiresInSeconds > 0 {
		value := now.Add(time.Duration(expiresInSeconds) * time.Second)
		expiresAt = &value
	}
	if db, ok := a.persistence.(store.MessageStore); ok {
		m, duplicate, events, err := db.SendMessage(context.Background(), store.MessageInput{UserID: uid, ConversationID: cid, ClientMsgID: clientID, Type: typ, Body: body, Mentions: mentions, MentionAll: mentionAll, ReplyToID: reply, MessageID: id("msg"), CreatedAt: now.UnixMilli(), ExpiresAt: expiresAt})
		if err != nil {
			switch err {
			case store.ErrForbidden:
				return nil, false, ErrForbidden
			case store.ErrNotFound:
				return nil, false, ErrNotFound
			case store.ErrConflict:
				return nil, false, ErrConflict
			}
			return nil, false, err
		}
		if !duplicate {
			a.mu.Lock()
			if c := a.state.Conversations[cid]; c != nil {
				c.Seq = m.Seq
				c.LastMessageSeq = m.Seq
				c.UpdatedAt = m.CreatedAt
			}
			a.state.Messages[cid] = append(a.state.Messages[cid], m)
			a.state.MessageByID[m.ID] = m
			a.state.MessageIdempotency[uid+":"+clientID] = m.ID
			for _, e := range events {
				a.state.UserSyncSeq[e.UserID] = e.Seq
				a.state.SyncEvents[e.UserID] = append(a.state.SyncEvents[e.UserID], e)
			}
			a.mu.Unlock()
			a.Metrics.Messages.Add(1)
			if _, distributed := a.persistence.(store.EventSubscriber); !distributed {
				for _, e := range events {
					go a.publish([]string{e.UserID}, e.Type, e.Payload)
				}
			}
		}
		return m, duplicate, nil
	}
	a.mu.Lock()
	memb := a.state.Members[cid][uid]
	if memb == nil {
		a.mu.Unlock()
		return nil, false, ErrForbidden
	}
	u := a.state.Users[uid]
	if u == nil || u.Banned {
		a.mu.Unlock()
		return nil, false, ErrForbidden
	}
	if memb.MutedUntil != nil && memb.MutedUntil.After(time.Now()) {
		a.mu.Unlock()
		return nil, false, ErrForbidden
	}
	key := uid + ":" + clientID
	if mid := a.state.MessageIdempotency[key]; mid != "" {
		m := a.state.MessageByID[mid]
		a.mu.Unlock()
		return m, true, nil
	}
	c := a.state.Conversations[cid]
	if c == nil {
		a.mu.Unlock()
		return nil, false, ErrNotFound
	}
	if c.Type == "direct" {
		for other := range a.state.Members[cid] {
			if other != uid && a.blockedLocked(uid, other) {
				a.mu.Unlock()
				return nil, false, ErrForbidden
			}
		}
	}
	if len(mentions) > 0 || mentionAll {
		if typ != "text" || c.Type != "group" || (mentionAll && memb.Role != "owner" && memb.Role != "admin") {
			a.mu.Unlock()
			return nil, false, ErrForbidden
		}
		for _, mentionedID := range mentions {
			if a.state.Members[cid][mentionedID] == nil {
				a.mu.Unlock()
				return nil, false, ErrForbidden
			}
		}
	}
	c.Seq++
	c.LastMessageSeq = c.Seq
	c.UpdatedAt = time.Now()
	m := &model.Message{ID: id("msg"), ClientMsgID: clientID, ConversationID: cid, SenderID: uid, Seq: c.Seq, Type: typ, Body: body, ReplyToID: reply, ExpiresAt: expiresAt, CreatedAt: now}
	a.state.Messages[cid] = append(a.state.Messages[cid], m)
	a.state.MessageByID[m.ID] = m
	a.state.MessageIdempotency[key] = m.ID
	ids := memberIDs(a.state.Members[cid])
	events := make([]*model.SyncEvent, 0, len(ids))
	for _, recipient := range ids {
		events = append(events, a.syncLocked(recipient, "message.created", map[string]any{"message": m}))
	}
	err := a.saveLocked()
	a.mu.Unlock()
	if err != nil {
		return nil, false, err
	}
	a.Metrics.Messages.Add(1)
	for i, recipient := range ids {
		e := events[i]
		go a.publish([]string{recipient}, e.Type, e.Payload)
	}
	return m, false, nil
}

func onlyBodyKeys(body map[string]any, allowed ...string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key := range body {
		if _, ok := set[key]; !ok {
			return false
		}
	}
	return true
}

func normalizeTextBody(body map[string]any) (map[string]any, []string, bool, error) {
	if !onlyBodyKeys(body, "text", "mentions", "mentionAll") {
		return nil, nil, false, ErrInvalid
	}
	text, ok := body["text"].(string)
	if !ok {
		return nil, nil, false, ErrInvalid
	}
	mentionAll := false
	if raw, exists := body["mentionAll"]; exists {
		var valid bool
		mentionAll, valid = raw.(bool)
		if !valid {
			return nil, nil, false, ErrInvalid
		}
	}
	mentions := []string{}
	seen := map[string]struct{}{}
	if raw, exists := body["mentions"]; exists {
		values := []any{}
		switch typed := raw.(type) {
		case []any:
			values = typed
		case []string:
			for _, value := range typed {
				values = append(values, value)
			}
		default:
			return nil, nil, false, ErrInvalid
		}
		if len(values) > 50 {
			return nil, nil, false, ErrInvalid
		}
		for _, rawID := range values {
			userID, valid := rawID.(string)
			userID = strings.TrimSpace(userID)
			if !valid || userID == "" || len(userID) > 100 {
				return nil, nil, false, ErrInvalid
			}
			if _, duplicate := seen[userID]; duplicate {
				return nil, nil, false, ErrInvalid
			}
			seen[userID] = struct{}{}
			mentions = append(mentions, userID)
		}
	}
	canonical := map[string]any{"text": strings.TrimSpace(text)}
	if len(mentions) > 0 {
		canonical["mentions"] = mentions
	}
	if mentionAll {
		canonical["mentionAll"] = true
	}
	return canonical, mentions, mentionAll, nil
}

func numericBodyValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

func (a *App) ForwardMessages(uid, targetID string, sourceIDs []string, mode, clientBatchID string) ([]*model.Message, bool, error) {
	clientBatchID = strings.TrimSpace(clientBatchID)
	if len(sourceIDs) == 0 || len(sourceIDs) > 100 || (mode != "separate" && mode != "merged") || clientBatchID == "" || len(clientBatchID) > 80 {
		return nil, false, ErrInvalid
	}
	if !a.CanAccess(uid, targetID) {
		return nil, false, ErrForbidden
	}
	seen := make(map[string]bool, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		if strings.TrimSpace(sourceID) == "" || seen[sourceID] {
			return nil, false, ErrInvalid
		}
		seen[sourceID] = true
	}
	var sources []*model.Message
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		items, err := q.ListForwardMessages(ctx, uid, sourceIDs)
		if err == store.ErrForbidden {
			return nil, false, ErrForbidden
		}
		if err == store.ErrConflict {
			return nil, false, ErrConflict
		}
		if err != nil {
			return nil, false, err
		}
		sources = items
	} else {
		a.mu.RLock()
		for _, sourceID := range sourceIDs {
			message := a.state.MessageByID[sourceID]
			if message == nil || a.state.Members[message.ConversationID][uid] == nil {
				a.mu.RUnlock()
				return nil, false, ErrForbidden
			}
			copy := *message
			sources = append(sources, &copy)
		}
		a.mu.RUnlock()
	}
	totalSize := 0
	for _, source := range sources {
		raw, _ := json.Marshal(source.Body)
		totalSize += len(raw)
	}
	if totalSize > 1024*1024 {
		return nil, false, ErrInvalid
	}
	results := make([]*model.Message, 0, len(sources))
	allDuplicate := true
	if mode == "merged" {
		entries := make([]map[string]any, 0, len(sources))
		for _, source := range sources {
			entries = append(entries, map[string]any{"sourceMessageId": source.ID, "senderId": source.SenderID, "createdAt": source.CreatedAt, "type": source.Type, "summary": forwardSummary(source)})
		}
		message, duplicate, err := a.sendMessage(uid, targetID, "forward:"+clientBatchID, "chat_history", map[string]any{"forwarded": true, "mode": "merged", "entries": entries}, "", 0, true)
		if err != nil {
			return nil, false, err
		}
		return []*model.Message{message}, duplicate, nil
	}
	for index, source := range sources {
		raw, _ := json.Marshal(source.Body)
		body := map[string]any{}
		_ = json.Unmarshal(raw, &body)
		body["forwarded"] = true
		body["sourceMessageId"] = source.ID
		message, duplicate, err := a.sendMessage(uid, targetID, fmt.Sprintf("forward:%s:%d", clientBatchID, index), source.Type, body, "", 0, true)
		if err != nil {
			return nil, false, err
		}
		allDuplicate = allDuplicate && duplicate
		results = append(results, message)
	}
	return results, allDuplicate, nil
}

func forwardSummary(message *model.Message) string {
	if text, ok := message.Body["text"].(string); ok {
		runes := []rune(strings.TrimSpace(text))
		if len(runes) > 120 {
			runes = runes[:120]
		}
		return string(runes)
	}
	labels := map[string]string{"image": "[图片]", "audio": "[语音]", "video": "[视频]", "file": "[文件]", "location": "[位置]", "contact": "[联系人]"}
	if label := labels[message.Type]; label != "" {
		return label
	}
	return "[消息]"
}

func (a *App) History(uid, cid string, before int64, limit int) ([]*model.Message, error) {
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		out, err := q.ListMessages(ctx, uid, cid, before, limit)
		if err == store.ErrForbidden {
			return nil, ErrForbidden
		}
		return out, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.state.Members[cid][uid] == nil {
		return nil, ErrForbidden
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	xs := a.state.Messages[cid]
	out := make([]*model.Message, 0, limit)
	for i := len(xs) - 1; i >= 0 && len(out) < limit; i-- {
		if before == 0 || xs[i].Seq < before {
			out = append(out, a.messageWithMemoryReactionsLocked(uid, xs[i]))
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
func (a *App) Recall(uid, mid string) error {
	window := time.Duration(a.settingInt("messageRecallMinutes", 2)) * time.Minute
	if s, ok := a.persistence.(store.RuntimeMutationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, _, err := s.RecallAuthorized(ctx, uid, mid, time.Now(), window)
		if err == store.ErrNotFound {
			return ErrNotFound
		}
		if err == store.ErrForbidden {
			return ErrForbidden
		}
		return err
	}
	if err := a.refresh(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.state.MessageByID[mid]
	if m == nil {
		return ErrNotFound
	}
	membership := a.state.Members[m.ConversationID][uid]
	if m.SenderID != uid && (membership == nil || (membership.Role != "owner" && membership.Role != "admin")) {
		return ErrForbidden
	}
	if m.SenderID == uid && time.Since(m.CreatedAt) > window {
		return ErrForbidden
	}
	if m.RecalledAt != nil {
		return nil
	}
	now := time.Now()
	m.RecalledAt = &now
	m.Body = map[string]any{}
	if ms, ok := a.persistence.(store.MutationStore); ok {
		if err := ms.RecallMessage(context.Background(), mid, now); err != nil {
			if err == store.ErrNotFound {
				return ErrNotFound
			}
			return err
		}
	}
	ids := memberIDs(a.state.Members[m.ConversationID])
	events := make([]*model.SyncEvent, 0, len(ids))
	for _, x := range ids {
		events = append(events, a.syncLocked(x, "message.recalled", map[string]any{"messageId": mid, "conversationId": m.ConversationID, "conversationSeq": m.Seq}))
	}
	if err := a.saveLocked(); err != nil {
		return err
	}
	for i, x := range ids {
		e := events[i]
		go a.publish([]string{x}, e.Type, e)
	}
	return nil
}

func (a *App) EditMessage(uid, mid, editID string, body map[string]any) (*model.Message, bool, error) {
	editID = strings.TrimSpace(editID)
	if editID == "" {
		editID = id("edit")
	}
	if !callIDPattern.MatchString(editID) {
		return nil, false, ErrInvalid
	}
	text, _ := body["text"].(string)
	if strings.TrimSpace(text) == "" || len([]rune(text)) > a.settingInt("maxMessageTextLength", 5000) {
		return nil, false, ErrInvalid
	}
	canonical, _, _, err := normalizeTextBody(body)
	if err != nil {
		return nil, false, err
	}
	window := time.Duration(a.settingInt("messageRecallMinutes", 2)) * time.Minute
	sensitiveEnabled := a.settingBool("sensitiveWordEnabled", true)
	if s, ok := a.persistence.(store.MessageCollaborationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		message, duplicate, err := s.EditMessage(ctx, uid, mid, editID, canonical, time.Now(), window)
		if err == store.ErrNotFound {
			return nil, false, ErrNotFound
		}
		if err == store.ErrForbidden {
			return nil, false, ErrForbidden
		}
		if err == store.ErrConflict {
			return nil, false, ErrConflict
		}
		if err != nil {
			return nil, false, err
		}
		return message, duplicate, nil
	}
	if err = a.refresh(); err != nil {
		return nil, false, err
	}
	a.mu.Lock()
	message := a.state.MessageByID[mid]
	if message == nil {
		a.mu.Unlock()
		return nil, false, ErrNotFound
	}
	membership := a.state.Members[message.ConversationID][uid]
	if membership == nil || message.SenderID != uid || message.Type != "text" || message.RecalledAt != nil || time.Since(message.CreatedAt) > window {
		a.mu.Unlock()
		return nil, false, ErrForbidden
	}
	if a.state.MessageEditRequests[mid] == nil {
		a.state.MessageEditRequests[mid] = map[string]string{}
	}
	currentRaw, _ := json.Marshal(message.Body)
	newRaw, _ := json.Marshal(canonical)
	if previousBody, duplicate := a.state.MessageEditRequests[mid][editID]; duplicate {
		if previousBody != string(newRaw) {
			a.mu.Unlock()
			return nil, false, ErrConflict
		}
		copy := *message
		a.mu.Unlock()
		return &copy, true, nil
	}
	if err = a.validateMemoryMentionsLocked(uid, message.ConversationID, canonical); err != nil {
		a.mu.Unlock()
		return nil, false, err
	}
	if sensitiveEnabled {
		for _, entry := range a.state.SensitiveWords {
			word := strings.SplitN(entry, "|", 2)[0]
			if word != "" && strings.Contains(strings.ToLower(text), strings.ToLower(word)) {
				a.mu.Unlock()
				return nil, false, ErrForbidden
			}
		}
	}
	a.state.MessageEditRequests[mid][editID] = string(newRaw)
	if string(currentRaw) == string(newRaw) {
		_ = a.saveLocked()
		copy := *message
		a.mu.Unlock()
		return &copy, true, nil
	}
	if message.EditVersion == 0 {
		original := map[string]any{}
		_ = json.Unmarshal(currentRaw, &original)
		a.state.MessageEdits[mid] = append(a.state.MessageEdits[mid], &model.MessageEdit{MessageID: mid, Version: 0, EditorID: uid, Body: original, EditedAt: message.CreatedAt})
	}
	now := time.Now()
	message.EditVersion++
	message.EditedAt = &now
	message.Body = canonical
	a.state.MessageEdits[mid] = append(a.state.MessageEdits[mid], &model.MessageEdit{MessageID: mid, Version: message.EditVersion, EditID: editID, EditorID: uid, Body: canonical, EditedAt: now})
	ids := memberIDs(a.state.Members[message.ConversationID])
	events := make([]*model.SyncEvent, 0, len(ids))
	payload := map[string]any{"message": message, "editId": editID}
	for _, memberID := range ids {
		events = append(events, a.syncLocked(memberID, "message.edited", payload))
	}
	a.auditLocked(uid, "message.edited", "message", mid, map[string]any{"editId": editID, "version": message.EditVersion})
	if err = a.saveLocked(); err != nil {
		a.mu.Unlock()
		return nil, false, err
	}
	copy := *message
	a.mu.Unlock()
	for i, memberID := range ids {
		event := events[i]
		go a.publish([]string{memberID}, event.Type, event.Payload)
	}
	return &copy, false, nil
}

func (a *App) validateMemoryMentionsLocked(uid, cid string, body map[string]any) error {
	mentions, _ := body["mentions"].([]string)
	mentionAll, _ := body["mentionAll"].(bool)
	if len(mentions) == 0 && !mentionAll {
		return nil
	}
	conversation := a.state.Conversations[cid]
	membership := a.state.Members[cid][uid]
	if conversation == nil || conversation.Type != "group" || membership == nil || (mentionAll && membership.Role != "owner" && membership.Role != "admin") {
		return ErrForbidden
	}
	for _, mentionedID := range mentions {
		if a.state.Members[cid][mentionedID] == nil {
			return ErrForbidden
		}
	}
	return nil
}

func (a *App) MessageEdits(uid, mid string) ([]*model.MessageEdit, error) {
	if s, ok := a.persistence.(store.MessageCollaborationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		items, err := s.ListMessageEdits(ctx, uid, mid)
		if err == store.ErrNotFound {
			return nil, ErrNotFound
		}
		if err == store.ErrForbidden {
			return nil, ErrForbidden
		}
		return items, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	message := a.state.MessageByID[mid]
	if message == nil {
		return nil, ErrNotFound
	}
	if a.state.Members[message.ConversationID][uid] == nil || message.RecalledAt != nil {
		return nil, ErrForbidden
	}
	return append([]*model.MessageEdit(nil), a.state.MessageEdits[mid]...), nil
}

func (a *App) SetMessageReaction(uid, mid, emoji string, add bool) (model.MessageReactionSummary, bool, error) {
	if !allowedMessageReactions[emoji] {
		return model.MessageReactionSummary{}, false, ErrInvalid
	}
	if s, ok := a.persistence.(store.MessageCollaborationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		summary, duplicate, err := s.SetMessageReaction(ctx, uid, mid, emoji, add, time.Now())
		if err == store.ErrNotFound {
			return summary, duplicate, ErrNotFound
		}
		if err == store.ErrForbidden {
			return summary, duplicate, ErrForbidden
		}
		return summary, duplicate, err
	}
	a.mu.Lock()
	message := a.state.MessageByID[mid]
	if message == nil {
		a.mu.Unlock()
		return model.MessageReactionSummary{}, false, ErrNotFound
	}
	if a.state.Members[message.ConversationID][uid] == nil || message.RecalledAt != nil {
		a.mu.Unlock()
		return model.MessageReactionSummary{}, false, ErrForbidden
	}
	if a.state.MessageReactions[mid] == nil {
		a.state.MessageReactions[mid] = map[string]map[string]bool{}
	}
	if a.state.MessageReactions[mid][emoji] == nil {
		a.state.MessageReactions[mid][emoji] = map[string]bool{}
	}
	wasSet := a.state.MessageReactions[mid][emoji][uid]
	changed := wasSet != add
	if add {
		a.state.MessageReactions[mid][emoji][uid] = true
	} else {
		delete(a.state.MessageReactions[mid][emoji], uid)
	}
	summary := model.MessageReactionSummary{Emoji: emoji, Count: len(a.state.MessageReactions[mid][emoji]), ReactedByMe: add}
	ids := memberIDs(a.state.Members[message.ConversationID])
	events := []*model.SyncEvent{}
	if changed {
		payload := map[string]any{"messageId": mid, "conversationId": message.ConversationID, "emoji": emoji, "actorId": uid, "added": add, "count": summary.Count}
		for _, memberID := range ids {
			events = append(events, a.syncLocked(memberID, "message.reaction.updated", payload))
		}
	}
	if err := a.saveLocked(); err != nil {
		a.mu.Unlock()
		return summary, false, err
	}
	a.mu.Unlock()
	for i, event := range events {
		go a.publish([]string{ids[i]}, event.Type, event.Payload)
	}
	return summary, !changed, nil
}

func (a *App) CollaborationMessage(uid, mid string) (*model.Message, error) {
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		items, err := q.ListForwardMessages(ctx, uid, []string{mid})
		if err == store.ErrForbidden {
			return nil, ErrForbidden
		}
		if err != nil {
			return nil, err
		}
		if len(items) != 1 {
			return nil, ErrNotFound
		}
		return items[0], nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	message := a.state.MessageByID[mid]
	if message == nil {
		return nil, ErrNotFound
	}
	if a.state.Members[message.ConversationID][uid] == nil {
		return nil, ErrForbidden
	}
	return a.messageWithMemoryReactionsLocked(uid, message), nil
}

func (a *App) SetGroupMessagePin(uid, cid, mid string, pin bool) (*model.MessagePin, bool, error) {
	if s, ok := a.persistence.(store.MessageCollaborationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		item, duplicate, err := s.SetGroupMessagePin(ctx, uid, cid, mid, pin, time.Now())
		if err == store.ErrNotFound {
			return nil, false, ErrNotFound
		}
		if err == store.ErrForbidden {
			return nil, false, ErrForbidden
		}
		return item, duplicate, err
	}
	a.mu.Lock()
	conversation, membership, message := a.state.Conversations[cid], a.state.Members[cid][uid], a.state.MessageByID[mid]
	if conversation == nil || message == nil || message.ConversationID != cid || message.RecalledAt != nil {
		a.mu.Unlock()
		return nil, false, ErrNotFound
	}
	if conversation.Type != "group" || membership == nil || (membership.Role != "owner" && membership.Role != "admin") {
		a.mu.Unlock()
		return nil, false, ErrForbidden
	}
	if a.state.GroupMessagePins[cid] == nil {
		a.state.GroupMessagePins[cid] = map[string]*model.MessagePin{}
	}
	_, exists := a.state.GroupMessagePins[cid][mid]
	changed := exists != pin
	now := time.Now()
	item := &model.MessagePin{ConversationID: cid, Message: message, PinnedBy: uid, PinnedAt: now}
	if pin {
		a.state.GroupMessagePins[cid][mid] = item
	} else {
		delete(a.state.GroupMessagePins[cid], mid)
	}
	ids := memberIDs(a.state.Members[cid])
	events := []*model.SyncEvent{}
	if changed {
		eventType := "group.message.pinned"
		if !pin {
			eventType = "group.message.unpinned"
		}
		payload := map[string]any{"conversationId": cid, "messageId": mid, "actorId": uid}
		for _, memberID := range ids {
			events = append(events, a.syncLocked(memberID, eventType, payload))
		}
		conversation.Seq++
		conversation.LastMessageSeq = conversation.Seq
		conversation.UpdatedAt = now
		system := &model.Message{ID: id("msg"), ConversationID: cid, SenderID: uid, ClientMsgID: id("system"), Seq: conversation.Seq, Type: "system", Body: map[string]any{"event": eventType, "messageId": mid, "actorId": uid}, CreatedAt: now}
		a.state.Messages[cid] = append(a.state.Messages[cid], system)
		a.state.MessageByID[system.ID] = system
		for _, memberID := range ids {
			events = append(events, a.syncLocked(memberID, "message.created", map[string]any{"message": system}))
		}
		a.auditLocked(uid, eventType, "message", mid, map[string]any{"conversationId": cid})
	}
	if err := a.saveLocked(); err != nil {
		a.mu.Unlock()
		return nil, false, err
	}
	a.mu.Unlock()
	for _, event := range events {
		go a.publish([]string{event.UserID}, event.Type, event.Payload)
	}
	return item, !changed, nil
}

func (a *App) GroupMessagePins(uid, cid string, before int64, limit int) ([]*model.MessagePin, error) {
	if s, ok := a.persistence.(store.MessageCollaborationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		items, err := s.ListGroupMessagePins(ctx, uid, cid, before, limit)
		if err == store.ErrForbidden {
			return nil, ErrForbidden
		}
		return items, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.state.Members[cid][uid] == nil {
		return nil, ErrForbidden
	}
	items := []*model.MessagePin{}
	for _, item := range a.state.GroupMessagePins[cid] {
		if before == 0 || item.PinnedAt.UnixMilli() < before {
			copy := *item
			copy.Message = a.messageWithMemoryReactionsLocked(uid, item.Message)
			items = append(items, &copy)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].PinnedAt.After(items[j].PinnedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (a *App) SearchConversationMessages(uid, cid, query string, before int64, limit int) ([]*model.Message, error) {
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 100 {
		return nil, ErrInvalid
	}
	if s, ok := a.persistence.(store.MessageCollaborationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		items, err := s.SearchConversationMessages(ctx, uid, cid, query, before, limit)
		if err == store.ErrForbidden {
			return nil, ErrForbidden
		}
		return items, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.state.Members[cid][uid] == nil {
		return nil, ErrForbidden
	}
	lower := strings.ToLower(query)
	items := []*model.Message{}
	for i := len(a.state.Messages[cid]) - 1; i >= 0 && len(items) < limit; i-- {
		message := a.state.Messages[cid][i]
		text, _ := message.Body["text"].(string)
		if message.Type == "text" && message.RecalledAt == nil && (before == 0 || message.Seq < before) && strings.Contains(strings.ToLower(text), lower) {
			items = append(items, a.messageWithMemoryReactionsLocked(uid, message))
		}
	}
	return items, nil
}

func (a *App) messageWithMemoryReactionsLocked(uid string, message *model.Message) *model.Message {
	copy := *message
	copy.Reactions = nil
	for emoji, users := range a.state.MessageReactions[message.ID] {
		copy.Reactions = append(copy.Reactions, model.MessageReactionSummary{Emoji: emoji, Count: len(users), ReactedByMe: users[uid]})
	}
	sort.Slice(copy.Reactions, func(i, j int) bool { return copy.Reactions[i].Emoji < copy.Reactions[j].Emoji })
	return &copy
}

func (a *App) Read(uid, cid string, seq int64) error {
	if seq < 0 {
		return ErrInvalid
	}
	if s, ok := a.persistence.(store.RuntimeMutationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, err := s.MarkRead(ctx, uid, cid, seq, time.Now())
		if err == store.ErrForbidden {
			return ErrForbidden
		}
		return err
	}
	if err := a.refresh(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.state.Members[cid][uid]
	if m == nil {
		return ErrForbidden
	}
	c := a.state.Conversations[cid]
	if seq > c.LastMessageSeq {
		seq = c.LastMessageSeq
	}
	if seq > m.LastReadSeq {
		m.LastReadSeq = seq
	}
	m.ManualUnread = false
	ids := memberIDs(a.state.Members[cid])
	a.syncLocked(uid, "conversation.read", map[string]any{"conversationId": cid, "seq": seq})
	if err := a.saveLocked(); err != nil {
		return err
	}
	go a.publish(ids, "message.read", map[string]any{"conversationId": cid, "userId": uid, "seq": seq})
	return nil
}

func (a *App) Delivered(uid, cid string, seq int64) (int64, error) {
	if seq < 0 {
		return 0, ErrInvalid
	}
	if s, ok := a.persistence.(store.RuntimeMutationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		actual, _, err := s.MarkDelivered(ctx, uid, cid, seq, time.Now())
		if err == store.ErrForbidden {
			return 0, ErrForbidden
		}
		return actual, err
	}
	if err := a.refresh(); err != nil {
		return 0, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.state.Members[cid][uid]
	if m == nil {
		return 0, ErrForbidden
	}
	c := a.state.Conversations[cid]
	if seq > c.LastMessageSeq {
		seq = c.LastMessageSeq
	}
	if seq <= m.LastDeliveredSeq {
		return m.LastDeliveredSeq, nil
	}
	m.LastDeliveredSeq = seq
	payload := map[string]any{"conversationId": cid, "userId": uid, "seq": seq}
	ids := memberIDs(a.state.Members[cid])
	for _, memberID := range ids {
		a.syncLocked(memberID, "message.delivered", payload)
	}
	if err := a.saveLocked(); err != nil {
		return 0, err
	}
	go a.publish(ids, "message.delivered", payload)
	return seq, nil
}

func (a *App) UpdateConversationPreferences(uid, cid string, preferences store.ConversationPreferences) error {
	if preferences.Pinned == nil && preferences.Archived == nil && preferences.NotificationsMuted == nil && preferences.ManualUnread == nil {
		return ErrInvalid
	}
	if s, ok := a.persistence.(store.RuntimeMutationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.UpdateConversationPreferences(ctx, uid, cid, preferences); err != nil {
			if err == store.ErrForbidden {
				return ErrForbidden
			}
			return err
		}
		return nil
	}
	if err := a.refresh(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.state.Members[cid][uid]
	if m == nil {
		return ErrForbidden
	}
	if preferences.Pinned != nil {
		m.Pinned = *preferences.Pinned
	}
	if preferences.Archived != nil {
		m.Archived = *preferences.Archived
	}
	if preferences.NotificationsMuted != nil {
		m.NotificationsMuted = *preferences.NotificationsMuted
	}
	if preferences.ManualUnread != nil {
		m.ManualUnread = *preferences.ManualUnread
	}
	a.syncLocked(uid, "conversation.preferences.updated", map[string]any{"conversationId": cid, "pinned": m.Pinned, "archived": m.Archived, "notificationsMuted": m.NotificationsMuted, "manualUnread": m.ManualUnread})
	return a.saveLocked()
}

func (a *App) HideConversation(uid, cid string) error {
	if s, ok := a.persistence.(store.RuntimeMutationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.HideConversation(ctx, uid, cid); err != nil {
			if err == store.ErrForbidden {
				return ErrForbidden
			}
			return err
		}
		return nil
	}
	if err := a.refresh(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.state.Members[cid][uid]
	c := a.state.Conversations[cid]
	if m == nil || c == nil {
		return ErrForbidden
	}
	hiddenUntil := c.LastMessageSeq
	m.HiddenUntilSeq = &hiddenUntil
	m.Pinned = false
	m.ManualUnread = false
	return a.saveLocked()
}
func (a *App) CanAccess(uid, cid string) bool {
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ok, err := q.CanAccessConversation(ctx, uid, cid)
		return err == nil && ok
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Members[cid][uid] != nil
}
func (a *App) SetTyping(uid, cid string, typing bool) {
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		access, err := q.CanAccessConversation(ctx, uid, cid)
		if err != nil || !access {
			return
		}
		ids, err := q.ConversationMemberIDs(ctx, cid)
		if err != nil {
			return
		}
		others := make([]string, 0, len(ids))
		for _, x := range ids {
			if x != uid {
				others = append(others, x)
			}
		}
		a.publish(others, "typing", map[string]any{"conversationId": cid, "userId": uid, "typing": typing, "expiresAt": time.Now().Add(6 * time.Second)})
		if bus, ok := a.persistence.(store.EphemeralBus); ok {
			_ = bus.PublishEphemeral(context.Background(), others, "typing", map[string]any{"conversationId": cid, "userId": uid, "typing": typing, "expiresAt": time.Now().Add(6 * time.Second)})
		}
		return
	}
	a.mu.RLock()
	if a.state.Members[cid][uid] == nil {
		a.mu.RUnlock()
		return
	}
	ids := memberIDs(a.state.Members[cid])
	a.mu.RUnlock()
	others := make([]string, 0, len(ids))
	for _, x := range ids {
		if x != uid {
			others = append(others, x)
		}
	}
	a.publish(others, "typing", map[string]any{"conversationId": cid, "userId": uid, "typing": typing, "expiresAt": time.Now().Add(6 * time.Second)})
}
func (a *App) callMemberIDs(uid, cid string) ([]string, error) {
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		access, err := q.CanAccessConversation(ctx, uid, cid)
		if err != nil {
			return nil, err
		}
		if !access {
			return nil, ErrForbidden
		}
		ids, err := q.ConversationMemberIDs(ctx, cid)
		if err != nil {
			return nil, err
		}
		return ids, nil
	}
	a.mu.RLock()
	if a.state.Members[cid][uid] == nil {
		a.mu.RUnlock()
		return nil, ErrForbidden
	}
	ids := memberIDs(a.state.Members[cid])
	a.mu.RUnlock()
	return ids, nil
}

func (a *App) publishCall(users []string, typ string, payload map[string]any) error {
	a.publish(users, typ, payload)
	if bus, ok := a.persistence.(store.EphemeralBus); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := bus.PublishEphemeral(ctx, users, typ, payload); err != nil {
			return fmt.Errorf("publish realtime call signal: %w", err)
		}
	}
	return nil
}

func (a *App) InviteCall(uid, cid, calleeID, callID, mediaType string) (*model.CallSession, bool, error) {
	if mediaType != "audio" && mediaType != "video" {
		return nil, false, ErrInvalid
	}
	if !a.settingBool("callsEnabled", true) || (mediaType == "video" && !a.settingBool("videoCallsEnabled", true)) {
		return nil, false, ErrForbidden
	}
	if callID == "" {
		callID = id("call")
	}
	if !callIDPattern.MatchString(callID) {
		return nil, false, ErrInvalid
	}
	ids, err := a.callMemberIDs(uid, cid)
	if err != nil {
		return nil, false, err
	}
	if len(ids) != 2 {
		return nil, false, ErrConflict
	}
	if calleeID == "" {
		for _, member := range ids {
			if member != uid {
				calleeID = member
			}
		}
	}
	if calleeID == "" || calleeID == uid {
		return nil, false, ErrInvalid
	}
	calleeIsMember := false
	for _, member := range ids {
		calleeIsMember = calleeIsMember || member == calleeID
	}
	if !calleeIsMember {
		return nil, false, ErrForbidden
	}
	now := time.Now()
	invite := store.CallInvite{ID: callID, ConversationID: cid, CallerID: uid, CalleeID: calleeID, MediaType: mediaType, InvitedAt: now, ExpiresAt: now.Add(a.callInviteTTL)}
	var call *model.CallSession
	var duplicate bool
	if calls, ok := a.persistence.(store.CallStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		call, duplicate, err = calls.InviteCall(ctx, invite)
		if err != nil {
			return nil, false, mapStoreError(err)
		}
	} else {
		a.callMu.Lock()
		defer a.callMu.Unlock()
		if existing := a.calls[callID]; existing != nil {
			if existing.ConversationID != cid || existing.CallerID != uid || existing.CalleeID != calleeID || existing.MediaType != mediaType {
				return nil, false, ErrConflict
			}
			copy := *existing
			return &copy, true, nil
		}
		for _, existing := range a.calls {
			if existing.Status == "invited" && !now.Before(existing.ExpiresAt) {
				existing.Status, existing.EndReason, existing.UpdatedAt, existing.EndedAt = "missed", "timeout", now, &now
			}
			if existing.ConversationID == cid && (existing.Status == "invited" || existing.Status == "accepted") {
				return nil, false, ErrConflict
			}
		}
		call = &model.CallSession{ID: callID, ConversationID: cid, CallerID: uid, CalleeID: calleeID, MediaType: mediaType, Status: "invited", InvitedAt: now, ExpiresAt: invite.ExpiresAt, UpdatedAt: now}
		a.calls[callID] = call
	}
	if !duplicate {
		_ = a.publishCall([]string{calleeID}, "call.invited", map[string]any{"call": call, "callId": call.ID, "conversationId": call.ConversationID, "mediaType": call.MediaType})
	}
	return call, duplicate, nil
}

func mapStoreError(err error) error {
	switch err {
	case nil:
		return nil
	case store.ErrNotFound:
		return ErrNotFound
	case store.ErrForbidden:
		return ErrForbidden
	case store.ErrConflict:
		return ErrConflict
	default:
		return err
	}
}

func (a *App) TransitionCall(uid, callID, action, reason string) (*model.CallSession, bool, error) {
	if !callIDPattern.MatchString(callID) || len([]rune(reason)) > 200 {
		return nil, false, ErrInvalid
	}
	var call *model.CallSession
	var duplicate bool
	var err error
	if calls, ok := a.persistence.(store.CallStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		call, duplicate, err = calls.TransitionCall(ctx, callID, uid, action, reason, time.Now())
		if err != nil {
			return call, false, mapStoreError(err)
		}
	} else {
		a.callMu.Lock()
		call, duplicate, err = transitionMemoryCall(a.calls[callID], uid, action, reason, time.Now())
		a.callMu.Unlock()
		if err != nil {
			return call, false, err
		}
	}
	if !duplicate {
		event := map[string]string{"accept": "call.accepted", "reject": "call.rejected", "cancel": "call.cancelled", "hangup": "call.ended", "end": "call.ended"}[action]
		_ = a.publishCall([]string{call.CallerID, call.CalleeID}, event, map[string]any{"call": call})
	}
	return call, duplicate, nil
}

func transitionMemoryCall(c *model.CallSession, uid, action, reason string, at time.Time) (*model.CallSession, bool, error) {
	if c == nil {
		return nil, false, ErrNotFound
	}
	if uid != c.CallerID && uid != c.CalleeID {
		return nil, false, ErrForbidden
	}
	if c.Status == "invited" && !at.Before(c.ExpiresAt) {
		c.Status, c.EndReason, c.UpdatedAt, c.EndedAt = "missed", "timeout", at, &at
		return c, false, ErrConflict
	}
	target := ""
	switch action {
	case "accept":
		if c.Status == "accepted" && uid == c.CalleeID {
			return c, true, nil
		}
		if c.Status != "invited" || uid != c.CalleeID {
			return nil, false, ErrConflict
		}
		target, c.AcceptedAt = "accepted", &at
	case "reject":
		if c.Status == "rejected" && uid == c.CalleeID {
			return c, true, nil
		}
		if c.Status != "invited" || uid != c.CalleeID {
			return nil, false, ErrConflict
		}
		target = "rejected"
	case "cancel":
		if c.Status == "cancelled" && uid == c.CallerID {
			return c, true, nil
		}
		if c.Status != "invited" || uid != c.CallerID {
			return nil, false, ErrConflict
		}
		target = "cancelled"
	case "hangup", "end":
		if c.Status == "ended" || (action == "end" && (c.Status == "cancelled" || c.Status == "rejected")) {
			return c, true, nil
		}
		if c.Status == "accepted" {
			target = "ended"
		} else if action == "end" && c.Status == "invited" && uid == c.CallerID {
			target = "cancelled"
		} else if action == "end" && c.Status == "invited" && uid == c.CalleeID {
			target = "rejected"
		} else {
			return nil, false, ErrConflict
		}
	default:
		return nil, false, ErrInvalid
	}
	c.Status, c.EndReason, c.UpdatedAt = target, reason, at
	if target != "accepted" {
		c.EndedAt, c.EndedBy = &at, uid
		if c.AcceptedAt != nil {
			c.DurationSeconds = max(int64(0), int64(at.Sub(*c.AcceptedAt).Seconds()))
		}
	}
	copy := *c
	return &copy, false, nil
}

func (a *App) GetCall(uid, callID string) (*model.CallSession, error) {
	if calls, ok := a.persistence.(store.CallStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		call, err := calls.GetCall(ctx, callID, uid, time.Now())
		return call, mapStoreError(err)
	}
	a.callMu.Lock()
	defer a.callMu.Unlock()
	call := a.calls[callID]
	if call == nil {
		return nil, ErrNotFound
	}
	if uid != call.CallerID && uid != call.CalleeID {
		return nil, ErrNotFound
	}
	if call.Status == "invited" && !time.Now().Before(call.ExpiresAt) {
		now := time.Now()
		call.Status, call.EndReason, call.UpdatedAt, call.EndedAt = "missed", "timeout", now, &now
	}
	copy := *call
	return &copy, nil
}

func (a *App) AdminCalls(q, status, cursor string, limit int) ([]*model.CallSession, int64, string, error) {
	if calls, ok := a.persistence.(store.CallStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return calls.ListAdminCalls(ctx, q, status, cursor, limit)
	}
	return []*model.CallSession{}, 0, "", nil
}

func (a *App) RunCallTimeouts(ctx context.Context) {
	calls, ok := a.persistence.(store.CallStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			expired, err := calls.ExpireCalls(ctx, now, 100)
			if err != nil {
				continue
			}
			for _, call := range expired {
				_ = a.publishCall([]string{call.CallerID, call.CalleeID}, "call.timeout", map[string]any{"call": call})
			}
		}
	}
}

func (a *App) SignalCall(uid, cid, typ string, payload map[string]any) error {
	callID, _ := payload["callId"].(string)
	if !callIDPattern.MatchString(callID) {
		return ErrInvalid
	}
	deliverType := typ
	switch typ {
	case "call.offer":
		mediaType, _ := payload["mediaType"].(string)
		callee, _ := payload["calleeUserId"].(string)
		call, _, err := a.InviteCall(uid, cid, callee, callID, mediaType)
		if err != nil {
			return err
		}
		if call.ConversationID != cid {
			return ErrConflict
		}
	case "call.answer":
		existing, err := a.GetCall(uid, callID)
		if err != nil {
			return err
		}
		if existing.ConversationID != cid {
			return ErrConflict
		}
		call, _, err := a.TransitionCall(uid, callID, "accept", "")
		if err != nil {
			return err
		}
		if call.ConversationID != cid {
			return ErrConflict
		}
	case "call.end":
		existing, err := a.GetCall(uid, callID)
		if err != nil {
			return err
		}
		if existing.ConversationID != cid {
			return ErrConflict
		}
		call, _, err := a.TransitionCall(uid, callID, "end", stringValue(payload["reason"]))
		if err != nil {
			return err
		}
		if call.ConversationID != cid {
			return ErrConflict
		}
	case "call.ice":
		call, err := a.GetCall(uid, callID)
		if err != nil {
			return err
		}
		if call.ConversationID != cid || (call.Status != "invited" && call.Status != "accepted") {
			return ErrConflict
		}
	case "call.signal.received":
		call, err := a.GetCall(uid, callID)
		if err != nil {
			return err
		}
		signalID, _ := payload["signalId"].(string)
		if call.ConversationID != cid || !callIDPattern.MatchString(signalID) {
			return ErrInvalid
		}
		deliverType = "call.signal.ack"
	default:
		return ErrInvalid
	}
	ids, err := a.callMemberIDs(uid, cid)
	if err != nil {
		return err
	}
	others := make([]string, 0, len(ids))
	for _, member := range ids {
		if member != uid {
			others = append(others, member)
		}
	}
	payload["conversationId"], payload["fromUserId"], payload["serverTime"] = cid, uid, time.Now()
	return a.publishCall(others, deliverType, payload)
}
func stringValue(v any) string { s, _ := v.(string); return s }
func (a *App) Sync(uid string, after int64, limit int) ([]*model.SyncEvent, int64, bool) {
	if q, ok := a.persistence.(store.QueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		events, cursor, more, err := q.ListSync(ctx, uid, after, limit)
		if err == nil {
			return events, cursor, more
		}
		return nil, after, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	xs := a.state.SyncEvents[uid]
	i := sort.Search(len(xs), func(i int) bool { return xs[i].Seq > after })
	end := min(len(xs), i+limit)
	out := append([]*model.SyncEvent(nil), xs[i:end]...)
	cursor := after
	if len(out) > 0 {
		cursor = out[len(out)-1].Seq
	}
	return out, cursor, end < len(xs)
}
func (a *App) syncLocked(uid, typ string, payload map[string]any) *model.SyncEvent {
	a.state.UserSyncSeq[uid]++
	// 同步事件离开状态锁后会异步序列化。必须在锁内保存独立快照，避免后续
	// 编辑消息、修改群资料等操作与 WebSocket JSON 编码并发读写同一对象。
	snapshot := cloneJSONMap(payload)
	e := &model.SyncEvent{Seq: a.state.UserSyncSeq[uid], UserID: uid, Type: typ, Payload: snapshot, CreatedAt: time.Now()}
	a.state.SyncEvents[uid] = append(a.state.SyncEvents[uid], e)
	return e
}

func cloneJSONMap(value map[string]any) map[string]any {
	raw, err := json.Marshal(value)
	if err != nil {
		// 所有同步 payload 都由服务端构造且必须满足 JSON 协议；若新增了不可编码
		// 类型，应在测试阶段立即暴露，而不是发送不完整事件导致客户端游标跳过。
		panic("sync payload is not JSON serializable: " + err.Error())
	}
	cloned := make(map[string]any, len(value))
	if err = json.Unmarshal(raw, &cloned); err != nil {
		panic("sync payload snapshot cannot be decoded: " + err.Error())
	}
	return cloned
}
func (a *App) publish(ids []string, typ string, payload any) {
	a.emitMu.RLock()
	emit := a.emit
	a.emitMu.RUnlock()
	if emit != nil {
		emit(ids, typ, payload)
	}
}

// Deliver publishes a durable cross-instance event to connections held by this
// process. Persistence implementations invoke it after receiving the event bus.
func (a *App) Deliver(ids []string, typ string, payload map[string]any) { a.publish(ids, typ, payload) }

func (a *App) Report(uid, targetType, targetID, reason, details string) (*model.Report, error) {
	if targetID == "" || reason == "" || len([]rune(reason)) > 80 || len([]rune(details)) > 5000 {
		return nil, ErrInvalid
	}
	if targetType != "user" && targetType != "message" && targetType != "group" {
		return nil, ErrInvalid
	}
	if s, ok := a.persistence.(store.RuntimeMutationStore); ok {
		now := time.Now()
		r := &model.Report{ID: id("rpt"), ReporterID: uid, TargetType: targetType, TargetID: targetID, Reason: reason, Details: details, Status: "pending", CreatedAt: now, UpdatedAt: now}
		audit := &model.AuditEntry{ID: id("aud"), ActorID: uid, Action: "report.created", TargetType: targetType, TargetID: targetID, Metadata: map[string]any{"reportId": r.ID}, CreatedAt: now}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.CreateReportRecord(ctx, r, audit); err != nil {
			if err == store.ErrNotFound {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return r, nil
	}
	if err := a.refresh(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	r := &model.Report{ID: id("rpt"), ReporterID: uid, TargetType: targetType, TargetID: targetID, Reason: reason, Details: details, Status: "pending", CreatedAt: now, UpdatedAt: now}
	a.state.Reports[r.ID] = r
	a.auditLocked(uid, "report.created", targetType, targetID, map[string]any{"reportId": r.ID})
	if err := a.saveLocked(); err != nil {
		return nil, err
	}
	return r, nil
}
func (a *App) AdminStats() map[string]any {
	_ = a.refresh()
	a.mu.RLock()
	defer a.mu.RUnlock()
	pending := 0
	banned := 0
	for _, r := range a.state.Reports {
		if r.Status == "pending" {
			pending++
		}
	}
	for _, u := range a.state.Users {
		if u.Banned {
			banned++
		}
	}
	return map[string]any{"users": len(a.state.Users), "bannedUsers": banned, "conversations": len(a.state.Conversations), "messages": len(a.state.MessageByID), "pendingReports": pending, "websocketConnections": a.Metrics.WSConnections.Load()}
}

// AdminDashboard 优先使用持久化层的实时聚合，内存模式仅用于开发和测试回退。
func (a *App) AdminDashboard() (map[string]any, error) {
	if s, ok := a.persistence.(store.AdminDashboardStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		result, err := s.AdminDashboard(ctx)
		if err != nil {
			return nil, err
		}
		result["websocketConnections"] = a.Metrics.WSConnections.Load()
		result["generatedAt"] = time.Now().UTC()
		return result, nil
	}
	result := a.AdminStats()
	result["generatedAt"] = time.Now().UTC()
	return result, nil
}
func (a *App) AdminUsers(q string) []*model.User { return a.SearchUsers(q) }
func (a *App) AdminUsersPage(q, status, cursor string, limit int) ([]*model.User, int64, string, error) {
	if s, ok := a.persistence.(store.AdminQueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.ListAdminUsers(ctx, q, status, cursor, limit)
	}
	all := a.AdminUsers(q)
	items := make([]*model.User, 0, len(all))
	for _, user := range all {
		if status == "" || (status == "active" && !user.Banned) || (status == "banned" && user.Banned) {
			items = append(items, user)
		}
	}
	start, end, next := pageBounds(cursor, limit, len(items))
	return items[start:end], int64(len(items)), next, nil
}
func (a *App) AdminFriendshipsPage(q, cursor string, limit int) ([]store.AdminFriendship, int64, string, error) {
	if s, ok := a.persistence.(store.AdminOperationsStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.ListAdminFriendships(ctx, q, cursor, limit)
	}
	return nil, 0, "", store.ErrUnsupported
}
func (a *App) AdminGroupsPage(q, status, cursor string, limit int) ([]map[string]any, int64, string, error) {
	if s, ok := a.persistence.(store.AdminOperationsStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.ListAdminGroups(ctx, q, status, cursor, limit)
	}
	all := a.AdminGroups(q)
	start, end, next := pageBounds(cursor, limit, len(all))
	return all[start:end], int64(len(all)), next, nil
}
func (a *App) AdminFeedbackPage(q, category, cursor string, limit int) ([]store.AdminFeedback, int64, string, error) {
	if s, ok := a.persistence.(store.AdminOperationsStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.ListAdminFeedback(ctx, q, category, cursor, limit)
	}
	return nil, 0, "", store.ErrUnsupported
}
func (a *App) AdminPushStatus() (map[string]any, error) {
	if s, ok := a.persistence.(store.AdminOperationsStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.AdminPushStatus(ctx)
	}
	return nil, store.ErrUnsupported
}
func (a *App) AdminTaskStatus() (map[string]any, error) {
	if s, ok := a.persistence.(store.AdminOperationsStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.AdminTaskStatus(ctx)
	}
	return nil, store.ErrUnsupported
}
func (a *App) AdminUserOverview(id string) (map[string]any, error) {
	if s, ok := a.persistence.(store.AdminOperationsStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.AdminUserOverview(ctx, id)
	}
	return nil, store.ErrUnsupported
}
func (a *App) AdminGroupOverview(id string) (map[string]any, error) {
	if s, ok := a.persistence.(store.AdminOperationsStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.AdminGroupOverview(ctx, id)
	}
	return nil, store.ErrUnsupported
}
func (a *App) AdminGroupMembers(id, q, cursor string, limit int) ([]*model.ConversationMember, int64, string, error) {
	if s, ok := a.persistence.(store.AdminOperationsStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.ListAdminGroupMembers(ctx, id, q, cursor, limit)
	}
	return nil, 0, "", store.ErrUnsupported
}
func (a *App) RecordAdminAudit(actor, action, targetType, targetID, result, ip string, metadata map[string]any) {
	if s, ok := a.persistence.(store.AdminOperationsStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.RecordAdminAudit(ctx, &model.AuditEntry{ID: id("aud"), ActorID: actor, Action: action, TargetType: targetType, TargetID: targetID, Metadata: metadata, Result: result, IP: ip, CreatedAt: time.Now()})
	}
}
func (a *App) AdminBan(actor, uid string, banned bool, durationHours int, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 500 || (banned && (durationHours < 0 || durationHours > 8760)) {
		return ErrInvalid
	}
	var until *time.Time
	if banned && durationHours > 0 {
		value := time.Now().Add(time.Duration(durationHours) * time.Hour)
		until = &value
	}
	if s, ok := a.persistence.(store.RuntimeMutationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := s.SetUserBanRecord(ctx, actor, uid, banned, until, reason, id("aud"), time.Now())
		if err == store.ErrNotFound {
			return ErrNotFound
		}
		return err
	}
	if err := a.refresh(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u := a.state.Users[uid]
	if u == nil {
		return ErrNotFound
	}
	u.Banned = banned
	u.BannedUntil = until
	a.auditLocked(actor, map[bool]string{true: "user.banned", false: "user.unbanned"}[banned], "user", uid, map[string]any{"reason": reason, "bannedUntil": until})
	if err := a.saveLocked(); err != nil {
		return err
	}
	if banned {
		return a.revokeAllLocked(uid)
	}
	return nil
}

func (a *App) RunBanExpirations(ctx context.Context) {
	s, ok := a.persistence.(store.BanExpiryStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		for {
			ids, err := s.ExpireUserBans(ctx, time.Now(), 100)
			if err != nil || len(ids) < 100 {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) CreateRefreshSession(id, uid string, hash []byte, exp time.Time) error {
	if s, ok := a.persistence.(store.RefreshSessionStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return s.CreateRefreshSession(ctx, id, uid, hash, exp)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshSessions[id] = refreshSession{UserID: uid, Hash: append([]byte(nil), hash...), ExpiresAt: exp}
	return nil
}
func (a *App) RotateRefreshSession(oldID, newID, uid string, hash []byte, exp time.Time) error {
	if s, ok := a.persistence.(store.RefreshSessionStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.RotateRefreshSession(ctx, oldID, newID, hash, exp, uid); err != nil {
			return ErrForbidden
		}
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	old, ok := a.refreshSessions[oldID]
	if !ok || old.Revoked || old.UserID != uid || time.Now().After(old.ExpiresAt) {
		return ErrForbidden
	}
	old.Revoked = true
	a.refreshSessions[oldID] = old
	a.refreshSessions[newID] = refreshSession{UserID: uid, Hash: append([]byte(nil), hash...), ExpiresAt: exp}
	return nil
}
func (a *App) RevokeRefreshSession(id, uid string) error {
	if s, ok := a.persistence.(store.RefreshSessionStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.RevokeRefreshSession(ctx, id, uid); err != nil {
			return ErrNotFound
		}
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.refreshSessions[id]
	if !ok || v.UserID != uid {
		return ErrNotFound
	}
	v.Revoked = true
	a.refreshSessions[id] = v
	return nil
}
func (a *App) revokeAllLocked(uid string) error {
	if s, ok := a.persistence.(store.RefreshSessionStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return s.RevokeUserRefreshSessions(ctx, uid)
	}
	for id, v := range a.refreshSessions {
		if v.UserID == uid {
			v.Revoked = true
			a.refreshSessions[id] = v
		}
	}
	return nil
}
func (a *App) RevokeAllRefreshSessions(uid string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.revokeAllLocked(uid)
}
func (a *App) AdminReports(status string) []*model.Report {
	_ = a.refresh()
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := []*model.Report{}
	for _, r := range a.state.Reports {
		if status == "" || r.Status == status {
			c := *r
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}
func (a *App) AdminReportsPage(q, status, cursor string, limit int) ([]*model.Report, int64, string, error) {
	if s, ok := a.persistence.(store.AdminQueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.ListAdminReports(ctx, q, status, cursor, limit)
	}
	q = strings.ToLower(q)
	all := a.AdminReports(status)
	items := make([]*model.Report, 0, len(all))
	for _, r := range all {
		haystack := strings.ToLower(strings.Join([]string{r.ID, r.ReporterID, r.TargetID, r.Reason, r.Details}, " "))
		if q == "" || strings.Contains(haystack, q) {
			items = append(items, r)
		}
	}
	start, end, next := pageBounds(cursor, limit, len(items))
	return items[start:end], int64(len(items)), next, nil
}
func (a *App) ResolveReport(actor, rid, action, resolution string) (string, error) {
	allowed := map[string]bool{"dismiss": true, "no_violation": true, "delete_message": true, "ban_user": true}
	if !allowed[action] {
		return "", ErrInvalid
	}
	if s, ok := a.persistence.(store.RuntimeMutationStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		status, err := s.ResolveReportRecord(ctx, actor, rid, action, resolution, id("aud"), time.Now())
		if err == store.ErrNotFound {
			return "", ErrNotFound
		}
		if err == store.ErrConflict {
			return "", ErrConflict
		}
		return status, err
	}
	status := "resolved"
	if action == "dismiss" || action == "no_violation" {
		status = "rejected"
	}
	if err := a.refresh(); err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.state.Reports[rid]
	if r == nil {
		return "", ErrNotFound
	}
	if action == "delete_message" {
		m := a.state.MessageByID[r.TargetID]
		if r.TargetType != "message" || m == nil {
			return "", ErrConflict
		}
		now := time.Now()
		m.Body = map[string]any{}
		m.RecalledAt = &now
	}
	if action == "ban_user" {
		target := r.TargetID
		if r.TargetType == "message" {
			m := a.state.MessageByID[target]
			if m == nil {
				return "", ErrNotFound
			}
			target = m.SenderID
		} else if r.TargetType != "user" {
			return "", ErrConflict
		}
		u := a.state.Users[target]
		if u == nil {
			return "", ErrNotFound
		}
		u.Banned = true
		_ = a.revokeAllLocked(target)
	}
	r.Status = status
	r.Resolution = resolution
	r.UpdatedAt = time.Now()
	a.auditLocked(actor, "report."+status, "report", rid, map[string]any{"resolution": resolution})
	if err := a.saveLocked(); err != nil {
		return "", err
	}
	return status, nil
}
func (a *App) Audits() []*model.AuditEntry {
	_ = a.refresh()
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]*model.AuditEntry(nil), a.state.Audits...)
}
func (a *App) AdminAuditsPage(q, status, cursor string, limit int) ([]*model.AuditEntry, int64, string, error) {
	if s, ok := a.persistence.(store.AdminQueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.ListAdminAudits(ctx, q, status, cursor, limit)
	}
	q = strings.ToLower(q)
	all := a.Audits()
	items := make([]*model.AuditEntry, 0, len(all))
	for _, entry := range all {
		haystack := strings.ToLower(strings.Join([]string{entry.ActorID, entry.Action, entry.TargetType, entry.TargetID}, " "))
		if (q == "" || strings.Contains(haystack, q)) && (status == "" || entry.Action == status) {
			items = append(items, entry)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	start, end, next := pageBounds(cursor, limit, len(items))
	return items[start:end], int64(len(items)), next, nil
}

func (a *App) AdminMessagesPage(q, messageType, cursor string, limit int) ([]*model.Message, int64, string, error) {
	if s, ok := a.persistence.(store.AdminQueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.ListAdminMessages(ctx, q, messageType, cursor, limit)
	}
	q = strings.ToLower(strings.TrimSpace(q))
	a.mu.RLock()
	items := make([]*model.Message, 0, len(a.state.MessageByID))
	for _, message := range a.state.MessageByID {
		raw, _ := json.Marshal(message.Body)
		haystack := strings.ToLower(strings.Join([]string{message.ID, message.ConversationID, message.SenderID, message.ClientMsgID, string(raw)}, " "))
		if (q == "" || strings.Contains(haystack, q)) && (messageType == "" || message.Type == messageType) {
			copy := *message
			items = append(items, &copy)
		}
	}
	a.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	start, end, next := pageBounds(cursor, limit, len(items))
	return items[start:end], int64(len(items)), next, nil
}

func (a *App) AdminMediaPage(q, status, cursor string, limit int) ([]*model.Media, int64, string, error) {
	if s, ok := a.persistence.(store.AdminQueryStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.ListAdminMedia(ctx, q, status, cursor, limit)
	}
	q = strings.ToLower(strings.TrimSpace(q))
	a.mu.RLock()
	items := make([]*model.Media, 0, len(a.state.Media))
	for _, media := range a.state.Media {
		haystack := strings.ToLower(strings.Join([]string{media.ID, media.OwnerID, media.ObjectKey, media.MIME, media.Checksum}, " "))
		if (q == "" || strings.Contains(haystack, q)) && (status == "" || media.Status == status) {
			copy := *media
			items = append(items, &copy)
		}
	}
	a.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].ID > items[j].ID })
	start, end, next := pageBounds(cursor, limit, len(items))
	return items[start:end], int64(len(items)), next, nil
}

func pageBounds(cursor string, limit, total int) (int, int, string) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	start, err := strconv.Atoi(cursor)
	if err != nil || start < 0 || start > total {
		start = 0
	}
	end := min(start+limit, total)
	next := ""
	if end < total {
		next = strconv.Itoa(end)
	}
	return start, end, next
}

func validateAnnouncementInput(input store.AnnouncementInput, now time.Time) error {
	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(input.Content)
	if input.Title == "" || len([]rune(input.Title)) > 80 || input.Content == "" || len([]rune(input.Content)) > 5000 {
		return ErrInvalid
	}
	if input.Status != "draft" && input.Status != "scheduled" {
		return ErrInvalid
	}
	if input.TargetType != "all" && input.TargetType != "users" {
		return ErrInvalid
	}
	if input.TargetType == "users" && (len(input.TargetUserIDs) == 0 || len(input.TargetUserIDs) > 1000) {
		return ErrInvalid
	}
	if input.Status == "scheduled" && (input.ScheduledAt == nil || !input.ScheduledAt.After(now)) {
		return ErrInvalid
	}
	return nil
}

func (a *App) Announcements(uid string) ([]*model.Announcement, error) {
	now := time.Now()
	if s, ok := a.persistence.(store.AnnouncementStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.ListAnnouncements(ctx, uid, now)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var items []*model.Announcement
	for _, item := range a.announcements {
		if item.Status == "scheduled" && item.ScheduledAt != nil && !item.ScheduledAt.After(now) {
			item.Status = "published"
			published := *item.ScheduledAt
			item.PublishedAt = &published
			item.UpdatedAt = now
			a.auditLocked("system", "announcement.published", "announcement", item.ID, map[string]any{"scheduled": true, "push": item.PushOnPublish})
		}
		applies := item.TargetType == "all"
		if item.TargetType == "users" {
			for _, id := range item.TargetUserIDs {
				if id == uid {
					applies = true
					break
				}
			}
		}
		if item.Status == "published" && applies {
			copy := *item
			if reads := a.announcementReads[item.ID]; reads != nil {
				if readAt, ok := reads[uid]; ok {
					copy.ReadAt = &readAt
				}
			}
			items = append(items, &copy)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Pinned != items[j].Pinned {
			return items[i].Pinned
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

// RunAnnouncementScheduler publishes due announcements independently of user
// traffic. The PostgreSQL promotion is atomic, so this can run on every API
// replica without creating duplicate push outbox rows.
func (a *App) RunAnnouncementScheduler(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if s, ok := a.persistence.(store.AnnouncementStore); ok {
				workCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				_, _ = s.PromoteDueAnnouncements(workCtx, now)
				cancel()
				continue
			}
			a.mu.Lock()
			for _, item := range a.announcements {
				if item.Status == "scheduled" && item.ScheduledAt != nil && !item.ScheduledAt.After(now) {
					item.Status = "published"
					published := *item.ScheduledAt
					item.PublishedAt = &published
					item.UpdatedAt = now
					a.auditLocked("system", "announcement.published", "announcement", item.ID, map[string]any{"scheduled": true, "push": item.PushOnPublish})
				}
			}
			a.mu.Unlock()
		}
	}
}

func (a *App) MarkAnnouncementRead(uid, id string) error {
	if s, ok := a.persistence.(store.AnnouncementStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := s.MarkAnnouncementRead(ctx, uid, id, time.Now())
		if err == store.ErrNotFound {
			return ErrNotFound
		}
		return err
	}
	items, _ := a.Announcements(uid)
	found := false
	for _, item := range items {
		if item.ID == id {
			found = true
			break
		}
	}
	if !found {
		return ErrNotFound
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.announcementReads[id] == nil {
		a.announcementReads[id] = map[string]time.Time{}
	}
	if _, ok := a.announcementReads[id][uid]; !ok {
		a.announcementReads[id][uid] = time.Now()
	}
	return nil
}
func (a *App) AdminAnnouncements(q, status, cursor string, limit int) ([]*model.Announcement, int64, string, error) {
	if s, ok := a.persistence.(store.AnnouncementStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.ListAdminAnnouncements(ctx, q, status, cursor, limit, time.Now())
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	q = strings.ToLower(q)
	var items []*model.Announcement
	for _, item := range a.announcements {
		if (status == "" || item.Status == status) && (q == "" || strings.Contains(strings.ToLower(item.ID+" "+item.Title+" "+item.Content), q)) {
			copy := *item
			items = append(items, &copy)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	start, end, next := pageBounds(cursor, limit, len(items))
	return items[start:end], int64(len(items)), next, nil
}
func (a *App) CreateAnnouncement(actor string, input store.AnnouncementInput) (*model.Announcement, error) {
	now := time.Now()
	input.ActorID = actor
	if !a.settingBool("announcementPushEnabled", true) {
		input.PushOnPublish = false
	}
	input.ID = id("announcement")
	if err := validateAnnouncementInput(input, now); err != nil {
		return nil, err
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(input.Content)
	if s, ok := a.persistence.(store.AnnouncementStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.CreateAnnouncement(ctx, input, now)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	item := &model.Announcement{ID: input.ID, Title: input.Title, Content: input.Content, Status: input.Status, Pinned: input.Pinned, TargetType: input.TargetType, TargetUserIDs: append([]string(nil), input.TargetUserIDs...), ScheduledAt: input.ScheduledAt, PushOnPublish: input.PushOnPublish, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	a.announcements[item.ID] = item
	a.auditLocked(actor, "announcement.created", "announcement", item.ID, map[string]any{"status": item.Status, "targetType": item.TargetType})
	copy := *item
	return &copy, nil
}
func (a *App) UpdateAnnouncement(actor, id string, input store.AnnouncementInput) (*model.Announcement, error) {
	now := time.Now()
	input.ActorID = actor
	if !a.settingBool("announcementPushEnabled", true) {
		input.PushOnPublish = false
	}
	if err := validateAnnouncementInput(input, now); err != nil {
		return nil, err
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(input.Content)
	if s, ok := a.persistence.(store.AnnouncementStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		item, err := s.UpdateAnnouncement(ctx, id, input, now)
		if err == store.ErrConflict {
			return nil, ErrConflict
		}
		return item, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	item := a.announcements[id]
	if item == nil {
		return nil, ErrNotFound
	}
	if item.Status != "draft" && item.Status != "scheduled" {
		return nil, ErrConflict
	}
	item.Title = input.Title
	item.Content = input.Content
	item.Status = input.Status
	item.Pinned = input.Pinned
	item.TargetType = input.TargetType
	item.TargetUserIDs = append([]string(nil), input.TargetUserIDs...)
	item.ScheduledAt = input.ScheduledAt
	item.PushOnPublish = input.PushOnPublish
	item.UpdatedAt = now
	a.auditLocked(actor, "announcement.updated", "announcement", item.ID, map[string]any{"status": item.Status, "targetType": item.TargetType})
	copy := *item
	return &copy, nil
}
func (a *App) PublishAnnouncement(actor, id string, push bool) (*model.Announcement, error) {
	now := time.Now()
	push = push && a.settingBool("announcementPushEnabled", true)
	if s, ok := a.persistence.(store.AnnouncementStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		item, err := s.PublishAnnouncement(ctx, id, actor, push, now)
		if err == store.ErrConflict {
			return nil, ErrConflict
		}
		return item, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	item := a.announcements[id]
	if item == nil {
		return nil, ErrNotFound
	}
	if item.Status == "published" {
		copy := *item
		return &copy, nil
	}
	if item.Status != "draft" && item.Status != "scheduled" {
		return nil, ErrConflict
	}
	item.Status = "published"
	item.ScheduledAt = nil
	item.PublishedAt = &now
	item.PushOnPublish = push
	item.UpdatedAt = now
	a.auditLocked(actor, "announcement.published", "announcement", item.ID, map[string]any{"push": push})
	copy := *item
	return &copy, nil
}
func (a *App) WithdrawAnnouncement(actor, id string) (*model.Announcement, error) {
	now := time.Now()
	if s, ok := a.persistence.(store.AnnouncementStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		item, err := s.WithdrawAnnouncement(ctx, id, actor, now)
		if err == store.ErrConflict {
			return nil, ErrConflict
		}
		return item, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	item := a.announcements[id]
	if item == nil {
		return nil, ErrNotFound
	}
	if item.Status != "published" && item.Status != "scheduled" {
		return nil, ErrConflict
	}
	item.Status = "withdrawn"
	item.WithdrawnAt = &now
	item.UpdatedAt = now
	a.auditLocked(actor, "announcement.withdrawn", "announcement", item.ID, nil)
	copy := *item
	return &copy, nil
}
func (a *App) DeleteAnnouncement(actor, id string) error {
	if s, ok := a.persistence.(store.AnnouncementStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := s.DeleteAnnouncement(ctx, id, actor, time.Now())
		if err == store.ErrConflict {
			return ErrConflict
		}
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	item := a.announcements[id]
	if item == nil {
		return ErrNotFound
	}
	if item.Status != "draft" && item.Status != "withdrawn" {
		return ErrConflict
	}
	delete(a.announcements, id)
	delete(a.announcementReads, id)
	a.auditLocked(actor, "announcement.deleted", "announcement", id, nil)
	return nil
}
func (a *App) AdminGroups(query string) []map[string]any {
	_ = a.refresh()
	a.mu.RLock()
	defer a.mu.RUnlock()
	q := strings.ToLower(query)
	out := []map[string]any{}
	for cid, c := range a.state.Conversations {
		if c.Type != "group" || (q != "" && !strings.Contains(strings.ToLower(c.Title), q)) {
			continue
		}
		out = append(out, map[string]any{"id": cid, "name": c.Title, "memberCount": len(a.state.Members[cid]), "lastMessageSeq": c.LastMessageSeq, "createdAt": c.CreatedAt})
	}
	return out
}
func (a *App) DisbandGroup(actor, cid, reason string) error {
	if groups, ok := a.persistence.(store.GroupStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		if err := groups.DisbandGroupRecord(ctx, actor, cid, reason, time.Now()); err != nil {
			return mapStoreError(err)
		}
		a.publish([]string{actor}, "group.disbanded", map[string]any{"conversationId": cid})
		return nil
	}
	if err := a.refresh(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.state.Conversations[cid]
	if c == nil || c.Type != "group" {
		return ErrNotFound
	}
	ids := memberIDs(a.state.Members[cid])
	delete(a.state.Conversations, cid)
	delete(a.state.Members, cid)
	delete(a.state.Messages, cid)
	a.auditLocked(actor, "group.disbanded", "group", cid, map[string]any{"reason": reason})
	for _, uid := range ids {
		a.syncLocked(uid, "group.disbanded", map[string]any{"conversationId": cid})
	}
	if err := a.saveLocked(); err != nil {
		return err
	}
	if ms, ok := a.persistence.(store.MutationStore); ok {
		if err := ms.DeleteConversation(context.Background(), cid); err != nil {
			return err
		}
	}
	go a.publish(ids, "group.disbanded", map[string]any{"conversationId": cid})
	return nil
}
func (a *App) SensitiveWords() map[string]string {
	if policies, ok := a.persistence.(store.PolicyStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if items, err := policies.ListSensitiveWords(ctx); err == nil {
			return items
		}
	}
	_ = a.refresh()
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := map[string]string{}
	for k, v := range a.state.SensitiveWords {
		out[k] = v
	}
	return out
}
func (a *App) AddSensitiveWord(actor, word, category string) (string, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return "", ErrInvalid
	}
	wid := id("word")
	if policies, ok := a.persistence.(store.PolicyStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := policies.CreateSensitiveWord(ctx, wid, word+"|"+category, actor, time.Now()); err != nil {
			return "", mapStoreError(err)
		}
		return wid, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.SensitiveWords[wid] = word + "|" + category
	a.auditLocked(actor, "sensitive_word.created", "sensitive_word", wid, map[string]any{"word": word, "category": category})
	return wid, a.saveLocked()
}
func (a *App) DeleteSensitiveWord(actor, wid string) error {
	if policies, ok := a.persistence.(store.PolicyStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		return mapStoreError(policies.DeleteSensitiveWordRecord(ctx, wid, actor, time.Now()))
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.state.SensitiveWords[wid]; !ok {
		return ErrNotFound
	}
	delete(a.state.SensitiveWords, wid)
	a.auditLocked(actor, "sensitive_word.deleted", "sensitive_word", wid, nil)
	if ms, ok := a.persistence.(store.MutationStore); ok {
		if err := ms.DeleteSensitiveWord(context.Background(), wid); err != nil {
			return err
		}
	}
	return a.saveLocked()
}
func (a *App) Settings() map[string]any {
	if policies, ok := a.persistence.(store.PolicyStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if settings, err := policies.RuntimeSettings(ctx); err == nil {
			a.mu.Lock()
			a.state.Settings = settings
			a.mu.Unlock()
			return settings
		}
	}
	_ = a.refresh()
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := map[string]any{}
	for k, v := range a.state.Settings {
		out[k] = v
	}
	return out
}
func (a *App) UpdateSettings(actor string, settings map[string]any) error {
	if len(settings) == 0 {
		return ErrInvalid
	}
	boolKeys := map[string]bool{
		"registrationEnabled": true, "allowRegistration": true, "maintenanceMode": true,
		"allowFriendRequests": true, "announcementPushEnabled": true, "callsEnabled": true, "videoCallsEnabled": true, "sensitiveWordEnabled": true,
		"allowSearchByHandle": true, "allowSearchByPhone": true,
	}
	numberRanges := map[string][2]float64{
		"passwordMinLength": {8, 16}, "maxMessageTextLength": {100, 10000}, "messageRecallMinutes": {1, 1440},
		"maxGroupMembers": {2, 5000}, "friendRequestExpiryDays": {1, 30}, "reportSlaHours": {1, 168},
	}
	for key, value := range settings {
		if boolKeys[key] {
			if _, ok := value.(bool); !ok {
				return ErrInvalid
			}
			continue
		}
		if bounds, ok := numberRanges[key]; ok {
			number, valid := value.(float64)
			if !valid || number < bounds[0] || number > bounds[1] || number != float64(int(number)) {
				return ErrInvalid
			}
			continue
		}
		if key == "announcement" {
			text, ok := value.(string)
			if !ok || len([]rune(text)) > 500 {
				return ErrInvalid
			}
			continue
		}
		return ErrInvalid
	}
	if policies, ok := a.persistence.(store.PolicyStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := policies.UpdateRuntimeSettings(ctx, actor, settings, time.Now()); err != nil {
			return mapStoreError(err)
		}
		a.mu.Lock()
		for key, value := range settings {
			a.state.Settings[key] = value
		}
		a.mu.Unlock()
		a.policyMu.Lock()
		a.policyLoadedAt = time.Now()
		a.policyMu.Unlock()
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, v := range settings {
		a.state.Settings[k] = v
	}
	a.auditLocked(actor, "settings.updated", "settings", "global", settings)
	return a.saveLocked()
}
func (a *App) RegisterDevice(uid string, d store.Device) (*model.Device, error) {
	if d.ID == "" {
		d.ID = id("dev")
	}
	if d.PushToken == "" || (d.Platform != "ios" && d.Platform != "android" && d.Platform != "web") {
		return nil, ErrInvalid
	}
	if d.Provider == "" {
		if d.Platform == "ios" {
			d.Provider = "apns"
		} else {
			d.Provider = "fcm"
		}
	}
	if ds, ok := a.persistence.(store.DeviceStore); ok {
		if err := ds.RegisterDevice(context.Background(), uid, d); err != nil {
			if err == store.ErrForbidden {
				return nil, ErrForbidden
			}
			return nil, err
		}
	}
	item := &model.Device{ID: d.ID, UserID: uid, Platform: d.Platform, Provider: d.Provider, PushToken: d.PushToken, NotificationsEnabled: d.NotificationsEnabled, PreviewEnabled: d.PreviewEnabled, SoundEnabled: d.SoundEnabled, VibrationEnabled: d.VibrationEnabled, UpdatedAt: time.Now()}
	a.mu.Lock()
	a.state.Devices[item.ID] = item
	if _, ok := a.persistence.(store.DeviceStore); !ok {
		if err := a.saveLocked(); err != nil {
			a.mu.Unlock()
			return nil, err
		}
	}
	a.mu.Unlock()
	return item, nil
}
func (a *App) UnregisterDevice(uid, did string) error {
	if ds, ok := a.persistence.(store.DeviceStore); ok {
		if err := ds.UnregisterDevice(context.Background(), uid, did); err != nil {
			if err == store.ErrNotFound {
				return ErrNotFound
			}
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.state.Devices[did]
	if d != nil && d.UserID != uid {
		return ErrForbidden
	}
	delete(a.state.Devices, did)
	if _, ok := a.persistence.(store.DeviceStore); !ok {
		return a.saveLocked()
	}
	return nil
}
func (a *App) CreateMedia(m store.Media) error {
	if ms, ok := a.persistence.(store.MediaStore); ok {
		if err := ms.CreateMedia(context.Background(), m); err != nil {
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Media[m.ID] = &model.Media{ID: m.ID, OwnerID: m.OwnerID, ObjectKey: m.ObjectKey, MIME: m.MIME, Status: m.Status, Size: m.Size}
	if _, ok := a.persistence.(store.MediaStore); !ok {
		return a.saveLocked()
	}
	return nil
}
func (a *App) CompleteMedia(id, uid string, size int64, checksum string) error {
	if ms, ok := a.persistence.(store.MediaStore); ok {
		if err := ms.CompleteMedia(context.Background(), id, uid, size, checksum); err != nil {
			if err == store.ErrNotFound {
				return ErrNotFound
			}
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.state.Media[id]
	if m == nil {
		return ErrNotFound
	}
	if m.OwnerID != uid {
		return ErrForbidden
	}
	m.Status = "ready"
	m.Size = size
	m.Checksum = checksum
	if _, ok := a.persistence.(store.MediaStore); !ok {
		return a.saveLocked()
	}
	return nil
}
func (a *App) GetMedia(id string) (store.Media, error) {
	if ms, ok := a.persistence.(store.MediaStore); ok {
		m, err := ms.GetMedia(context.Background(), id)
		if err == store.ErrNotFound {
			return m, ErrNotFound
		}
		return m, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	m := a.state.Media[id]
	if m == nil {
		return store.Media{}, ErrNotFound
	}
	return store.Media{ID: m.ID, OwnerID: m.OwnerID, ObjectKey: m.ObjectKey, MIME: m.MIME, Status: m.Status, Checksum: m.Checksum, Size: m.Size}, nil
}
func (a *App) LeaseMediaCleanup(ctx context.Context, now time.Time, pendingAge, orphanAge, lease time.Duration, limit int) ([]store.MediaCleanupItem, error) {
	if cleanup, ok := a.persistence.(store.MediaCleanupStore); ok {
		return cleanup.LeaseMediaCleanup(ctx, now, pendingAge, orphanAge, lease, limit)
	}
	return nil, store.ErrUnsupported
}
func (a *App) CompleteMediaCleanup(ctx context.Context, id string, cleanupErr error, at time.Time) error {
	if cleanup, ok := a.persistence.(store.MediaCleanupStore); ok {
		return cleanup.CompleteMediaCleanup(ctx, id, cleanupErr, at)
	}
	return store.ErrUnsupported
}
func (a *App) MediaCleanupStatus() (store.MediaCleanupStatus, error) {
	if cleanup, ok := a.persistence.(store.MediaCleanupStore); ok {
		return cleanup.MediaCleanupStatus(context.Background(), time.Now(), time.Hour, 24*time.Hour)
	}
	return store.MediaCleanupStatus{}, store.ErrUnsupported
}
func (a *App) CanAccessMedia(uid, id string) (bool, error) {
	if ms, ok := a.persistence.(store.MediaStore); ok {
		allowed, err := ms.CanAccessMedia(context.Background(), uid, id)
		if err == store.ErrNotFound {
			return false, ErrNotFound
		}
		return allowed, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	m := a.state.Media[id]
	if m == nil {
		return false, nil
	}
	if m.OwnerID == uid {
		return true, nil
	}
	for cid, messages := range a.state.Messages {
		if a.state.Members[cid][uid] == nil {
			continue
		}
		for _, message := range messages {
			if mediaID, _ := message.Body["mediaId"].(string); mediaID == id {
				return true, nil
			}
		}
	}
	return false, nil
}
func (a *App) auditLocked(actor, action, targetType, targetID string, metadata map[string]any) {
	a.state.Audits = append(a.state.Audits, &model.AuditEntry{ID: id("aud"), ActorID: actor, Action: action, TargetType: targetType, TargetID: targetID, Metadata: metadata, CreatedAt: time.Now()})
}

func (a *App) DebugSummary() string {
	s := a.AdminStats()
	return fmt.Sprintf("users=%v conversations=%v messages=%v", s["users"], s["conversations"], s["messages"])
}
