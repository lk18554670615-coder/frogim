package livekit

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/linli/im/server/internal/tenancy"
	lkauth "github.com/livekit/protocol/auth"
)

// Kept in signed participant metadata so LiveKit's refreshed tokens retain the
// original enterprise and authentication generation. Participants cannot change
// their own metadata. API credentials and business JWTs are never accepted here.
type TenantParticipant struct {
	SchemaVersion  string           `json:"schemaVersion"`
	Identity       tenancy.Identity `json:"tenantIdentity"`
	AuthVersion    int64            `json:"authVersion"`
	RealmVersion   int64            `json:"realmVersion"`
	CallID         string           `json:"callId"`
	ConversationID string           `json:"conversationId"`
	MediaType      string           `json:"mediaType"`
}

func (c *Control) IssueTenantParticipant(callID, conversationID, mediaType string, identity tenancy.Identity, authVersion, realmVersion int64) (ParticipantSession, error) {
	if identity.Validate() != nil || authVersion < 1 || realmVersion < 1 || !tenancy.ValidID(callID) || conversationID == "" {
		return ParticipantSession{}, tenancy.ErrInvalid
	}
	metadata, err := json.Marshal(TenantParticipant{SchemaVersion: "3", Identity: identity, AuthVersion: authVersion, RealmVersion: realmVersion, CallID: callID, ConversationID: conversationID, MediaType: mediaType})
	if err != nil {
		return ParticipantSession{}, err
	}
	return c.issueParticipant(callID, identity.LocalUserID, string(metadata))
}

var ErrTenantParticipant = errors.New("enterprise call credential unavailable")

func (c *Control) VerifyTenantParticipant(token string) (TenantParticipant, error) {
	var p TenantParticipant
	if token == "" || len(token) > 16384 {
		return p, ErrTenantParticipant
	}
	v, err := lkauth.ParseAPIToken(token)
	if err != nil || v.APIKey() != c.apiKey {
		return p, ErrTenantParticipant
	}
	claims, grants, err := v.Verify(c.apiSecret)
	if err != nil || claims == nil || claims.ExpiresAt == nil || !claims.ExpiresAt.After(time.Now()) || grants == nil || grants.Video == nil {
		return p, ErrTenantParticipant
	}
	video := grants.Video
	if !video.RoomJoin || video.RoomAdmin || video.RoomCreate || video.RoomList || video.RoomRecord || video.IngressAdmin || video.GetCanUpdateOwnMetadata() || grants.SIP != nil {
		return p, ErrTenantParticipant
	}
	if json.Unmarshal([]byte(grants.Metadata), &p) != nil || p.SchemaVersion != "3" || p.Identity.Validate() != nil || p.AuthVersion < 1 || p.RealmVersion < 1 || !tenancy.ValidID(p.CallID) || p.ConversationID == "" || video.Room != CallRoomName(p.CallID) || grants.Identity != p.Identity.LocalUserID {
		return TenantParticipant{}, ErrTenantParticipant
	}
	return p, nil
}
