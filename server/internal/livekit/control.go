package livekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	lkauth "github.com/livekit/protocol/auth"
	lkproto "github.com/livekit/protocol/livekit"
	"github.com/twitchtv/twirp"
)

const (
	MaxCallParticipants = 9
	defaultTokenTTL     = 5 * time.Minute
)

type Config struct {
	URL       string
	APIURL    string
	APIKey    string
	APISecret string
	TokenTTL  time.Duration
	Client    *http.Client
}

type ParticipantSession struct {
	URL       string    `json:"url"`
	RoomName  string    `json:"roomName"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type RoomSummary struct {
	SID              string    `json:"sid"`
	Name             string    `json:"name"`
	Metadata         string    `json:"metadata,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	EmptyTimeout     uint32    `json:"emptyTimeoutSeconds"`
	DepartureTimeout uint32    `json:"departureTimeoutSeconds"`
	MaxParticipants  uint32    `json:"maxParticipants"`
	ParticipantCount uint32    `json:"participantCount"`
	PublisherCount   uint32    `json:"publisherCount"`
	ActiveRecording  bool      `json:"activeRecording"`
}

type ParticipantTrack struct {
	SID      string `json:"sid"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type"`
	Source   string `json:"source"`
	MimeType string `json:"mimeType,omitempty"`
	Muted    bool   `json:"muted"`
}

type ParticipantSummary struct {
	SID        string                         `json:"sid"`
	Identity   string                         `json:"identity"`
	Name       string                         `json:"name,omitempty"`
	State      string                         `json:"state"`
	Metadata   string                         `json:"metadata,omitempty"`
	JoinedAt   time.Time                      `json:"joinedAt"`
	Permission *lkproto.ParticipantPermission `json:"permission,omitempty"`
	Tracks     []ParticipantTrack             `json:"tracks"`
}

type Control struct {
	url       string
	apiKey    string
	apiSecret string
	tokenTTL  time.Duration
	rooms     lkproto.RoomService
}

func NewControl(cfg Config) (*Control, error) {
	if !strings.HasPrefix(cfg.URL, "ws://") && !strings.HasPrefix(cfg.URL, "wss://") {
		return nil, errors.New("LiveKit client URL must use ws or wss")
	}
	if !strings.HasPrefix(cfg.APIURL, "http://") && !strings.HasPrefix(cfg.APIURL, "https://") {
		return nil, errors.New("LiveKit API URL must use http or https")
	}
	if strings.TrimSpace(cfg.APIKey) == "" || len(cfg.APISecret) < 32 {
		return nil, errors.New("LiveKit API credentials are invalid")
	}
	ttl := cfg.TokenTTL
	if ttl == 0 {
		ttl = defaultTokenTTL
	}
	if ttl < time.Minute || ttl > 15*time.Minute {
		return nil, errors.New("LiveKit participant token TTL must be between 1m and 15m")
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Control{
		url:       strings.TrimRight(cfg.URL, "/"),
		apiKey:    cfg.APIKey,
		apiSecret: cfg.APISecret,
		tokenTTL:  ttl,
		rooms:     lkproto.NewRoomServiceProtobufClient(strings.TrimRight(cfg.APIURL, "/"), client),
	}, nil
}

func CallRoomName(callID string) string { return "call_" + callID }

func (c *Control) URL() string             { return c.url }
func (c *Control) TokenTTL() time.Duration { return c.tokenTTL }

func (c *Control) EnsureCallRoom(ctx context.Context, callID, conversationID, mediaType string) error {
	metadata, err := json.Marshal(map[string]string{
		"schemaVersion":  "1",
		"callId":         callID,
		"conversationId": conversationID,
		"mediaType":      mediaType,
	})
	if err != nil {
		return err
	}
	authCtx, err := c.authContext(ctx, &lkauth.VideoGrant{RoomCreate: true})
	if err != nil {
		return err
	}
	_, err = c.rooms.CreateRoom(authCtx, &lkproto.CreateRoomRequest{
		Name:             CallRoomName(callID),
		EmptyTimeout:     60,
		DepartureTimeout: 20,
		MaxParticipants:  MaxCallParticipants,
		Metadata:         string(metadata),
	})
	if isTwirpCode(err, twirp.AlreadyExists) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create LiveKit room: %w", err)
	}
	return nil
}

func (c *Control) DeleteCallRoom(ctx context.Context, callID string) error {
	authCtx, err := c.authContext(ctx, &lkauth.VideoGrant{RoomCreate: true})
	if err != nil {
		return err
	}
	_, err = c.rooms.DeleteRoom(authCtx, &lkproto.DeleteRoomRequest{Room: CallRoomName(callID)})
	if isTwirpCode(err, twirp.NotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete LiveKit room: %w", err)
	}
	return nil
}

func (c *Control) ListRooms(ctx context.Context) ([]RoomSummary, error) {
	authCtx, err := c.authContext(ctx, &lkauth.VideoGrant{RoomList: true})
	if err != nil {
		return nil, err
	}
	response, err := c.rooms.ListRooms(authCtx, &lkproto.ListRoomsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list LiveKit rooms: %w", err)
	}
	result := make([]RoomSummary, 0, len(response.GetRooms()))
	for _, room := range response.GetRooms() {
		result = append(result, RoomSummary{
			SID: room.GetSid(), Name: room.GetName(), Metadata: room.GetMetadata(), CreatedAt: unixTime(room.GetCreationTime()),
			EmptyTimeout: room.GetEmptyTimeout(), DepartureTimeout: room.GetDepartureTimeout(), MaxParticipants: room.GetMaxParticipants(),
			ParticipantCount: room.GetNumParticipants(), PublisherCount: room.GetNumPublishers(), ActiveRecording: room.GetActiveRecording(),
		})
	}
	return result, nil
}

