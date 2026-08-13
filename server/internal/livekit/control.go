package livekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
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
	URL           string
	APIURL        string
	APIKey        string
	APISecret     string
	PrometheusURL string
	TokenTTL      time.Duration
	Client        *http.Client
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

type MetricsSummary struct {
	Healthy                    bool      `json:"healthy"`
	ActiveRooms                int64     `json:"activeRooms"`
	ActiveParticipants         int64     `json:"activeParticipants"`
	CPUPercent                 float64   `json:"cpuPercent"`
	ResidentMemoryBytes        int64     `json:"residentMemoryBytes"`
	NetworkReceiveBytesPerSec  float64   `json:"networkReceiveBytesPerSecond"`
	NetworkTransmitBytesPerSec float64   `json:"networkTransmitBytesPerSecond"`
	PacketLossPercent          float64   `json:"packetLossPercent"`
	ParticipantJoinsLastHour   int64     `json:"participantJoinsLastHour"`
	RoomsCompletedLastHour     int64     `json:"roomsCompletedLastHour"`
	SampledAt                  time.Time `json:"sampledAt"`
}

type Control struct {
	url           string
	apiKey        string
	apiSecret     string
	prometheusURL string
	tokenTTL      time.Duration
	client        *http.Client
	rooms         lkproto.RoomService
}

func NewControl(cfg Config) (*Control, error) {
	if !strings.HasPrefix(cfg.URL, "ws://") && !strings.HasPrefix(cfg.URL, "wss://") {
		return nil, errors.New("LiveKit client URL must use ws or wss")
	}
	if !strings.HasPrefix(cfg.APIURL, "http://") && !strings.HasPrefix(cfg.APIURL, "https://") {
		return nil, errors.New("LiveKit API URL must use http or https")
	}
	if cfg.PrometheusURL != "" && !strings.HasPrefix(cfg.PrometheusURL, "http://") && !strings.HasPrefix(cfg.PrometheusURL, "https://") {
		return nil, errors.New("Prometheus URL must use http or https")
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
		url:           strings.TrimRight(cfg.URL, "/"),
		apiKey:        cfg.APIKey,
		apiSecret:     cfg.APISecret,
		prometheusURL: strings.TrimRight(cfg.PrometheusURL, "/"),
		tokenTTL:      ttl,
		client:        client,
		rooms:         lkproto.NewRoomServiceProtobufClient(strings.TrimRight(cfg.APIURL, "/"), client),
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

func (c *Control) Metrics(ctx context.Context) (MetricsSummary, error) {
	if c.prometheusURL == "" {
		return MetricsSummary{}, errors.New("Prometheus is not configured")
	}
	queries := []string{
		`max(up{job="livekit"})`,
		`sum(livekit_room_total{job="livekit"})`,
		`sum(livekit_participant_total{job="livekit"})`,
		`sum(rate(process_cpu_seconds_total{job="livekit"}[5m])) * 100`,
		`sum(process_resident_memory_bytes{job="livekit"})`,
		`sum(rate(process_network_receive_bytes_total{job="livekit"}[5m]))`,
		`sum(rate(process_network_transmit_bytes_total{job="livekit"}[5m]))`,
		`100 * sum(rate(livekit_packet_loss_total{job="livekit"}[5m])) / clamp_min(sum(rate(livekit_packet_total{job="livekit"}[5m])), 1)`,
		`sum(increase(livekit_participant_join_total{job="livekit"}[1h]))`,
		`sum(increase(livekit_room_duration_seconds_count{job="livekit"}[1h]))`,
	}
	values := make([]float64, len(queries))
	errs := make(chan error, len(queries))
	var wait sync.WaitGroup
	for index, query := range queries {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, err := c.prometheusScalar(ctx, query)
			if err != nil {
				errs <- err
				return
			}
			values[index] = value
		}()
	}
	wait.Wait()
	close(errs)
	if err := <-errs; err != nil {
		return MetricsSummary{}, err
	}
	return MetricsSummary{
		Healthy:                    values[0] == 1,
		ActiveRooms:                int64(values[1]),
		ActiveParticipants:         int64(values[2]),
		CPUPercent:                 values[3],
		ResidentMemoryBytes:        int64(values[4]),
		NetworkReceiveBytesPerSec:  values[5],
		NetworkTransmitBytesPerSec: values[6],
		PacketLossPercent:          values[7],
		ParticipantJoinsLastHour:   int64(values[8] + 0.5),
		RoomsCompletedLastHour:     int64(values[9] + 0.5),
		SampledAt:                  time.Now().UTC(),
	}, nil
}

func (c *Control) prometheusScalar(ctx context.Context, query string) (float64, error) {
	endpoint, err := url.Parse(c.prometheusURL + "/api/v1/query")
	if err != nil {
		return 0, fmt.Errorf("build Prometheus query: %w", err)
	}
	parameters := endpoint.Query()
	parameters.Set("query", query)
	endpoint.RawQuery = parameters.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("build Prometheus request: %w", err)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("query Prometheus: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("query Prometheus: status %d", response.StatusCode)
	}
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err = json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return 0, fmt.Errorf("decode Prometheus response: %w", err)
	}
	if payload.Status != "success" {
		return 0, errors.New("Prometheus query failed")
	}
	if len(payload.Data.Result) == 0 {
		return 0, nil
	}
	value := payload.Data.Result[0].Value
	if len(value) != 2 {
		return 0, errors.New("Prometheus scalar value is invalid")
	}
	var raw string
	if err = json.Unmarshal(value[1], &raw); err != nil {
		return 0, fmt.Errorf("decode Prometheus scalar: %w", err)
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse Prometheus scalar: %w", err)
	}
	return parsed, nil
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
