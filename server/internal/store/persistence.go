package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/linli/im/server/internal/model"
	"github.com/redis/go-redis/v9"
)

type Persistence interface {
	Load(context.Context) (*model.State, error)
	Save(context.Context, *model.State) error
	Ping(context.Context) error
	Close()
}
type Normalized interface{ IsNormalized() bool }

// QueryStore exposes bounded, targeted reads used by the production runtime.
// The in-memory implementation deliberately does not implement it.
type QueryStore interface {
	GetUser(context.Context, string) (*model.User, error)
	SearchUsers(context.Context, string, int) ([]*model.User, error)
	SearchUsersByIdentifier(context.Context, string, string, int) ([]*model.User, error)
	ListFriends(context.Context, string) ([]*model.User, error)
	ListBlockedUsers(context.Context, string) ([]*model.User, error)
	ListFriendRequests(context.Context, string) ([]*model.FriendRequest, error)
	ListGroupInvites(context.Context, string, string, int) ([]map[string]any, error)
	ListConversations(context.Context, string, int) ([]map[string]any, error)
	ListMessages(context.Context, string, string, int64, int) ([]*model.Message, error)
	ListForwardMessages(context.Context, string, []string) ([]*model.Message, error)
	ListSync(context.Context, string, int64, int) ([]*model.SyncEvent, int64, bool, error)
	CanAccessConversation(context.Context, string, string) (bool, error)
	ConversationMemberIDs(context.Context, string) ([]string, error)
	ListConversationMembers(context.Context, string, string) ([]*model.ConversationMember, error)
}

// ConversationMemberPageStore keeps large group membership reads bounded.
// The legacy list method remains available for internal compatibility, while
// public APIs should use this cursor-based interface.
type ConversationMemberPageStore interface {
	ListConversationMembersPage(context.Context, string, string, string, int) ([]*model.ConversationMember, string, error)
}

// AdminQueryStore keeps potentially large moderation tables out of the legacy
// whole-state snapshot. Cursor values are opaque to API clients.
type AdminQueryStore interface {
	ListAdminUsers(context.Context, string, string, string, int) ([]*model.User, int64, string, error)
	ListAdminReports(context.Context, string, string, string, int) ([]*model.Report, int64, string, error)
	ListAdminAudits(context.Context, string, string, string, int) ([]*model.AuditEntry, int64, string, error)
	ListAdminMessages(context.Context, string, string, string, int) ([]*model.Message, int64, string, error)
	ListAdminMedia(context.Context, string, string, string, int) ([]*model.Media, int64, string, error)
}
type AdminFriendship struct {
	UserID, FriendUserID, UserName, FriendName string
	CreatedAt, UpdatedAt                       time.Time
}
type AdminFeedback struct {
	ID, UserID, UserName, Category, Content, Contact string
	CreatedAt                                        time.Time
}
type AdminOperationsStore interface {
	ListAdminGroups(context.Context, string, string, string, int) ([]map[string]any, int64, string, error)
	ListAdminFriendships(context.Context, string, string, int) ([]AdminFriendship, int64, string, error)
	ListAdminFeedback(context.Context, string, string, string, int) ([]AdminFeedback, int64, string, error)
	AdminPushStatus(context.Context) (map[string]any, error)
	AdminTaskStatus(context.Context) (map[string]any, error)
	AdminUserOverview(context.Context, string) (map[string]any, error)
	AdminGroupOverview(context.Context, string) (map[string]any, error)
	ListAdminGroupMembers(context.Context, string, string, string, int) ([]*model.ConversationMember, int64, string, error)
	RecordAdminAudit(context.Context, *model.AuditEntry) error
}
type AuthStore interface {
	LoginOrCreateUser(context.Context, string, string, string, time.Time) (*model.User, error)
}
type PasswordAuthStore interface {
	RegisterPasswordUser(context.Context, string, string, string, string, time.Time) (*model.User, error)
	PasswordCredentials(context.Context, string) (*model.User, string, error)
	UpdatePassword(context.Context, string, string, time.Time) error
}
type RefreshSessionStore interface {
	CreateRefreshSession(context.Context, string, string, []byte, time.Time) error
	RotateRefreshSession(context.Context, string, string, []byte, time.Time, string) error
	RevokeRefreshSession(context.Context, string, string) error
	RevokeUserRefreshSessions(context.Context, string) error
}
type AccountStore interface {
	AccountDeleted(context.Context, string) (bool, error)
	DeleteAccount(context.Context, string, time.Time) (bool, error)
}

type Memory struct{}

func (Memory) Load(context.Context) (*model.State, error) { return model.NewState(), nil }
func (Memory) Save(context.Context, *model.State) error   { return nil }
func (Memory) Ping(context.Context) error                 { return nil }
func (Memory) Close()                                     {}

type WithRedis struct {
	base  Persistence
	redis *redis.Client
	id    string
}

func NewWithRedis(base Persistence, url string) (*WithRedis, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	return &WithRedis{base: base, redis: redis.NewClient(opts), id: hex.EncodeToString(b[:])}, nil
}
func (p *WithRedis) Load(ctx context.Context) (*model.State, error) { return p.base.Load(ctx) }
func (p *WithRedis) Save(ctx context.Context, s *model.State) error { return p.base.Save(ctx, s) }
func (p *WithRedis) Ping(ctx context.Context) error {
	if err := p.base.Ping(ctx); err != nil {
		return err
	}
	return p.redis.Ping(ctx).Err()
}
func (p *WithRedis) Close()             { _ = p.redis.Close(); p.base.Close() }
func (p *WithRedis) IsNormalized() bool { n, ok := p.base.(Normalized); return ok && n.IsNormalized() }

