package model

import "time"

type User struct {
	ID                     string     `json:"id"`
	Phone                  string     `json:"phone,omitempty"`
	Name                   string     `json:"name"`
	Handle                 string     `json:"handle"`
	HandleChangeCount      int        `json:"handleChangeCount"`
	HandleChangesRemaining int        `json:"handleChangesRemaining"`
	AllowSearchByHandle    bool       `json:"allowSearchByHandle"`
	AllowSearchByPhone     bool       `json:"allowSearchByPhone"`
	Signature              string     `json:"signature"`
	AvatarMediaID          string     `json:"avatarMediaId,omitempty"`
	AvatarURL              string     `json:"avatarUrl,omitempty"`
	Banned                 bool       `json:"banned"`
	BannedUntil            *time.Time `json:"bannedUntil,omitempty"`
	Remark                 string     `json:"remark,omitempty"`
	Tags                   []string   `json:"tags,omitempty"`
	CreatedAt              time.Time  `json:"createdAt"`
	DeletedAt              *time.Time `json:"deletedAt,omitempty"`
}

type FriendRequest struct {
	ID         string     `json:"id"`
	FromUserID string     `json:"fromUserId"`
	ToUserID   string     `json:"toUserId"`
	Message    string     `json:"message"`
	Source     string     `json:"source"`
	SourceID   string     `json:"sourceId,omitempty"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

type Conversation struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	Title          string    `json:"title,omitempty"`
	AvatarURL      string    `json:"avatarUrl,omitempty"`
	Seq            int64     `json:"seq"`
	LastMessageSeq int64     `json:"lastMessageSeq"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type ConversationMember struct {
	ConversationID     string     `json:"conversationId"`
	ID                 string     `json:"id,omitempty"`
	UserID             string     `json:"userId"`
	Name               string     `json:"name"`
	Handle             string     `json:"handle"`
	AvatarURL          string     `json:"avatarUrl,omitempty"`
	Role               string     `json:"role"`
	MutedUntil         *time.Time `json:"mutedUntil,omitempty"`
	LastReadSeq        int64      `json:"lastReadSeq"`
	LastDeliveredSeq   int64      `json:"lastDeliveredSeq"`
	Pinned             bool       `json:"pinned"`
	Archived           bool       `json:"archived"`
	NotificationsMuted bool       `json:"notificationsMuted"`
	ManualUnread       bool       `json:"manualUnread"`
	GroupNickname      string     `json:"groupNickname,omitempty"`
	HiddenUntilSeq     *int64     `json:"hiddenUntilSeq,omitempty"`
	JoinedAt           time.Time  `json:"joinedAt"`
}

type GroupProfile struct {
	ConversationID       string     `json:"conversationId"`
	OwnerID              string     `json:"ownerId"`
	Name                 string     `json:"name"`
	AvatarURL            string     `json:"avatarUrl,omitempty"`
	Announcement         string     `json:"announcement"`
	AnnouncementVersion  int64      `json:"announcementVersion"`
	AnnouncementReadAt   *time.Time `json:"announcementReadAt,omitempty"`
	JoinPolicy           string     `json:"joinPolicy"`
	AllowMemberAddFriend bool       `json:"allowMemberAddFriend"`
	AllMutedUntil        *time.Time `json:"allMutedUntil,omitempty"`
	QRToken              string     `json:"qrToken,omitempty"`
	QRExpiresAt          *time.Time `json:"qrExpiresAt,omitempty"`
	DissolvedAt          *time.Time `json:"dissolvedAt,omitempty"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

type GroupInvite struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversationId"`
	InviterID      string     `json:"inviterId"`
	InviteeID      string     `json:"inviteeId"`
	Source         string     `json:"source"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"createdAt"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`
}

type Message struct {
	ID             string                   `json:"id"`
	ClientMsgID    string                   `json:"clientMsgId"`
	ConversationID string                   `json:"conversationId"`
	SenderID       string                   `json:"senderId"`
	Seq            int64                    `json:"conversationSeq"`
	Type           string                   `json:"type"`
	Body           map[string]any           `json:"body"`
	ReplyToID      string                   `json:"replyToId,omitempty"`
	RecalledAt     *time.Time               `json:"recalledAt,omitempty"`
	ExpiresAt      *time.Time               `json:"expiresAt,omitempty"`
	ExpiredAt      *time.Time               `json:"expiredAt,omitempty"`
	EditedAt       *time.Time               `json:"editedAt,omitempty"`
	EditVersion    int                      `json:"editVersion"`
	Reactions      []MessageReactionSummary `json:"reactions,omitempty"`
	CreatedAt      time.Time                `json:"createdAt"`
}

type ScheduledMessage struct {
	ID               string         `json:"id"`
	UserID           string         `json:"userId"`
	ConversationID   string         `json:"conversationId"`
	ClientMsgID      string         `json:"clientMsgId"`
	Type             string         `json:"type"`
	Body             map[string]any `json:"body"`
	ReplyToID        string         `json:"replyToId,omitempty"`
	ExpiresInSeconds int64          `json:"expiresInSeconds,omitempty"`
	ScheduledAt      time.Time      `json:"scheduledAt"`
	Status           string         `json:"status"`
	Attempts         int            `json:"attempts"`
	LastError        string         `json:"lastError,omitempty"`
	SentMessageID    string         `json:"sentMessageId,omitempty"`
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
}

type MessageReactionSummary struct {
	Emoji       string `json:"emoji"`
	Count       int    `json:"count"`
	ReactedByMe bool   `json:"reactedByMe"`
}

type MessageEdit struct {
	MessageID string         `json:"messageId"`
	Version   int            `json:"version"`
	EditID    string         `json:"-"`
	EditorID  string         `json:"editorId"`
	Body      map[string]any `json:"body"`
	EditedAt  time.Time      `json:"editedAt"`
}

type MessagePin struct {
	ConversationID string    `json:"conversationId"`
	Message        *Message  `json:"message"`
	PinnedBy       string    `json:"pinnedBy"`
	PinnedAt       time.Time `json:"pinnedAt"`
}

type CallSession struct {
	ID              string     `json:"id"`
	ConversationID  string     `json:"conversationId"`
	Kind            string     `json:"kind"`
	CallerID        string     `json:"callerId"`
	CalleeID        string     `json:"calleeId,omitempty"`
	ParticipantIDs  []string   `json:"participantIds"`
	JoinedUserIDs   []string   `json:"joinedUserIds"`
	DeclinedUserIDs []string   `json:"declinedUserIds"`
	LeftUserIDs     []string   `json:"leftUserIds"`
	MediaType       string     `json:"mediaType"`
	Status          string     `json:"status"`
	EndReason       string     `json:"endReason,omitempty"`
	EndedBy         string     `json:"endedBy,omitempty"`
	InvitedAt       time.Time  `json:"invitedAt"`
	ExpiresAt       time.Time  `json:"expiresAt"`
	AcceptedAt      *time.Time `json:"acceptedAt,omitempty"`
	EndedAt         *time.Time `json:"endedAt,omitempty"`
	DurationSeconds int64      `json:"durationSeconds"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type Announcement struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Content       string     `json:"content"`
	Status        string     `json:"status"`
	Pinned        bool       `json:"pinned"`
	TargetType    string     `json:"targetType"`
	TargetUserIDs []string   `json:"targetUserIds,omitempty"`
	ScheduledAt   *time.Time `json:"scheduledAt,omitempty"`
	PublishedAt   *time.Time `json:"publishedAt,omitempty"`
	WithdrawnAt   *time.Time `json:"withdrawnAt,omitempty"`
	PushOnPublish bool       `json:"pushOnPublish"`
	CreatedBy     string     `json:"createdBy"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	ReadAt        *time.Time `json:"readAt,omitempty"`
}

type Report struct {
	ID         string    `json:"id"`
	ReporterID string    `json:"reporterId"`
	TargetType string    `json:"targetType"`
	TargetID   string    `json:"targetId"`
	Reason     string    `json:"reason"`
	Details    string    `json:"details"`
	Status     string    `json:"status"`
	Resolution string    `json:"resolution,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type AuditEntry struct {
	ID         string         `json:"id"`
	ActorID    string         `json:"actorId"`
	Action     string         `json:"action"`
	TargetType string         `json:"targetType"`
	TargetID   string         `json:"targetId"`
	Metadata   map[string]any `json:"metadata"`
	Result     string         `json:"result"`
	IP         string         `json:"ip,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`
}
type Device struct {
	ID                   string    `json:"id"`
	UserID               string    `json:"userId"`
	Platform             string    `json:"platform"`
	Provider             string    `json:"provider"`
	PushToken            string    `json:"pushToken,omitempty"`
	NotificationsEnabled bool      `json:"notificationsEnabled"`
	PreviewEnabled       bool      `json:"previewEnabled"`
	SoundEnabled         bool      `json:"soundEnabled"`
	VibrationEnabled     bool      `json:"vibrationEnabled"`
	UpdatedAt            time.Time `json:"updatedAt"`
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

type State struct {
	Revision       int64                                     `json:"revision"`
	Users          map[string]*User                          `json:"users"`
	PhoneToUser    map[string]string                         `json:"phoneToUser"`
	FriendRequests map[string]*FriendRequest                 `json:"friendRequests"`
	Friends        map[string]map[string]bool                `json:"friends"`
	Blocks         map[string]map[string]bool                `json:"blocks"`
	Conversations  map[string]*Conversation                  `json:"conversations"`
	Members        map[string]map[string]*ConversationMember `json:"members"`
	DirectIndex    map[string]string                         `json:"directIndex"`
	Reports        map[string]*Report                        `json:"reports"`
	Audits         []*AuditEntry                             `json:"audits"`
	SensitiveWords map[string]string                         `json:"sensitiveWords"`
	Settings       map[string]any                            `json:"settings"`
	Devices        map[string]*Device                        `json:"devices"`
	Media          map[string]*Media                         `json:"media"`
}

func NewState() *State {
	return &State{
		Users: map[string]*User{}, PhoneToUser: map[string]string{}, FriendRequests: map[string]*FriendRequest{},
		Friends: map[string]map[string]bool{}, Blocks: map[string]map[string]bool{}, Conversations: map[string]*Conversation{},
		Members: map[string]map[string]*ConversationMember{}, DirectIndex: map[string]string{},
		Reports: map[string]*Report{}, Audits: []*AuditEntry{},
		SensitiveWords: map[string]string{}, Settings: map[string]any{
			"registrationEnabled": true, "allowRegistration": true, "passwordMinLength": 8,
			"maxMessageTextLength": 5000, "messageRecallMinutes": 2,
			"maxGroupMembers": 500, "allowFriendRequests": true, "friendRequestExpiryDays": 7,
			"allowSearchByHandle": true, "allowSearchByPhone": false,
			"announcementPushEnabled": true, "callsEnabled": true, "videoCallsEnabled": true,
			"sensitiveWordEnabled": true, "reportSlaHours": 8, "maintenanceMode": false, "announcement": "",
		}, Devices: map[string]*Device{}, Media: map[string]*Media{},
	}
}
