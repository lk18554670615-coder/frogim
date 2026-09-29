package tenancy

import "time"

// PushRequest carries routing metadata only. A tenant can never supply device
// tokens, arbitrary payloads, URLs or message text to the platform dispatcher.
type PushRequest struct {
	Identity
	RequestID      string    `json:"requestId"`
	AuthVersion    int64     `json:"authVersion"`
	RealmVersion   int64     `json:"realmVersion"`
	EventType      string    `json:"eventType"`
	ConversationID string    `json:"conversationId,omitempty"`
	MessageID      string    `json:"messageId,omitempty"`
	MessageType    string    `json:"messageType,omitempty"`
	CallID         string    `json:"callId,omitempty"`
	MediaType      string    `json:"mediaType,omitempty"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

func (p PushRequest) Validate(now time.Time) error {
	if p.Identity.Validate() != nil || !ValidID(p.RequestID) || p.AuthVersion < 1 || p.RealmVersion < 1 || p.ExpiresAt.IsZero() || p.ExpiresAt.After(now.Add(24*time.Hour)) {
		return ErrInvalid
	}
	if p.ConversationID != "" && !ValidID(p.ConversationID) || p.MessageID != "" && !ValidID(p.MessageID) || p.CallID != "" && !ValidID(p.CallID) {
		return ErrInvalid
	}
	switch p.EventType {
	case "message.created":
		if p.ConversationID == "" || p.MessageID == "" || p.CallID != "" || p.MediaType != "" {
			return ErrInvalid
		}
		switch p.MessageType {
		case "text", "image", "audio", "video", "file", "location", "contact", "sticker", "moment", "chat_history", "call", "live", "support":
		default:
			return ErrInvalid
		}
	case "call.invited":
		if p.ConversationID == "" || p.CallID == "" || p.MessageID != "" || p.MessageType != "" || (p.MediaType != "audio" && p.MediaType != "video") || p.ExpiresAt.After(now.Add(time.Minute)) {
			return ErrInvalid
		}
	case "friend.request", "friend.request.updated", "group.invite", "announcement.published":
		if p.ConversationID != "" || p.MessageID != "" || p.MessageType != "" || p.CallID != "" || p.MediaType != "" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

type PushReceipt struct {
	Status  string `json:"status"`
	Sent    int    `json:"sent"`
	Skipped int    `json:"skipped"`
}

// PushRejected is restricted to definitive refusals from the authenticated
// platform control endpoint. It never includes a peer body, URL or credential.
type PushRejected struct{}

func (*PushRejected) Error() string { return "platform rejected frozen push identity or request" }