func (p *WithRedis) GetUser(ctx context.Context, id string) (*model.User, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.GetUser(ctx, id)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) SearchUsers(ctx context.Context, q string, n int) ([]*model.User, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.SearchUsers(ctx, q, n)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) SearchUsersByIdentifier(ctx context.Context, q, by string, n int) ([]*model.User, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.SearchUsersByIdentifier(ctx, q, by, n)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListFriends(ctx context.Context, uid string) ([]*model.User, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.ListFriends(ctx, uid)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListBlockedUsers(ctx context.Context, uid string) ([]*model.User, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.ListBlockedUsers(ctx, uid)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListFriendRequests(ctx context.Context, uid string) ([]*model.FriendRequest, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.ListFriendRequests(ctx, uid)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListGroupInvites(ctx context.Context, uid, status string, n int) ([]map[string]any, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.ListGroupInvites(ctx, uid, status, n)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListConversations(ctx context.Context, uid string, n int) ([]map[string]any, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.ListConversations(ctx, uid, n)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListMessages(ctx context.Context, uid, cid string, before int64, n int) ([]*model.Message, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.ListMessages(ctx, uid, cid, before, n)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListForwardMessages(ctx context.Context, uid string, ids []string) ([]*model.Message, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.ListForwardMessages(ctx, uid, ids)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListSync(ctx context.Context, uid string, after int64, n int) ([]*model.SyncEvent, int64, bool, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.ListSync(ctx, uid, after, n)
	}
	return nil, 0, false, ErrUnsupported
}
func (p *WithRedis) CanAccessConversation(ctx context.Context, uid, cid string) (bool, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.CanAccessConversation(ctx, uid, cid)
	}
	return false, ErrUnsupported
}
func (p *WithRedis) ConversationMemberIDs(ctx context.Context, cid string) ([]string, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.ConversationMemberIDs(ctx, cid)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListConversationMembers(ctx context.Context, uid, cid string) ([]*model.ConversationMember, error) {
	if s, ok := p.base.(QueryStore); ok {
		return s.ListConversationMembers(ctx, uid, cid)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListAdminUsers(ctx context.Context, q, status, cursor string, limit int) ([]*model.User, int64, string, error) {
	if s, ok := p.base.(AdminQueryStore); ok {
		return s.ListAdminUsers(ctx, q, status, cursor, limit)
	}
	return nil, 0, "", ErrUnsupported
}
func (p *WithRedis) ListAdminReports(ctx context.Context, q, status, cursor string, limit int) ([]*model.Report, int64, string, error) {
	if s, ok := p.base.(AdminQueryStore); ok {
		return s.ListAdminReports(ctx, q, status, cursor, limit)
	}
	return nil, 0, "", ErrUnsupported
}
func (p *WithRedis) ListAdminAudits(ctx context.Context, q, status, cursor string, limit int) ([]*model.AuditEntry, int64, string, error) {
	if s, ok := p.base.(AdminQueryStore); ok {
		return s.ListAdminAudits(ctx, q, status, cursor, limit)
	}
	return nil, 0, "", ErrUnsupported
}
func (p *WithRedis) ListAdminMessages(ctx context.Context, q, messageType, cursor string, limit int) ([]*model.Message, int64, string, error) {
	if s, ok := p.base.(AdminQueryStore); ok {
		return s.ListAdminMessages(ctx, q, messageType, cursor, limit)
	}
	return nil, 0, "", ErrUnsupported
}
func (p *WithRedis) ListAdminMedia(ctx context.Context, q, status, cursor string, limit int) ([]*model.Media, int64, string, error) {
	if s, ok := p.base.(AdminQueryStore); ok {
		return s.ListAdminMedia(ctx, q, status, cursor, limit)
	}
	return nil, 0, "", ErrUnsupported
}
func (p *WithRedis) ListAdminFriendships(ctx context.Context, q, cursor string, limit int) ([]AdminFriendship, int64, string, error) {
	if s, ok := p.base.(AdminOperationsStore); ok {
		return s.ListAdminFriendships(ctx, q, cursor, limit)
	}
	return nil, 0, "", ErrUnsupported
}
func (p *WithRedis) ListAdminGroups(ctx context.Context, q, status, cursor string, limit int) ([]map[string]any, int64, string, error) {
	if s, ok := p.base.(AdminOperationsStore); ok {
		return s.ListAdminGroups(ctx, q, status, cursor, limit)
	}
	return nil, 0, "", ErrUnsupported
}
func (p *WithRedis) ListAdminFeedback(ctx context.Context, q, status, cursor string, limit int) ([]AdminFeedback, int64, string, error) {
	if s, ok := p.base.(AdminOperationsStore); ok {
		return s.ListAdminFeedback(ctx, q, status, cursor, limit)
	}
	return nil, 0, "", ErrUnsupported
}
func (p *WithRedis) AdminPushStatus(ctx context.Context) (map[string]any, error) {
	if s, ok := p.base.(AdminOperationsStore); ok {
		return s.AdminPushStatus(ctx)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) AdminTaskStatus(ctx context.Context) (map[string]any, error) {
	if s, ok := p.base.(AdminOperationsStore); ok {
		return s.AdminTaskStatus(ctx)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) AdminUserOverview(ctx context.Context, id string) (map[string]any, error) {
	if s, ok := p.base.(AdminOperationsStore); ok {
		return s.AdminUserOverview(ctx, id)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) AdminGroupOverview(ctx context.Context, id string) (map[string]any, error) {
	if s, ok := p.base.(AdminOperationsStore); ok {
		return s.AdminGroupOverview(ctx, id)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListAdminGroupMembers(ctx context.Context, id, q, cursor string, limit int) ([]*model.ConversationMember, int64, string, error) {
	if s, ok := p.base.(AdminOperationsStore); ok {
		return s.ListAdminGroupMembers(ctx, id, q, cursor, limit)
	}
	return nil, 0, "", ErrUnsupported
}
func (p *WithRedis) RecordAdminAudit(ctx context.Context, entry *model.AuditEntry) error {
	if s, ok := p.base.(AdminOperationsStore); ok {
		return s.RecordAdminAudit(ctx, entry)
	}
	return ErrUnsupported
}
func (p *WithRedis) LoginOrCreateUser(ctx context.Context, phone, name, id string, created time.Time) (*model.User, error) {
	if s, ok := p.base.(AuthStore); ok {
		return s.LoginOrCreateUser(ctx, phone, name, id, created)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) RegisterPasswordUser(ctx context.Context, phone, name, id, hash string, created time.Time) (*model.User, error) {
	if s, ok := p.base.(PasswordAuthStore); ok {
		return s.RegisterPasswordUser(ctx, phone, name, id, hash, created)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) PasswordCredentials(ctx context.Context, phone string) (*model.User, string, error) {
	if s, ok := p.base.(PasswordAuthStore); ok {
		return s.PasswordCredentials(ctx, phone)
	}
	return nil, "", ErrUnsupported
}
func (p *WithRedis) UpdatePassword(ctx context.Context, phone, hash string, updated time.Time) error {
	if s, ok := p.base.(PasswordAuthStore); ok {
		return s.UpdatePassword(ctx, phone, hash, updated)
	}
	return ErrUnsupported
}
func (p *WithRedis) CreateRefreshSession(ctx context.Context, id, uid string, hash []byte, exp time.Time) error {
	if s, ok := p.base.(RefreshSessionStore); ok {
		return s.CreateRefreshSession(ctx, id, uid, hash, exp)
	}
	return ErrUnsupported
}
func (p *WithRedis) RotateRefreshSession(ctx context.Context, oldID, newID string, hash []byte, exp time.Time, uid string) error {
	if s, ok := p.base.(RefreshSessionStore); ok {
		return s.RotateRefreshSession(ctx, oldID, newID, hash, exp, uid)
	}
	return ErrUnsupported
}
func (p *WithRedis) RevokeRefreshSession(ctx context.Context, id, uid string) error {
	if s, ok := p.base.(RefreshSessionStore); ok {
		return s.RevokeRefreshSession(ctx, id, uid)
	}
	return ErrUnsupported
}
func (p *WithRedis) RevokeUserRefreshSessions(ctx context.Context, uid string) error {
	if s, ok := p.base.(RefreshSessionStore); ok {
		return s.RevokeUserRefreshSessions(ctx, uid)
	}
	return ErrUnsupported
}
func (p *WithRedis) AccountDeleted(ctx context.Context, uid string) (bool, error) {
	if s, ok := p.base.(AccountStore); ok {
		return s.AccountDeleted(ctx, uid)
	}
	return false, ErrUnsupported
}
func (p *WithRedis) DeleteAccount(ctx context.Context, uid string, at time.Time) (bool, error) {
	if s, ok := p.base.(AccountStore); ok {
		return s.DeleteAccount(ctx, uid, at)
	}
	return false, ErrUnsupported
}

// MessageStore is implemented by the normalized PostgreSQL repository. All
// returned sync events and outbox rows are committed atomically with the message.
type MessageStore interface {
	SendMessage(context.Context, MessageInput) (*model.Message, bool, []*model.SyncEvent, error)
}
type MessageCollaborationStore interface {
	EditMessage(context.Context, string, string, string, map[string]any, time.Time, time.Duration) (*model.Message, bool, error)
	ListMessageEdits(context.Context, string, string) ([]*model.MessageEdit, error)
	SetMessageReaction(context.Context, string, string, string, bool, time.Time) (model.MessageReactionSummary, bool, error)
	SetGroupMessagePin(context.Context, string, string, string, bool, time.Time) (*model.MessagePin, bool, error)
	ListGroupMessagePins(context.Context, string, string, int64, int) ([]*model.MessagePin, error)
	SearchConversationMessages(context.Context, string, string, string, int64, int) ([]*model.Message, error)
}
type MessageInput struct {
	UserID, ConversationID, ClientMsgID, Type, ReplyToID string
	Body                                                 map[string]any
	Mentions                                             []string
	MentionAll                                           bool
	MessageID                                            string
	CreatedAt                                            int64
	ExpiresAt                                            *time.Time
}

type ScheduledMessageStore interface {
	CreateScheduledMessage(context.Context, *model.ScheduledMessage) (*model.ScheduledMessage, bool, error)
	UpdateScheduledMessage(context.Context, string, string, ScheduledMessageUpdate, time.Time) (*model.ScheduledMessage, error)
	CancelScheduledMessage(context.Context, string, string, time.Time) (*model.ScheduledMessage, error)
	ListScheduledMessages(context.Context, string, string, int) ([]*model.ScheduledMessage, error)
	LeaseScheduledMessages(context.Context, time.Time, time.Duration, int) ([]*model.ScheduledMessage, error)
	CompleteScheduledMessage(context.Context, string, string, error, time.Time) error
}

type ScheduledMessageUpdate struct {
	Type             *string
	Body             map[string]any
	BodySet          bool
	ReplyToID        *string
	ExpiresInSeconds *int64
	ScheduledAt      *time.Time
}

type MessageExpiryStore interface {
	ExpireMessages(context.Context, time.Time, int) ([]ExpiredMessage, error)
}

type ExpiredMessage struct {
	MessageID, ConversationID string
	ConversationSeq           int64
	ExpiredAt                 time.Time
	MemberIDs                 []string
}

type DeviceStore interface {
	RegisterDevice(context.Context, string, Device) error
	UnregisterDevice(context.Context, string, string) error
}
type Device struct {
	ID                   string `json:"id"`
	Platform             string `json:"platform"`
	PushToken            string `json:"pushToken"`
	Provider             string `json:"provider"`
	NotificationsEnabled bool   `json:"notificationsEnabled"`
	PreviewEnabled       bool   `json:"previewEnabled"`
	SoundEnabled         bool   `json:"soundEnabled"`
	VibrationEnabled     bool   `json:"vibrationEnabled"`
}

type MediaStore interface {
	CreateMedia(context.Context, Media) error
	CompleteMedia(context.Context, string, string, int64, string) error
	GetMedia(context.Context, string) (Media, error)
	CanAccessMedia(context.Context, string, string) (bool, error)
}
type MediaCleanupItem struct {
	ID, ObjectKey string
}
type MediaCleanupStatus struct {
	PendingCandidates int64      `json:"pendingCandidates"`
	OrphanCandidates  int64      `json:"orphanCandidates"`
	Processing        int64      `json:"processing"`
	FailedAttempts    int64      `json:"failedAttempts"`
	LastRunAt         *time.Time `json:"lastRunAt,omitempty"`
}
type MediaCleanupStore interface {
	LeaseMediaCleanup(context.Context, time.Time, time.Duration, time.Duration, time.Duration, int) ([]MediaCleanupItem, error)
	CompleteMediaCleanup(context.Context, string, error, time.Time) error
	MediaCleanupStatus(context.Context, time.Time, time.Duration, time.Duration) (MediaCleanupStatus, error)
}
type ProfileStore interface {
	UpdateUserProfile(context.Context, string, UserProfileUpdate) (*model.User, error)
	UpdateUserPhone(context.Context, string, string) (*model.User, error)
	ListUserDevices(context.Context, string) ([]*model.Device, error)
	ListFavorites(context.Context, string, int) ([]*model.Message, error)
	SetFavorite(context.Context, string, string, bool) error
	CreateFeedback(context.Context, string, string, string, string, string, time.Time) error
}
type FriendStore interface {
	CreateFriendRequest(context.Context, *model.FriendRequest) (*model.FriendRequest, bool, error)
	TransitionFriendRequest(context.Context, string, string, string, time.Time) (*model.FriendRequest, bool, error)
	DeleteFriend(context.Context, string, string, time.Time) error
	UpdateFriendMetadata(context.Context, string, string, FriendMetadata, time.Time) error
	SetFriendBlock(context.Context, string, string, bool, time.Time) error
	ExpireFriendRequests(context.Context, time.Time, int) ([]*model.FriendRequest, error)
}
type GroupStore interface {
	CreateGroupRecord(context.Context, string, string, string, []string, time.Time) (*model.Conversation, error)
	GetGroupProfile(context.Context, string, string) (*model.GroupProfile, error)
	UpdateGroupProfile(context.Context, string, string, GroupProfileUpdate, time.Time) (*model.GroupProfile, error)
	SetGroupAnnouncement(context.Context, string, string, string, time.Time) (*model.GroupProfile, error)
	MarkGroupAnnouncementRead(context.Context, string, string, time.Time) error
	CreateGroupInvite(context.Context, *model.GroupInvite) (*model.GroupInvite, bool, error)
	TransitionGroupInvite(context.Context, string, string, string, time.Time) (*model.GroupInvite, bool, error)
	JoinGroupByQR(context.Context, string, string, time.Time) error
	AddGroupMembers(context.Context, string, string, []string, time.Time) error
	ApplyGroupMemberAction(context.Context, GroupMemberAction) error
	DisbandGroupRecord(context.Context, string, string, string, time.Time) error
}
type GroupProfileUpdate struct {
	Name, AvatarMediaID, JoinPolicy *string
	AllowMemberAddFriend            *bool
	AllMutedUntil                   *time.Time
	RotateQR                        bool
}
type GroupMemberAction struct {
	ActorID, ConversationID, TargetID, Action, Role, Nickname, Reason string
	MutedUntil                                                        *time.Time
	At                                                                time.Time
}
type FriendMetadata struct {
	Remark string   `json:"remark"`
	Tags   []string `json:"tags"`
}
type CallStore interface {
	InviteCall(context.Context, CallInvite) (*model.CallSession, bool, error)
	TransitionCall(context.Context, string, string, string, string, time.Time) (*model.CallSession, bool, error)
	GetCall(context.Context, string, string, time.Time) (*model.CallSession, error)
	ExpireCalls(context.Context, time.Time, int) ([]*model.CallSession, error)
	ListAdminCalls(context.Context, string, string, string, int) ([]*model.CallSession, int64, string, error)
}
type AnnouncementStore interface {
	PromoteDueAnnouncements(context.Context, time.Time) (int, error)
	ListAnnouncements(context.Context, string, time.Time) ([]*model.Announcement, error)
	MarkAnnouncementRead(context.Context, string, string, time.Time) error
	ListAdminAnnouncements(context.Context, string, string, string, int, time.Time) ([]*model.Announcement, int64, string, error)
	CreateAnnouncement(context.Context, AnnouncementInput, time.Time) (*model.Announcement, error)
	UpdateAnnouncement(context.Context, string, AnnouncementInput, time.Time) (*model.Announcement, error)
	PublishAnnouncement(context.Context, string, string, bool, time.Time) (*model.Announcement, error)
	WithdrawAnnouncement(context.Context, string, string, time.Time) (*model.Announcement, error)
	DeleteAnnouncement(context.Context, string, string, time.Time) error
}
type AnnouncementInput struct {
	ID            string
	Title         string
	Content       string
	Status        string
	Pinned        bool
	TargetType    string
	TargetUserIDs []string
	ScheduledAt   *time.Time
	PushOnPublish bool
	ActorID       string
}
type CallInvite struct {
	ID, ConversationID, CallerID, CalleeID, MediaType string
	InvitedAt, ExpiresAt                              time.Time
}
type UserProfileUpdate struct {
	Name          *string `json:"name,omitempty"`
	Handle        *string `json:"handle,omitempty"`
	Signature     *string `json:"signature,omitempty"`
	AvatarMediaID *string `json:"avatarMediaId,omitempty"`
}

func (p *WithRedis) InviteCall(ctx context.Context, in CallInvite) (*model.CallSession, bool, error) {
	if s, ok := p.base.(CallStore); ok {
		return s.InviteCall(ctx, in)
	}
	return nil, false, ErrUnsupported
}
func (p *WithRedis) TransitionCall(ctx context.Context, id, uid, action, reason string, at time.Time) (*model.CallSession, bool, error) {
	if s, ok := p.base.(CallStore); ok {
		return s.TransitionCall(ctx, id, uid, action, reason, at)
	}
	return nil, false, ErrUnsupported
}
func (p *WithRedis) GetCall(ctx context.Context, id, uid string, at time.Time) (*model.CallSession, error) {
	if s, ok := p.base.(CallStore); ok {
		return s.GetCall(ctx, id, uid, at)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ExpireCalls(ctx context.Context, at time.Time, limit int) ([]*model.CallSession, error) {
	if s, ok := p.base.(CallStore); ok {
		return s.ExpireCalls(ctx, at, limit)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListAdminCalls(ctx context.Context, q, status, cursor string, limit int) ([]*model.CallSession, int64, string, error) {
	if s, ok := p.base.(CallStore); ok {
		return s.ListAdminCalls(ctx, q, status, cursor, limit)
	}
	return nil, 0, "", ErrUnsupported
}
func (p *WithRedis) ListAnnouncements(ctx context.Context, uid string, now time.Time) ([]*model.Announcement, error) {
	if s, ok := p.base.(AnnouncementStore); ok {
		return s.ListAnnouncements(ctx, uid, now)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) PromoteDueAnnouncements(ctx context.Context, now time.Time) (int, error) {
	if s, ok := p.base.(AnnouncementStore); ok {
		return s.PromoteDueAnnouncements(ctx, now)
	}
	return 0, ErrUnsupported
}
func (p *WithRedis) MarkAnnouncementRead(ctx context.Context, uid, id string, at time.Time) error {
	if s, ok := p.base.(AnnouncementStore); ok {
		return s.MarkAnnouncementRead(ctx, uid, id, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) ListAdminAnnouncements(ctx context.Context, q, status, cursor string, limit int, now time.Time) ([]*model.Announcement, int64, string, error) {
	if s, ok := p.base.(AnnouncementStore); ok {
		return s.ListAdminAnnouncements(ctx, q, status, cursor, limit, now)
	}
	return nil, 0, "", ErrUnsupported
}
func (p *WithRedis) CreateAnnouncement(ctx context.Context, input AnnouncementInput, at time.Time) (*model.Announcement, error) {
	if s, ok := p.base.(AnnouncementStore); ok {
		return s.CreateAnnouncement(ctx, input, at)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) UpdateAnnouncement(ctx context.Context, id string, input AnnouncementInput, at time.Time) (*model.Announcement, error) {
	if s, ok := p.base.(AnnouncementStore); ok {
		return s.UpdateAnnouncement(ctx, id, input, at)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) PublishAnnouncement(ctx context.Context, id, actor string, push bool, at time.Time) (*model.Announcement, error) {
	if s, ok := p.base.(AnnouncementStore); ok {
		return s.PublishAnnouncement(ctx, id, actor, push, at)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) WithdrawAnnouncement(ctx context.Context, id, actor string, at time.Time) (*model.Announcement, error) {
	if s, ok := p.base.(AnnouncementStore); ok {
		return s.WithdrawAnnouncement(ctx, id, actor, at)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) DeleteAnnouncement(ctx context.Context, id, actor string, at time.Time) error {
	if s, ok := p.base.(AnnouncementStore); ok {
		return s.DeleteAnnouncement(ctx, id, actor, at)
	}
	return ErrUnsupported
}

type Media struct {
	ID        string `json:"id"`
	OwnerID   string `json:"ownerId"`
	ObjectKey string `json:"objectKey"`
	MIME      string `json:"mime"`
	Status    string `json:"status"`
	Checksum  string `json:"checksum,omitempty"`
	Size      int64  `json:"size"`
}
type OutboxItem struct {
	ID        int64          `json:"id"`
	UserID    string         `json:"userId"`
	EventType string         `json:"eventType"`
	Payload   map[string]any `json:"payload"`
	Devices   []Device       `json:"devices"`
	Attempts  int            `json:"attempts"`
}
type OutboxStore interface {
	ClaimPush(context.Context, int) ([]OutboxItem, error)
	CompletePush(context.Context, int64, error) error
}

func (p *WithRedis) ListConversationMembersPage(ctx context.Context, uid, cid, cursor string, n int) ([]*model.ConversationMember, string, error) {
	if s, ok := p.base.(ConversationMemberPageStore); ok {
		return s.ListConversationMembersPage(ctx, uid, cid, cursor, n)
	}
	return nil, "", ErrUnsupported
}

// MessageFanoutStore expands one durable message into per-user sync and push
// rows outside the latency-sensitive message transaction.
type MessageFanoutStore interface {
	ProcessMessageFanout(context.Context, int) (int, bool, error)
}

type RetentionPolicy struct {
	SyncEvents time.Duration
	Outbox     time.Duration
}

type RuntimeMaintenanceStore interface {
	CleanupRuntimeData(context.Context, RetentionPolicy, int) (int64, error)
}

type RuntimeStats struct {
	DBMaxConnections, DBTotalConnections, DBIdleConnections, DBAcquiredConnections int32
	DBAcquireCount, DBEmptyAcquireCount, DBCanceledAcquireCount                    int64
	DBAcquireDurationSeconds                                                       float64
	RedisTotalConnections, RedisIdleConnections, RedisTimeouts                     uint32
	PushPending, EventPending, FanoutPending                                       int64
	OldestPushSeconds, OldestEventSeconds, OldestFanoutSeconds                     float64
}

type RuntimeStatsStore interface {
	RuntimeStats(context.Context) (RuntimeStats, error)
}

// PushDeviceInvalidator 按设备 ID 停用推送服务商已确认失效的 token，避免后续持续投递。
type PushDeviceInvalidator interface {
	InvalidatePushDevices(context.Context, []string) error
}
type EventSubscriber interface {
	RunEvents(context.Context, func([]string, string, map[string]any)) error
}
type EphemeralBus interface {
	PublishEphemeral(context.Context, []string, string, map[string]any) error
	RunEphemeral(context.Context, func([]string, string, map[string]any)) error
}

// RateLimiterStore provides one atomic rate-limit budget shared by every IM
// node. Implementations must not expose the original phone number or IP in the
// storage key.
type RateLimiterStore interface {
	AllowRate(context.Context, string, int, time.Duration) (bool, error)
}

// PolicyStore keeps mutable moderation rules and runtime settings safe for
// multi-node deployments without saving a stale whole-process state snapshot.
type PolicyStore interface {
	ListSensitiveWords(context.Context) (map[string]string, error)
	CreateSensitiveWord(context.Context, string, string, string, time.Time) error
	DeleteSensitiveWordRecord(context.Context, string, string, time.Time) error
	RuntimeSettings(context.Context) (map[string]any, error)
	UpdateRuntimeSettings(context.Context, string, map[string]any, time.Time) error
}
type ephemeralEvent struct {
	Origin  string         `json:"origin"`
	Users   []string       `json:"users"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

func (p *WithRedis) PublishEphemeral(ctx context.Context, users []string, typ string, payload map[string]any) error {
	raw, err := json.Marshal(ephemeralEvent{Origin: p.id, Users: users, Type: typ, Payload: payload})
	if err != nil {
		return err
	}
	return p.redis.Publish(ctx, "im:ephemeral", raw).Err()
}
func (p *WithRedis) RunEphemeral(ctx context.Context, deliver func([]string, string, map[string]any)) error {
	sub := p.redis.Subscribe(ctx, "im:ephemeral")
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		return err
	}
	for {
		msg, err := sub.ReceiveMessage(ctx)
		if err != nil {
			return err
		}
		var e ephemeralEvent
		if json.Unmarshal([]byte(msg.Payload), &e) == nil && e.Origin != p.id {
			deliver(e.Users, e.Type, e.Payload)
		}
	}
}

var allowRateScript = redis.NewScript(`
local value = redis.call('INCR', KEYS[1])
if value == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return value <= tonumber(ARGV[1])
`)

func (p *WithRedis) AllowRate(ctx context.Context, key string, max int, window time.Duration) (bool, error) {
	digest := sha256.Sum256([]byte(key))
	result, err := allowRateScript.Run(ctx, p.redis, []string{"im:rate:" + hex.EncodeToString(digest[:])}, max, window.Milliseconds()).Bool()
	return result, err
}

func (p *WithRedis) ListSensitiveWords(ctx context.Context) (map[string]string, error) {
	if s, ok := p.base.(PolicyStore); ok {
		return s.ListSensitiveWords(ctx)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) CreateSensitiveWord(ctx context.Context, id, value, actor string, at time.Time) error {
	if s, ok := p.base.(PolicyStore); ok {
		return s.CreateSensitiveWord(ctx, id, value, actor, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) DeleteSensitiveWordRecord(ctx context.Context, id, actor string, at time.Time) error {
	if s, ok := p.base.(PolicyStore); ok {
		return s.DeleteSensitiveWordRecord(ctx, id, actor, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) RuntimeSettings(ctx context.Context) (map[string]any, error) {
	if s, ok := p.base.(PolicyStore); ok {
		return s.RuntimeSettings(ctx)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) UpdateRuntimeSettings(ctx context.Context, actor string, settings map[string]any, at time.Time) error {
	if s, ok := p.base.(PolicyStore); ok {
		return s.UpdateRuntimeSettings(ctx, actor, settings, at)
	}
	return ErrUnsupported
}

type MutationStore interface {
	SetBlock(context.Context, string, string, bool) error
	RemoveMember(context.Context, string, string) error
	DeleteConversation(context.Context, string) error
	DeleteSensitiveWord(context.Context, string) error
	RecallMessage(context.Context, string, time.Time) error
}
type RuntimeMutationStore interface {
	MarkRead(context.Context, string, string, int64, time.Time) (int64, []string, error)
	MarkDelivered(context.Context, string, string, int64, time.Time) (int64, []string, error)
	UpdateConversationPreferences(context.Context, string, string, ConversationPreferences) error
	HideConversation(context.Context, string, string) error
	RecallAuthorized(context.Context, string, string, time.Time, time.Duration) (string, int64, []string, error)
	CreateReportRecord(context.Context, *model.Report, *model.AuditEntry) error
	SetUserBanRecord(context.Context, string, string, bool, *time.Time, string, string, time.Time) error
	ResolveReportRecord(context.Context, string, string, string, string, string, time.Time) (string, error)
}
type BanExpiryStore interface {
	ExpireUserBans(context.Context, time.Time, int) ([]string, error)
}

type ConversationPreferences struct {
	Pinned             *bool `json:"pinned,omitempty"`
	Archived           *bool `json:"archived,omitempty"`
	NotificationsMuted *bool `json:"notificationsMuted,omitempty"`
	ManualUnread       *bool `json:"manualUnread,omitempty"`
}

type WithRedisMessage struct{ *WithRedis }

func (p *WithRedis) SendMessage(ctx context.Context, in MessageInput) (*model.Message, bool, []*model.SyncEvent, error) {
	if m, ok := p.base.(MessageStore); ok {
		return m.SendMessage(ctx, in)
	}
	return nil, false, nil, ErrUnsupported
}
func (p *WithRedis) EditMessage(ctx context.Context, uid, mid, editID string, body map[string]any, at time.Time, window time.Duration) (*model.Message, bool, error) {
	if s, ok := p.base.(MessageCollaborationStore); ok {
		return s.EditMessage(ctx, uid, mid, editID, body, at, window)
	}
	return nil, false, ErrUnsupported
}
func (p *WithRedis) ListMessageEdits(ctx context.Context, uid, mid string) ([]*model.MessageEdit, error) {
	if s, ok := p.base.(MessageCollaborationStore); ok {
		return s.ListMessageEdits(ctx, uid, mid)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) SetMessageReaction(ctx context.Context, uid, mid, emoji string, add bool, at time.Time) (model.MessageReactionSummary, bool, error) {
	if s, ok := p.base.(MessageCollaborationStore); ok {
		return s.SetMessageReaction(ctx, uid, mid, emoji, add, at)
	}
	return model.MessageReactionSummary{}, false, ErrUnsupported
}
func (p *WithRedis) SetGroupMessagePin(ctx context.Context, uid, cid, mid string, pin bool, at time.Time) (*model.MessagePin, bool, error) {
	if s, ok := p.base.(MessageCollaborationStore); ok {
		return s.SetGroupMessagePin(ctx, uid, cid, mid, pin, at)
	}
	return nil, false, ErrUnsupported
}
func (p *WithRedis) ListGroupMessagePins(ctx context.Context, uid, cid string, before int64, limit int) ([]*model.MessagePin, error) {
	if s, ok := p.base.(MessageCollaborationStore); ok {
		return s.ListGroupMessagePins(ctx, uid, cid, before, limit)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) SearchConversationMessages(ctx context.Context, uid, cid, query string, before int64, limit int) ([]*model.Message, error) {
	if s, ok := p.base.(MessageCollaborationStore); ok {
		return s.SearchConversationMessages(ctx, uid, cid, query, before, limit)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) RegisterDevice(ctx context.Context, uid string, d Device) error {
	if s, ok := p.base.(DeviceStore); ok {
		return s.RegisterDevice(ctx, uid, d)
	}
	return ErrUnsupported
}
func (p *WithRedis) UnregisterDevice(ctx context.Context, uid, id string) error {
	if s, ok := p.base.(DeviceStore); ok {
		return s.UnregisterDevice(ctx, uid, id)
	}
	return ErrUnsupported
}
func (p *WithRedis) CreateMedia(ctx context.Context, m Media) error {
	if s, ok := p.base.(MediaStore); ok {
		return s.CreateMedia(ctx, m)
	}
	return ErrUnsupported
}
func (p *WithRedis) CompleteMedia(ctx context.Context, id, uid string, size int64, sum string) error {
	if s, ok := p.base.(MediaStore); ok {
		return s.CompleteMedia(ctx, id, uid, size, sum)
	}
	return ErrUnsupported
}
func (p *WithRedis) LeaseMediaCleanup(ctx context.Context, now time.Time, pendingAge, orphanAge, lease time.Duration, limit int) ([]MediaCleanupItem, error) {
	if s, ok := p.base.(MediaCleanupStore); ok {
		return s.LeaseMediaCleanup(ctx, now, pendingAge, orphanAge, lease, limit)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) CompleteMediaCleanup(ctx context.Context, id string, cleanupErr error, at time.Time) error {
	if s, ok := p.base.(MediaCleanupStore); ok {
		return s.CompleteMediaCleanup(ctx, id, cleanupErr, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) MediaCleanupStatus(ctx context.Context, now time.Time, pendingAge, orphanAge time.Duration) (MediaCleanupStatus, error) {
	if s, ok := p.base.(MediaCleanupStore); ok {
		return s.MediaCleanupStatus(ctx, now, pendingAge, orphanAge)
	}
	return MediaCleanupStatus{}, ErrUnsupported
}
func (p *WithRedis) GetMedia(ctx context.Context, id string) (Media, error) {
	if s, ok := p.base.(MediaStore); ok {
		return s.GetMedia(ctx, id)
	}
	return Media{}, ErrUnsupported
}
func (p *WithRedis) CanAccessMedia(ctx context.Context, uid, id string) (bool, error) {
	if s, ok := p.base.(MediaStore); ok {
		return s.CanAccessMedia(ctx, uid, id)
	}
	return false, ErrUnsupported
}
func (p *WithRedis) UpdateUserProfile(ctx context.Context, uid string, update UserProfileUpdate) (*model.User, error) {
	if s, ok := p.base.(ProfileStore); ok {
		return s.UpdateUserProfile(ctx, uid, update)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) UpdateUserPhone(ctx context.Context, uid, phone string) (*model.User, error) {
	if s, ok := p.base.(ProfileStore); ok {
		return s.UpdateUserPhone(ctx, uid, phone)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListUserDevices(ctx context.Context, uid string) ([]*model.Device, error) {
	if s, ok := p.base.(ProfileStore); ok {
		return s.ListUserDevices(ctx, uid)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListFavorites(ctx context.Context, uid string, limit int) ([]*model.Message, error) {
	if s, ok := p.base.(ProfileStore); ok {
		return s.ListFavorites(ctx, uid, limit)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) SetFavorite(ctx context.Context, uid, messageID string, enabled bool) error {
	if s, ok := p.base.(ProfileStore); ok {
		return s.SetFavorite(ctx, uid, messageID, enabled)
	}
	return ErrUnsupported
}
func (p *WithRedis) CreateFeedback(ctx context.Context, id, uid, category, content, contact string, created time.Time) error {
	if s, ok := p.base.(ProfileStore); ok {
		return s.CreateFeedback(ctx, id, uid, category, content, contact, created)
	}
	return ErrUnsupported
}
func (p *WithRedis) CreateFriendRequest(ctx context.Context, request *model.FriendRequest) (*model.FriendRequest, bool, error) {
	if s, ok := p.base.(FriendStore); ok {
		return s.CreateFriendRequest(ctx, request)
	}
	return nil, false, ErrUnsupported
}
func (p *WithRedis) TransitionFriendRequest(ctx context.Context, id, uid, action string, at time.Time) (*model.FriendRequest, bool, error) {
	if s, ok := p.base.(FriendStore); ok {
		return s.TransitionFriendRequest(ctx, id, uid, action, at)
	}
	return nil, false, ErrUnsupported
}
func (p *WithRedis) DeleteFriend(ctx context.Context, uid, target string, at time.Time) error {
	if s, ok := p.base.(FriendStore); ok {
		return s.DeleteFriend(ctx, uid, target, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) UpdateFriendMetadata(ctx context.Context, uid, target string, metadata FriendMetadata, at time.Time) error {
	if s, ok := p.base.(FriendStore); ok {
		return s.UpdateFriendMetadata(ctx, uid, target, metadata, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) SetFriendBlock(ctx context.Context, uid, target string, blocked bool, at time.Time) error {
	if s, ok := p.base.(FriendStore); ok {
		return s.SetFriendBlock(ctx, uid, target, blocked, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) ExpireFriendRequests(ctx context.Context, at time.Time, limit int) ([]*model.FriendRequest, error) {
	if s, ok := p.base.(FriendStore); ok {
		return s.ExpireFriendRequests(ctx, at, limit)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) CreateGroupRecord(ctx context.Context, id, owner, name string, members []string, at time.Time) (*model.Conversation, error) {
	if s, ok := p.base.(GroupStore); ok {
		return s.CreateGroupRecord(ctx, id, owner, name, members, at)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) GetGroupProfile(ctx context.Context, uid, cid string) (*model.GroupProfile, error) {
	if s, ok := p.base.(GroupStore); ok {
		return s.GetGroupProfile(ctx, uid, cid)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) UpdateGroupProfile(ctx context.Context, actor, cid string, u GroupProfileUpdate, at time.Time) (*model.GroupProfile, error) {
	if s, ok := p.base.(GroupStore); ok {
		return s.UpdateGroupProfile(ctx, actor, cid, u, at)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) SetGroupAnnouncement(ctx context.Context, actor, cid, content string, at time.Time) (*model.GroupProfile, error) {
	if s, ok := p.base.(GroupStore); ok {
		return s.SetGroupAnnouncement(ctx, actor, cid, content, at)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) MarkGroupAnnouncementRead(ctx context.Context, uid, cid string, at time.Time) error {
	if s, ok := p.base.(GroupStore); ok {
		return s.MarkGroupAnnouncementRead(ctx, uid, cid, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) CreateGroupInvite(ctx context.Context, i *model.GroupInvite) (*model.GroupInvite, bool, error) {
	if s, ok := p.base.(GroupStore); ok {
		return s.CreateGroupInvite(ctx, i)
	}
	return nil, false, ErrUnsupported
}
func (p *WithRedis) TransitionGroupInvite(ctx context.Context, id, uid, action string, at time.Time) (*model.GroupInvite, bool, error) {
	if s, ok := p.base.(GroupStore); ok {
		return s.TransitionGroupInvite(ctx, id, uid, action, at)
	}
	return nil, false, ErrUnsupported
}
func (p *WithRedis) JoinGroupByQR(ctx context.Context, uid, token string, at time.Time) error {
	if s, ok := p.base.(GroupStore); ok {
		return s.JoinGroupByQR(ctx, uid, token, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) AddGroupMembers(ctx context.Context, actor, cid string, ids []string, at time.Time) error {
	if s, ok := p.base.(GroupStore); ok {
		return s.AddGroupMembers(ctx, actor, cid, ids, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) ApplyGroupMemberAction(ctx context.Context, a GroupMemberAction) error {
	if s, ok := p.base.(GroupStore); ok {
		return s.ApplyGroupMemberAction(ctx, a)
	}
	return ErrUnsupported
}
func (p *WithRedis) DisbandGroupRecord(ctx context.Context, actor, cid, reason string, at time.Time) error {
	if s, ok := p.base.(GroupStore); ok {
		return s.DisbandGroupRecord(ctx, actor, cid, reason, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) ClaimPush(ctx context.Context, n int) ([]OutboxItem, error) {
	if s, ok := p.base.(OutboxStore); ok {
		return s.ClaimPush(ctx, n)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) CompletePush(ctx context.Context, id int64, sendErr error) error {
	if s, ok := p.base.(OutboxStore); ok {
		return s.CompletePush(ctx, id, sendErr)
	}
	return ErrUnsupported
}
func (p *WithRedis) InvalidatePushDevices(ctx context.Context, ids []string) error {
	if s, ok := p.base.(PushDeviceInvalidator); ok {
		return s.InvalidatePushDevices(ctx, ids)
	}
	return ErrUnsupported
}

func (p *WithRedis) ProcessMessageFanout(ctx context.Context, batch int) (int, bool, error) {
	if s, ok := p.base.(MessageFanoutStore); ok {
		return s.ProcessMessageFanout(ctx, batch)
	}
	return 0, false, ErrUnsupported
}

func (p *WithRedis) CleanupRuntimeData(ctx context.Context, policy RetentionPolicy, batch int) (int64, error) {
	if s, ok := p.base.(RuntimeMaintenanceStore); ok {
		return s.CleanupRuntimeData(ctx, policy, batch)
	}
	return 0, ErrUnsupported
}

func (p *WithRedis) RuntimeStats(ctx context.Context) (RuntimeStats, error) {
	var stats RuntimeStats
	if s, ok := p.base.(RuntimeStatsStore); ok {
		var err error
		stats, err = s.RuntimeStats(ctx)
		if err != nil {
			return stats, err
		}
	}
	redisStats := p.redis.PoolStats()
	stats.RedisTotalConnections = redisStats.TotalConns
	stats.RedisIdleConnections = redisStats.IdleConns
	stats.RedisTimeouts = redisStats.Timeouts
	return stats, nil
}
func (p *WithRedis) RunEvents(ctx context.Context, deliver func([]string, string, map[string]any)) error {
	if s, ok := p.base.(EventSubscriber); ok {
		return s.RunEvents(ctx, deliver)
	}
	return ErrUnsupported
}
func (p *WithRedis) SetBlock(ctx context.Context, a, b string, v bool) error {
	if s, ok := p.base.(MutationStore); ok {
		return s.SetBlock(ctx, a, b, v)
	}
	return ErrUnsupported
}
func (p *WithRedis) RemoveMember(ctx context.Context, c, u string) error {
	if s, ok := p.base.(MutationStore); ok {
		return s.RemoveMember(ctx, c, u)
	}
	return ErrUnsupported
}
func (p *WithRedis) DeleteConversation(ctx context.Context, c string) error {
	if s, ok := p.base.(MutationStore); ok {
		return s.DeleteConversation(ctx, c)
	}
	return ErrUnsupported
}
func (p *WithRedis) DeleteSensitiveWord(ctx context.Context, id string) error {
	if s, ok := p.base.(MutationStore); ok {
		return s.DeleteSensitiveWord(ctx, id)
	}
	return ErrUnsupported
}
func (p *WithRedis) RecallMessage(ctx context.Context, id string, t time.Time) error {
	if s, ok := p.base.(MutationStore); ok {
		return s.RecallMessage(ctx, id, t)
	}
	return ErrUnsupported
}
func (p *WithRedis) MarkRead(ctx context.Context, u, c string, s int64, t time.Time) (int64, []string, error) {
	if x, ok := p.base.(RuntimeMutationStore); ok {
		return x.MarkRead(ctx, u, c, s, t)
	}
	return 0, nil, ErrUnsupported
}
func (p *WithRedis) MarkDelivered(ctx context.Context, u, c string, s int64, t time.Time) (int64, []string, error) {
	if x, ok := p.base.(RuntimeMutationStore); ok {
		return x.MarkDelivered(ctx, u, c, s, t)
	}
	return 0, nil, ErrUnsupported
}
func (p *WithRedis) UpdateConversationPreferences(ctx context.Context, uid, cid string, preferences ConversationPreferences) error {
	if x, ok := p.base.(RuntimeMutationStore); ok {
		return x.UpdateConversationPreferences(ctx, uid, cid, preferences)
	}
	return ErrUnsupported
}

func (p *WithRedis) CreateScheduledMessage(ctx context.Context, item *model.ScheduledMessage) (*model.ScheduledMessage, bool, error) {
	if x, ok := p.base.(ScheduledMessageStore); ok {
		return x.CreateScheduledMessage(ctx, item)
	}
	return nil, false, ErrUnsupported
}
func (p *WithRedis) UpdateScheduledMessage(ctx context.Context, uid, id string, update ScheduledMessageUpdate, at time.Time) (*model.ScheduledMessage, error) {
	if x, ok := p.base.(ScheduledMessageStore); ok {
		return x.UpdateScheduledMessage(ctx, uid, id, update, at)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) CancelScheduledMessage(ctx context.Context, uid, id string, at time.Time) (*model.ScheduledMessage, error) {
	if x, ok := p.base.(ScheduledMessageStore); ok {
		return x.CancelScheduledMessage(ctx, uid, id, at)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ListScheduledMessages(ctx context.Context, uid, status string, limit int) ([]*model.ScheduledMessage, error) {
	if x, ok := p.base.(ScheduledMessageStore); ok {
		return x.ListScheduledMessages(ctx, uid, status, limit)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) LeaseScheduledMessages(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]*model.ScheduledMessage, error) {
	if x, ok := p.base.(ScheduledMessageStore); ok {
		return x.LeaseScheduledMessages(ctx, now, lease, limit)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) CompleteScheduledMessage(ctx context.Context, id, messageID string, sendErr error, at time.Time) error {
	if x, ok := p.base.(ScheduledMessageStore); ok {
		return x.CompleteScheduledMessage(ctx, id, messageID, sendErr, at)
	}
	return ErrUnsupported
}
func (p *WithRedis) ExpireMessages(ctx context.Context, at time.Time, limit int) ([]ExpiredMessage, error) {
	if x, ok := p.base.(MessageExpiryStore); ok {
		return x.ExpireMessages(ctx, at, limit)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) HideConversation(ctx context.Context, uid, cid string) error {
	if x, ok := p.base.(RuntimeMutationStore); ok {
		return x.HideConversation(ctx, uid, cid)
	}
	return ErrUnsupported
}
func (p *WithRedis) RecallAuthorized(ctx context.Context, u, m string, t time.Time, window time.Duration) (string, int64, []string, error) {
	if x, ok := p.base.(RuntimeMutationStore); ok {
		return x.RecallAuthorized(ctx, u, m, t, window)
	}
	return "", 0, nil, ErrUnsupported
}
func (p *WithRedis) CreateReportRecord(ctx context.Context, r *model.Report, a *model.AuditEntry) error {
	if x, ok := p.base.(RuntimeMutationStore); ok {
		return x.CreateReportRecord(ctx, r, a)
	}
	return ErrUnsupported
}
func (p *WithRedis) SetUserBanRecord(ctx context.Context, actor, uid string, b bool, until *time.Time, reason, auditID string, t time.Time) error {
	if x, ok := p.base.(RuntimeMutationStore); ok {
		return x.SetUserBanRecord(ctx, actor, uid, b, until, reason, auditID, t)
	}
	return ErrUnsupported
}
func (p *WithRedis) ExpireUserBans(ctx context.Context, at time.Time, limit int) ([]string, error) {
	if x, ok := p.base.(BanExpiryStore); ok {
		return x.ExpireUserBans(ctx, at, limit)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) ResolveReportRecord(ctx context.Context, actor, rid, action, note, auditID string, t time.Time) (string, error) {
	if x, ok := p.base.(RuntimeMutationStore); ok {
		return x.ResolveReportRecord(ctx, actor, rid, action, note, auditID, t)
	}
	return "", ErrUnsupported
}