func (c *Control) ListParticipants(ctx context.Context, roomName string) ([]ParticipantSummary, error) {
	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return nil, errors.New("LiveKit room name is required")
	}
	authCtx, err := c.authContext(ctx, &lkauth.VideoGrant{RoomAdmin: true, Room: roomName})
	if err != nil {
		return nil, err
	}
	response, err := c.rooms.ListParticipants(authCtx, &lkproto.ListParticipantsRequest{Room: roomName})
	if err != nil {
		return nil, fmt.Errorf("list LiveKit participants: %w", err)
	}
	result := make([]ParticipantSummary, 0, len(response.GetParticipants()))
	for _, participant := range response.GetParticipants() {
		tracks := make([]ParticipantTrack, 0, len(participant.GetTracks()))
		for _, track := range participant.GetTracks() {
			tracks = append(tracks, ParticipantTrack{
				SID: track.GetSid(), Name: track.GetName(), Type: track.GetType().String(), Source: track.GetSource().String(),
				MimeType: track.GetMimeType(), Muted: track.GetMuted(),
			})
		}
		result = append(result, ParticipantSummary{
			SID: participant.GetSid(), Identity: participant.GetIdentity(), Name: participant.GetName(),
			State: participant.GetState().String(), Metadata: participant.GetMetadata(), JoinedAt: unixTime(participant.GetJoinedAt()),
			Permission: participant.GetPermission(), Tracks: tracks,
		})
	}
	return result, nil
}

func (c *Control) RemoveParticipant(ctx context.Context, roomName, identity string) error {
	roomName, identity = strings.TrimSpace(roomName), strings.TrimSpace(identity)
	if roomName == "" || identity == "" {
		return errors.New("LiveKit room and participant identity are required")
	}
	authCtx, err := c.authContext(ctx, &lkauth.VideoGrant{RoomAdmin: true, Room: roomName})
	if err != nil {
		return err
	}
	_, err = c.rooms.RemoveParticipant(authCtx, &lkproto.RoomParticipantIdentity{Room: roomName, Identity: identity})
	if isTwirpCode(err, twirp.NotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove LiveKit participant: %w", err)
	}
	return nil
}

func (c *Control) DeleteRoom(ctx context.Context, roomName string) error {
	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return errors.New("LiveKit room name is required")
	}
	authCtx, err := c.authContext(ctx, &lkauth.VideoGrant{RoomCreate: true})
	if err != nil {
		return err
	}
	_, err = c.rooms.DeleteRoom(authCtx, &lkproto.DeleteRoomRequest{Room: roomName})
	if isTwirpCode(err, twirp.NotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete LiveKit room: %w", err)
	}
	return nil
}

func (c *Control) IssueParticipant(callID, userID, conversationID, mediaType string) (ParticipantSession, error) {
	if callID == "" || userID == "" {
		return ParticipantSession{}, errors.New("call and user identities are required")
	}
	metadata, err := json.Marshal(map[string]string{
		"schemaVersion":  "1",
		"callId":         callID,
		"conversationId": conversationID,
		"mediaType":      mediaType,
	})
	if err != nil {
		return ParticipantSession{}, err
	}
	grant := &lkauth.VideoGrant{RoomJoin: true, Room: CallRoomName(callID)}
	grant.SetCanPublish(true)
	grant.SetCanSubscribe(true)
	grant.SetCanPublishData(false)
	grant.SetCanPublishSources([]lkproto.TrackSource{
		lkproto.TrackSource_MICROPHONE,
		lkproto.TrackSource_CAMERA,
		lkproto.TrackSource_SCREEN_SHARE,
		lkproto.TrackSource_SCREEN_SHARE_AUDIO,
	})
	token, err := lkauth.NewAccessToken(c.apiKey, c.apiSecret).
		SetIdentity(userID).
		SetMetadata(string(metadata)).
		SetValidFor(c.tokenTTL).
		SetVideoGrant(grant).
		ToJWT()
	if err != nil {
		return ParticipantSession{}, fmt.Errorf("issue LiveKit participant token: %w", err)
	}
	return ParticipantSession{
		URL:       c.url,
		RoomName:  CallRoomName(callID),
		Token:     token,
		ExpiresAt: time.Now().Add(c.tokenTTL).UTC(),
	}, nil
}

func (c *Control) authContext(ctx context.Context, grant *lkauth.VideoGrant) (context.Context, error) {
	token, err := lkauth.NewAccessToken(c.apiKey, c.apiSecret).SetVideoGrant(grant).ToJWT()
	if err != nil {
		return nil, err
	}
	headers, _ := twirp.HTTPRequestHeaders(ctx)
	if headers == nil {
		headers = make(http.Header)
	} else {
		headers = headers.Clone()
	}
	headers.Set("Authorization", "Bearer "+token)
	return twirp.WithHTTPRequestHeaders(ctx, headers)
}

func isTwirpCode(err error, code twirp.ErrorCode) bool {
	var twirpErr twirp.Error
	return errors.As(err, &twirpErr) && twirpErr.Code() == code
}

func unixTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}
