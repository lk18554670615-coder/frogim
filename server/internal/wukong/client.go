package wukong

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ChannelPerson         uint8 = 1
	ChannelGroup          uint8 = 2
	ChannelCustomer       uint8 = 3
	ChannelCommunity      uint8 = 4
	ChannelCommunityTopic uint8 = 5
	ChannelInfo           uint8 = 6
	ChannelLive           uint8 = 9
	ChannelVisitor        uint8 = 10
	ChannelAgent          uint8 = 11
	ChannelAgentCommunity uint8 = 12

	DeviceApp     = 0
	DeviceWeb     = 1
	DeviceDesktop = 2

	DeviceLevelSlave  = 0
	DeviceLevelMaster = 1

	// ContentTypeCommand is the pinned SDK command content type. Domain event
	// types (for example ContentTypeCallEvent) live inside its param object.
	ContentTypeText          = 1
	ContentTypeImage         = 2
	ContentTypeGIF           = 3
	ContentTypeVoice         = 4
	ContentTypeVideo         = 5
	ContentTypeLocation      = 6
	ContentTypeCard          = 7
	ContentTypeFile          = 8
	ContentTypeCommand       = 99
	ContentTypeMergedHistory = 1001
	ContentTypeSystemEvent   = 1002
	ContentTypeStoreSticker  = 1003
	ContentTypeMomentShare   = 1004
	ContentTypeCallEvent     = 1005
	ContentTypeLiveEvent     = 1006
	ContentTypeSupportEvent  = 1007
	ContentTypeScreenshot    = 1008

	// SettingStream is defined by the pinned WuKongIMGoProto v1.2.3.
	// Bit 5 is SettingSignal; stream is bit 1.
	SettingStream = 1 << 1
)

type Config struct {
	APIURL       string
	ManagerURL   string
	ManagerToken string
	Timeout      time.Duration
	MaxRetries   int
}

type Client struct {
	apiURL       string
	managerURL   string
	managerToken string
	http         *http.Client
	maxRetries   int
}

type HTTPError struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("wukongim %s %s returned %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

func NewClient(cfg Config) (*Client, error) {
	apiURL, err := normalizeHTTPURL(cfg.APIURL)
	if err != nil {
		return nil, fmt.Errorf("invalid WuKongIM API URL: %w", err)
	}
	managerURL, err := normalizeHTTPURL(cfg.ManagerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid WuKongIM manager URL: %w", err)
	}
	if strings.TrimSpace(cfg.ManagerToken) == "" {
		return nil, errors.New("WuKongIM manager token is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	return &Client{
		apiURL: apiURL, managerURL: managerURL, managerToken: cfg.ManagerToken,
		http: &http.Client{Timeout: cfg.Timeout}, maxRetries: cfg.MaxRetries,
	}, nil
}

func normalizeHTTPURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("absolute http(s) URL required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

type UserTokenRequest struct {
	UID         string `json:"uid"`
	Token       string `json:"token"`
	DeviceFlag  int    `json:"device_flag"`
	DeviceLevel int    `json:"device_level"`
}

func (c *Client) ProvisionUser(ctx context.Context, request UserTokenRequest) error {
	if strings.TrimSpace(request.UID) == "" || strings.TrimSpace(request.Token) == "" {
		return errors.New("uid and token are required")
	}
	if request.DeviceFlag < DeviceApp || request.DeviceFlag > DeviceDesktop {
		return fmt.Errorf("unsupported device flag %d", request.DeviceFlag)
	}
	return c.post(ctx, c.apiURL, "/user/token", request, nil, true)
}

func (c *Client) QuitDevice(ctx context.Context, uid string, deviceFlag int) error {
	if strings.TrimSpace(uid) == "" || deviceFlag < -1 || deviceFlag > DeviceDesktop {
		return errors.New("valid uid and device flag are required")
	}
	return c.post(ctx, c.apiURL, "/user/device_quit", map[string]any{"uid": uid, "device_flag": deviceFlag}, nil, true)
}

func normalizeSystemUIDs(uids []string) ([]string, error) {
	result := make([]string, 0, len(uids))
	seen := make(map[string]struct{}, len(uids))
	for _, value := range uids {
		uid := strings.TrimSpace(value)
		if uid == "" {
			return nil, errors.New("system uid is required")
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		result = append(result, uid)
	}
	if len(result) == 0 {
		return nil, errors.New("at least one system uid is required")
	}
	return result, nil
}

// AddSystemUIDs uses the exact v2.2.5 system account endpoint. When a
// datasource is enabled WuKongIM still uses this API to refresh the live node
// caches; the datasource remains authoritative after restart.
func (c *Client) AddSystemUIDs(ctx context.Context, uids []string) error {
	normalized, err := normalizeSystemUIDs(uids)
	if err != nil {
		return err
	}
	return c.post(ctx, c.apiURL, "/user/systemuids_add", map[string]any{"uids": normalized}, nil, true)
}

// RemoveSystemUIDs removes system-account privileges from all live WuKongIM
// node caches using the pinned v2.2.5 API contract.
func (c *Client) RemoveSystemUIDs(ctx context.Context, uids []string) error {
	normalized, err := normalizeSystemUIDs(uids)
	if err != nil {
		return err
	}
	return c.post(ctx, c.apiURL, "/user/systemuids_remove", map[string]any{"uids": normalized}, nil, true)
}

type MessageHeader struct {
	NoPersist int `json:"no_persist"`
	RedDot    int `json:"red_dot"`
	SyncOnce  int `json:"sync_once"`
}

const MaxCommandRecipients = 1000

type sendMessageRequest struct {
	Header      MessageHeader `json:"header"`
	ClientMsgNo string        `json:"client_msg_no,omitempty"`
	IsStream    int           `json:"is_stream,omitempty"`
	FromUID     string        `json:"from_uid,omitempty"`
	ChannelID   string        `json:"channel_id,omitempty"`
	ChannelType uint8         `json:"channel_type,omitempty"`
	Expire      uint32        `json:"expire,omitempty"`
	Subscribers []string      `json:"subscribers,omitempty"`
	Payload     []byte        `json:"payload"`
}

type StoredMessageRequest struct {
	ClientMsgNo string         `json:"client_msg_no"`
	FromUID     string         `json:"from_uid"`
	ChannelID   string         `json:"channel_id"`
	ChannelType uint8          `json:"channel_type"`
	Expire      uint32         `json:"expire"`
	Payload     map[string]any `json:"payload"`
}

type StoredMessageResult struct {
	MessageID   int64  `json:"message_id"`
	ClientMsgNo string `json:"client_msg_no"`
}

const (
	MessageEventStreamDelta    = "stream.delta"
	MessageEventStreamSnapshot = "stream.snapshot"
	MessageEventStreamClose    = "stream.close"
	MessageEventStreamError    = "stream.error"
	MessageEventStreamCancel   = "stream.cancel"
	MessageEventStreamFinish   = "stream.finish"
)

type MessageEventRequest struct {
	ChannelID   string         `json:"channel_id"`
	ChannelType uint8          `json:"channel_type"`
	FromUID     string         `json:"from_uid"`
	MessageID   int64          `json:"message_id,omitempty"`
	ClientMsgNo string         `json:"client_msg_no"`
	EventID     string         `json:"event_id"`
	EventType   string         `json:"event_type"`
	EventKey    string         `json:"event_key,omitempty"`
	Visibility  string         `json:"visibility,omitempty"`
	OccurredAt  int64          `json:"occurred_at,omitempty"`
	Payload     map[string]any `json:"payload,omitempty"`
}

type MessageEventResult struct {
	ClientMsgNo  string `json:"client_msg_no"`
	EventKey     string `json:"event_key"`
	EventID      string `json:"event_id"`
	MsgEventSeq  uint64 `json:"msg_event_seq"`
	StreamStatus string `json:"stream_status"`
	ChannelID    string `json:"channel_id"`
	ChannelType  uint8  `json:"channel_type"`
	FromUID      string `json:"from_uid"`
}

type MessageEventSyncRequest struct {
	ChannelID       string `json:"channel_id"`
	ChannelType     uint8  `json:"channel_type"`
	FromUID         string `json:"from_uid"`
	ClientMsgNo     string `json:"client_msg_no"`
	EventKey        string `json:"event_key,omitempty"`
	FromMsgEventSeq uint64 `json:"from_msg_event_seq"`
	Limit           int    `json:"limit"`
}

type MessageEvent struct {
	MsgEventSeq uint64         `json:"msg_event_seq"`
	EventID     string         `json:"event_id"`
	EventKey    string         `json:"event_key"`
	EventType   string         `json:"event_type"`
	Visibility  string         `json:"visibility,omitempty"`
	OccurredAt  int64          `json:"occurred_at,omitempty"`
	Payload     map[string]any `json:"payload,omitempty"`
}

type MessageEventSyncResult struct {
	ClientMsgNo     string         `json:"client_msg_no"`
	FromMsgEventSeq uint64         `json:"from_msg_event_seq"`
	NextMsgEventSeq uint64         `json:"next_msg_event_seq"`
	More            int            `json:"more"`
	Events          []MessageEvent `json:"events"`
	FilteredByKey   string         `json:"filtered_by_event_key"`
}

func supportedContentType(contentType int) bool {
	return (contentType >= ContentTypeText && contentType <= ContentTypeFile) ||
		(contentType >= ContentTypeMergedHistory && contentType <= ContentTypeScreenshot)
}

// SendStoredMessage is the exact pinned /message/send channel contract used
// for server-originated scheduled, system and operational messages. Ordinary
// client messages continue to travel directly through the official SDK.
func (c *Client) SendStoredMessage(ctx context.Context, request StoredMessageRequest) (StoredMessageResult, error) {
	return c.sendStoredMessage(ctx, request, false)
}

// SendStreamMessage creates the pinned v2.2.5 stream anchor. Incremental
// content is attached later through AppendMessageEvent using client_msg_no.
func (c *Client) SendStreamMessage(ctx context.Context, request StoredMessageRequest) (StoredMessageResult, error) {
	return c.sendStoredMessage(ctx, request, true)
}

func (c *Client) sendStoredMessage(ctx context.Context, request StoredMessageRequest, stream bool) (StoredMessageResult, error) {
	var output struct {
		Status int                 `json:"status"`
		Data   StoredMessageResult `json:"data"`
	}
	request.ClientMsgNo = strings.TrimSpace(request.ClientMsgNo)
	request.FromUID = strings.TrimSpace(request.FromUID)
	request.ChannelID = strings.TrimSpace(request.ChannelID)
	contentType, ok := request.Payload["type"].(int)
	if !ok {
		if numeric, numericOK := request.Payload["type"].(float64); numericOK {
			contentType, ok = int(numeric), numeric == float64(int(numeric))
		}
	}
	if request.ClientMsgNo == "" || request.FromUID == "" || request.ChannelID == "" || !SupportedChannelType(request.ChannelType) || !ok || !supportedContentType(contentType) {
		return StoredMessageResult{}, errors.New("valid stored message request is required")
	}
	payload, err := json.Marshal(request.Payload)
	if err != nil {
		return StoredMessageResult{}, fmt.Errorf("encode WuKongIM stored message: %w", err)
	}
	err = c.post(ctx, c.apiURL, "/message/send", sendMessageRequest{
		Header:      MessageHeader{NoPersist: 0, RedDot: 1, SyncOnce: 0},
		IsStream:    map[bool]int{true: 1, false: 0}[stream],
		ClientMsgNo: request.ClientMsgNo, FromUID: request.FromUID,
		ChannelID: request.ChannelID, ChannelType: request.ChannelType,
		Expire: request.Expire, Payload: payload,
	}, &output, true)
	if err != nil {
		return StoredMessageResult{}, err
	}
	if output.Status != http.StatusOK || output.Data.MessageID == 0 || strings.TrimSpace(output.Data.ClientMsgNo) == "" {
		return StoredMessageResult{}, errors.New("WuKongIM stored message response is incomplete")
	}
	return output.Data, nil
}

// AppendMessageEvent uses the exact pinned /message/event envelope. event_id
// is the idempotency key owned by WuKongIM.
func (c *Client) AppendMessageEvent(ctx context.Context, request MessageEventRequest) (MessageEventResult, error) {
	var output struct {
		Status int                `json:"status"`
		Data   MessageEventResult `json:"data"`
	}
	request.ChannelID = strings.TrimSpace(request.ChannelID)
	request.FromUID = strings.TrimSpace(request.FromUID)
	request.ClientMsgNo = strings.TrimSpace(request.ClientMsgNo)
	request.EventID = strings.TrimSpace(request.EventID)
	request.EventType = strings.ToLower(strings.TrimSpace(request.EventType))
	request.EventKey = strings.TrimSpace(request.EventKey)
	if request.ChannelID == "" || request.FromUID == "" || request.ClientMsgNo == "" || request.EventID == "" || !SupportedChannelType(request.ChannelType) || !supportedMessageEventType(request.EventType) {
		return MessageEventResult{}, errors.New("valid WuKongIM message event is required")
	}
	if request.EventType == MessageEventStreamFinish {
		request.EventKey = ""
		request.Payload = nil
	} else if request.EventKey == "" {
		request.EventKey = "main"
	}
	if err := c.post(ctx, c.apiURL, "/message/event", request, &output, true); err != nil {
		return MessageEventResult{}, err
	}
	if output.Status != http.StatusOK || output.Data.ClientMsgNo == "" || output.Data.EventID == "" || output.Data.ChannelID == "" {
		return MessageEventResult{}, errors.New("WuKongIM message event response is incomplete")
	}
	return output.Data, nil
}

func supportedMessageEventType(value string) bool {
	switch value {
	case MessageEventStreamDelta, MessageEventStreamSnapshot, MessageEventStreamClose, MessageEventStreamError, MessageEventStreamCancel, MessageEventStreamFinish:
		return true
	default:
		return false
	}
}

// SyncMessageEvents reads the pinned persisted event projection. Private and
// restricted events are intentionally never requested by the client proxy.
func (c *Client) SyncMessageEvents(ctx context.Context, request MessageEventSyncRequest) (MessageEventSyncResult, error) {
	var output struct {
		Status int                    `json:"status"`
		Data   MessageEventSyncResult `json:"data"`
	}
	request.ChannelID = strings.TrimSpace(request.ChannelID)
	request.FromUID = strings.TrimSpace(request.FromUID)
	request.ClientMsgNo = strings.TrimSpace(request.ClientMsgNo)
	request.EventKey = strings.TrimSpace(request.EventKey)
	if request.ChannelID == "" || request.FromUID == "" || request.ClientMsgNo == "" || !SupportedChannelType(request.ChannelType) || request.Limit <= 0 || request.Limit > 2000 {
		return MessageEventSyncResult{}, errors.New("valid WuKongIM message event sync request is required")
	}
	if err := c.post(ctx, c.apiURL, "/message/eventsync", request, &output, true); err != nil {
		return MessageEventSyncResult{}, err
	}
	if output.Status != http.StatusOK || output.Data.ClientMsgNo != request.ClientMsgNo {
		return MessageEventSyncResult{}, errors.New("WuKongIM message event sync response is incomplete")
	}
	if output.Data.Events == nil {
		output.Data.Events = []MessageEvent{}
	}
	return output.Data, nil
}

// SendCommand uses the exact targeted, non-persistent command contract in the
// pinned WuKongIM server: subscribers requires sync_once=1, and SDK command
// routing requires content type 99 with cmd/param fields.
func (c *Client) SendCommand(ctx context.Context, recipients []string, command string, param map[string]any) error {
	command = strings.TrimSpace(command)
	if command == "" || len(recipients) == 0 || len(recipients) > MaxCommandRecipients {
		return errors.New("valid command and 1-1000 recipients are required")
	}
	clean := make([]string, 0, len(recipients))
	seen := make(map[string]struct{}, len(recipients))
	for _, recipient := range recipients {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			return errors.New("command recipient is required")
		}
		if _, exists := seen[recipient]; exists {
			continue
		}
		seen[recipient] = struct{}{}
		clean = append(clean, recipient)
	}
	if len(clean) == 0 {
		return errors.New("command recipient is required")
	}
	if param == nil {
		param = map[string]any{}
	}
	payload, err := json.Marshal(map[string]any{
		"type":  ContentTypeCommand,
		"cmd":   command,
		"param": param,
	})
	if err != nil {
		return fmt.Errorf("encode WuKongIM command: %w", err)
	}
	return c.post(ctx, c.apiURL, "/message/send", sendMessageRequest{
		Header:      MessageHeader{NoPersist: 1, RedDot: 0, SyncOnce: 1},
		Subscribers: clean,
		Payload:     payload,
	}, nil, true)
}

// SendBusinessEvent sends a versioned, non-persistent application event over
// WuKongIM CMD. Durable events normally enter this shape through the database
// outbox; this direct path is reserved for intentionally ephemeral hints such
// as typing state.
func (c *Client) SendBusinessEvent(ctx context.Context, recipients []string, event string, payload any) error {
	event = strings.TrimSpace(event)
	if event == "" || event == "message.created" || strings.HasPrefix(event, "call.") {
		return errors.New("valid non-call business event is required")
	}
	param := map[string]any{"schemaVersion": 1, "event": event}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode WuKongIM business event: %w", err)
		}
		var snapshot any
		if err = json.Unmarshal(raw, &snapshot); err != nil {
			return fmt.Errorf("snapshot WuKongIM business event: %w", err)
		}
		param["payload"] = snapshot
	}
	clean := make([]string, 0, len(recipients))
	seen := make(map[string]struct{}, len(recipients))
	for _, recipient := range recipients {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			return errors.New("business event recipient is required")
		}
		if _, exists := seen[recipient]; exists {
			continue
		}
		seen[recipient] = struct{}{}
		clean = append(clean, recipient)
	}
	if len(clean) == 0 {
		return errors.New("business event recipient is required")
	}
	for offset := 0; offset < len(clean); offset += MaxCommandRecipients {
		end := min(offset+MaxCommandRecipients, len(clean))
		if err := c.SendCommand(ctx, clean[offset:end], event, param); err != nil {
			return err
		}
	}
	return nil
}

type ChannelRequest struct {
	ChannelID     string   `json:"channel_id"`
	ChannelType   uint8    `json:"channel_type"`
	Large         int      `json:"large"`
	Ban           int      `json:"ban"`
	Disband       int      `json:"disband"`
	SendBan       int      `json:"send_ban"`
	AllowStranger int      `json:"allow_stranger"`
	Reset         int      `json:"reset"`
	Subscribers   []string `json:"subscribers"`
}

func (c *Client) UpsertChannel(ctx context.Context, request ChannelRequest) error {
	if strings.TrimSpace(request.ChannelID) == "" || !SupportedChannelType(request.ChannelType) {
		return errors.New("valid channel id and supported channel type are required")
	}
	if request.ChannelType == ChannelPerson && len(request.Subscribers) != 0 {
		return errors.New("person channels cannot contain subscribers")
	}
	return c.post(ctx, c.apiURL, "/channel", request, nil, true)
}

func (c *Client) SetSubscribers(ctx context.Context, channelID string, channelType uint8, subscribers []string) error {
	if strings.TrimSpace(channelID) == "" || !SupportedChannelType(channelType) || channelType == ChannelPerson {
		return errors.New("valid non-person channel is required")
	}
	request := map[string]any{"channel_id": channelID, "channel_type": channelType, "subscribers": subscribers, "reset": 1}
	return c.post(ctx, c.apiURL, "/channel/subscriber_add", request, nil, true)
}

type AccessListRequest struct {
	ChannelID   string   `json:"channel_id"`
	ChannelType uint8    `json:"channel_type"`
	UIDs        []string `json:"uids"`
}

func (c *Client) AddAllowlist(ctx context.Context, channelID string, channelType uint8, uids []string) error {
	request, err := validAccessListRequest(channelID, channelType, uids)
	if err != nil {
		return err
	}
	return c.post(ctx, c.apiURL, "/channel/whitelist_add", request, nil, true)
}

func (c *Client) RemoveAllowlist(ctx context.Context, channelID string, channelType uint8, uids []string) error {
	request, err := validAccessListRequest(channelID, channelType, uids)
	if err != nil {
		return err
	}
	return c.post(ctx, c.apiURL, "/channel/whitelist_remove", request, nil, true)
}

func (c *Client) SetAllowlist(ctx context.Context, channelID string, channelType uint8, uids []string) error {
	request, err := accessListSetRequest(channelID, channelType, uids)
	if err != nil {
		return err
	}
	return c.post(ctx, c.apiURL, "/channel/whitelist_set", request, nil, true)
}

func (c *Client) AddDenylist(ctx context.Context, channelID string, channelType uint8, uids []string) error {
	request, err := validAccessListRequest(channelID, channelType, uids)
	if err != nil {
		return err
	}
	return c.post(ctx, c.apiURL, "/channel/blacklist_add", request, nil, true)
}

func (c *Client) RemoveDenylist(ctx context.Context, channelID string, channelType uint8, uids []string) error {
	request, err := validAccessListRequest(channelID, channelType, uids)
	if err != nil {
		return err
	}
	return c.post(ctx, c.apiURL, "/channel/blacklist_remove", request, nil, true)
}

func (c *Client) SetDenylist(ctx context.Context, channelID string, channelType uint8, uids []string) error {
	request, err := accessListSetRequest(channelID, channelType, uids)
	if err != nil {
		return err
	}
	return c.post(ctx, c.apiURL, "/channel/blacklist_set", request, nil, true)
}

func validAccessListRequest(channelID string, channelType uint8, uids []string) (AccessListRequest, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || !SupportedChannelType(channelType) || len(uids) == 0 {
		return AccessListRequest{}, errors.New("valid channel and at least one uid are required")
	}
	return accessListSetRequest(channelID, channelType, uids)
}

func accessListSetRequest(channelID string, channelType uint8, uids []string) (AccessListRequest, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || !SupportedChannelType(channelType) {
		return AccessListRequest{}, errors.New("valid channel is required")
	}
	clean := make([]string, 0, len(uids))
	seen := make(map[string]struct{}, len(uids))
	for _, uid := range uids {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			return AccessListRequest{}, errors.New("access list uid is required")
		}
		if _, exists := seen[uid]; !exists {
			seen[uid] = struct{}{}
			clean = append(clean, uid)
		}
	}
	return AccessListRequest{ChannelID: channelID, ChannelType: channelType, UIDs: clean}, nil
}

func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, c.apiURL, "/health", nil, nil, true)
}

func (c *Client) ManagerHealth(ctx context.Context, output any) error {
	return c.do(ctx, http.MethodGet, c.managerURL, "/health", nil, output, true)
}

// ManagerVarz is the pinned v2.2.5 Manager /varz contract. The result is kept
// schema-flexible because WuKongIM exposes counters added by build options, but
// the request path and authentication remain fixed and covered by tests.
func (c *Client) ManagerVarz(ctx context.Context) (map[string]any, error) {
	var output map[string]any
	if err := c.do(ctx, http.MethodGet, c.managerURL, "/varz", nil, &output, true); err != nil {
		return nil, err
	}
	if output == nil {
		output = map[string]any{}
	}
	return output, nil
}

// ManagerSettings reads the exact pinned v2.2.5 /varz/setting contract. It
// exposes only runtime feature flags and contains no WuKongIM credentials.
func (c *Client) ManagerSettings(ctx context.Context) (map[string]any, error) {
	var output map[string]any
	if err := c.do(ctx, http.MethodGet, c.managerURL, "/varz/setting", nil, &output, true); err != nil {
		return nil, err
	}
	if output == nil {
		output = map[string]any{}
	}
	return output, nil
}

func (c *Client) ManagerNodes(ctx context.Context) (map[string]any, error) {
	return c.managerObject(ctx, "/cluster/nodes", nil)
}

type ManagerChannelQuery struct {
	ChannelID     string
	ChannelType   uint8
	OffsetCreated int64
	Previous      bool
	Limit         int
}

func (c *Client) ManagerChannels(ctx context.Context, request ManagerChannelQuery) (map[string]any, error) {
	if request.Limit <= 0 || request.Limit > 200 || (request.ChannelType != 0 && !SupportedChannelType(request.ChannelType)) {
		return nil, errors.New("valid WuKongIM channel page is required")
	}
	query := url.Values{"limit": {strconv.Itoa(request.Limit)}}
	if channelID := strings.TrimSpace(request.ChannelID); channelID != "" {
		query.Set("channel_id", channelID)
	}
	if request.ChannelType != 0 {
		query.Set("channel_type", strconv.Itoa(int(request.ChannelType)))
	}
	if request.OffsetCreated > 0 {
		query.Set("offset_created_at", strconv.FormatInt(request.OffsetCreated, 10))
	}
	if request.Previous {
		query.Set("pre", "1")
	}
	return c.managerObject(ctx, "/cluster/channels", query)
}

type ManagerMessageQuery struct {
	FromUID          string
	ChannelID        string
	ChannelType      uint8
	MessageID        int64
	ClientMsgNo      string
	OffsetMessageID  int64
	OffsetMessageSeq uint64
	Previous         bool
	Limit            int
}

func (c *Client) ManagerMessages(ctx context.Context, request ManagerMessageQuery) (map[string]any, error) {
	if request.Limit <= 0 || request.Limit > 200 || (request.ChannelType != 0 && !SupportedChannelType(request.ChannelType)) {
		return nil, errors.New("valid WuKongIM message page is required")
	}
	query := url.Values{"limit": {strconv.Itoa(request.Limit)}}
	setQueryString(query, "from_uid", request.FromUID)
	setQueryString(query, "channel_id", request.ChannelID)
	setQueryString(query, "client_msg_no", request.ClientMsgNo)
	if request.ChannelType != 0 {
		query.Set("channel_type", strconv.Itoa(int(request.ChannelType)))
	}
	if request.MessageID > 0 {
		query.Set("message_id", strconv.FormatInt(request.MessageID, 10))
	}
	if request.OffsetMessageID > 0 {
		query.Set("offset_message_id", strconv.FormatInt(request.OffsetMessageID, 10))
	}
	if request.OffsetMessageSeq > 0 {
		query.Set("offset_message_seq", strconv.FormatUint(request.OffsetMessageSeq, 10))
	}
	if request.Previous {
		query.Set("pre", "1")
	}
	return c.managerObject(ctx, "/cluster/messages", query)
}

type ManagerDeviceQuery struct {
	UID           string
	DeviceFlag    int
	OffsetCreated int64
	Previous      bool
	Limit         int
}

func (c *Client) ManagerDevices(ctx context.Context, request ManagerDeviceQuery) (map[string]any, error) {
	if request.Limit <= 0 || request.Limit > 200 || request.DeviceFlag < -1 || request.DeviceFlag > DeviceDesktop {
		return nil, errors.New("valid WuKongIM device page is required")
	}
	query := url.Values{"limit": {strconv.Itoa(request.Limit)}}
	setQueryString(query, "uid", request.UID)
	if request.DeviceFlag >= 0 {
		query.Set("device_flag", strconv.Itoa(request.DeviceFlag))
	}
	if request.OffsetCreated > 0 {
		query.Set("offset_created_at", strconv.FormatInt(request.OffsetCreated, 10))
	}
	if request.Previous {
		query.Set("pre", "1")
	}
	return c.managerObject(ctx, "/cluster/devices", query)
}

func (c *Client) ManagerPlugins(ctx context.Context, nodeID uint64) ([]map[string]any, error) {
	query := url.Values{}
	if nodeID > 0 {
		query.Set("node_id", strconv.FormatUint(nodeID, 10))
	}
	path := "/plugins"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var output []map[string]any
	if err := c.do(ctx, http.MethodGet, c.managerURL, path, nil, &output, true); err != nil {
		return nil, err
	}
	if output == nil {
		output = []map[string]any{}
	}
	return output, nil
}

type ManagerPluginLogEntry struct {
	Sequence  uint64 `json:"sequence"`
	Stream    string `json:"stream"`
	Timestamp int64  `json:"timestamp"`
	Message   string `json:"message"`
}

type ManagerPluginLogs struct {
	PluginNo string                  `json:"plugin_no"`
	NodeID   uint64                  `json:"node_id"`
	Entries  []ManagerPluginLogEntry `json:"entries"`
}

func (c *Client) ManagerPluginLogs(ctx context.Context, pluginNo string, nodeID uint64, limit int) (*ManagerPluginLogs, error) {
	pluginNo = strings.TrimSpace(pluginNo)
	if pluginNo == "" || limit <= 0 || limit > 500 {
		return nil, errors.New("plugin number and log limit are required")
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if nodeID > 0 {
		query.Set("node_id", strconv.FormatUint(nodeID, 10))
	}
	path := "/pluginlogs/" + url.PathEscape(pluginNo) + "?" + query.Encode()
	var output ManagerPluginLogs
	if err := c.do(ctx, http.MethodGet, c.managerURL, path, nil, &output, true); err != nil {
		return nil, err
	}
	if output.Entries == nil {
		output.Entries = []ManagerPluginLogEntry{}
	}
	return &output, nil
}

func (c *Client) UpdatePluginConfig(ctx context.Context, pluginNo string, nodeID uint64, config map[string]any) error {
	pluginNo = strings.TrimSpace(pluginNo)
	if pluginNo == "" || nodeID == 0 || config == nil {
		return errors.New("plugin number, node and config are required")
	}
	return c.post(ctx, c.managerURL, "/pluginconfig/"+url.PathEscape(pluginNo), map[string]any{"node_id": nodeID, "config": config}, nil, false)
}

func (c *Client) UninstallPlugin(ctx context.Context, pluginNo string, nodeID uint64) error {
	pluginNo = strings.TrimSpace(pluginNo)
	if pluginNo == "" || nodeID == 0 {
		return errors.New("plugin number and node are required")
	}
	return c.post(ctx, c.managerURL, "/plugin/uninstall", map[string]any{"plugin_no": pluginNo, "node_id": nodeID}, nil, false)
}

func (c *Client) managerObject(ctx context.Context, path string, query url.Values) (map[string]any, error) {
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var output map[string]any
	if err := c.do(ctx, http.MethodGet, c.managerURL, path, nil, &output, true); err != nil {
		return nil, err
	}
	if output == nil {
		output = map[string]any{}
	}
	return output, nil
}

func setQueryString(query url.Values, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		query.Set(key, value)
	}
}

type ConnectionInfo struct {
	ID              int64     `json:"id"`
	UID             string    `json:"uid"`
	IP              string    `json:"ip"`
	Port            int       `json:"port"`
	LastActivity    time.Time `json:"last_activity"`
	Device          string    `json:"device"`
	DeviceID        string    `json:"device_id"`
	Version         uint8     `json:"version"`
	NodeID          uint64    `json:"node_id"`
	InMessages      int64     `json:"in_msgs"`
	OutMessages     int64     `json:"out_msgs"`
	InMessageBytes  int64     `json:"in_msg_bytes"`
	OutMessageBytes int64     `json:"out_msg_bytes"`
}

type ConnectionList struct {
	Connections []ConnectionInfo `json:"connections"`
	Now         time.Time        `json:"now"`
	Total       int              `json:"total"`
	Offset      int              `json:"offset"`
	Limit       int              `json:"limit"`
}

// Connections proxies the pinned Manager /connz contract. The Manager token
// remains server-side and this response is reduced before reaching admin UIs.
func (c *Client) Connections(ctx context.Context, uid string, offset, limit int) (ConnectionList, error) {
	var output ConnectionList
	uid = strings.TrimSpace(uid)
	if offset < 0 || limit <= 0 || limit > 10000 {
		return output, errors.New("valid connection page is required")
	}
	query := url.Values{}
	query.Set("offset", strconv.Itoa(offset))
	query.Set("limit", strconv.Itoa(limit))
	query.Set("sort", "id")
	if uid != "" {
		query.Set("uid", uid)
	}
	if err := c.do(ctx, http.MethodGet, c.managerURL, "/connz?"+query.Encode(), nil, &output, true); err != nil {
		return ConnectionList{}, err
	}
	if output.Connections == nil {
		output.Connections = []ConnectionInfo{}
	}
	return output, nil
}

type ConversationSyncRequest struct {
	UID                 string  `json:"uid"`
	Version             int64   `json:"version"`
	LastMsgSeqs         string  `json:"last_msg_seqs"`
	MsgCount            int64   `json:"msg_count"`
	OnlyUnread          uint8   `json:"only_unread"`
	ExcludeChannelTypes []uint8 `json:"exclude_channel_types"`
	Page                int     `json:"page"`
	PageSize            int     `json:"page_size"`
}

// MaxConversationSyncMessageCount matches the pinned Flutter SDK 1.7.9
// conversation callback, which requests 200 recent messages after CONNACK.
const MaxConversationSyncMessageCount int64 = 200

type SyncedConversation struct {
	ChannelID      string          `json:"channel_id"`
	ChannelType    uint8           `json:"channel_type"`
	Unread         int             `json:"unread"`
	Timestamp      int64           `json:"timestamp"`
	LastMsgSeq     uint32          `json:"last_msg_seq"`
	LastClientNo   string          `json:"last_client_msg_no"`
	OffsetMsgSeq   int64           `json:"offset_msg_seq"`
	ReadedToMsgSeq uint32          `json:"readed_to_msg_seq"`
	Version        int64           `json:"version"`
	Recents        []SyncedMessage `json:"recents"`
}

// SyncedMessage preserves the pinned server response while decoding its
// []byte payload (base64 in JSON) into the content object expected by all
// official client SDK datasource callbacks.
type SyncedMessage map[string]any

func (m *SyncedMessage) UnmarshalJSON(data []byte) error {
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	payload, ok := value["payload"].(string)
	if ok && payload != "" {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return fmt.Errorf("decode WuKongIM message payload: %w", err)
		}
		var content any
		if err = json.Unmarshal(decoded, &content); err != nil {
			return fmt.Errorf("decode WuKongIM message content: %w", err)
		}
		value["payload"] = content
	}
	*m = value
	return nil
}

func (c *Client) SyncConversations(ctx context.Context, request ConversationSyncRequest) ([]SyncedConversation, error) {
	if strings.TrimSpace(request.UID) == "" || request.MsgCount < 0 || request.MsgCount > MaxConversationSyncMessageCount || request.Page < 0 || request.PageSize < 0 || request.PageSize > 500 {
		return nil, errors.New("invalid conversation sync request")
	}
	var output []SyncedConversation
	if err := c.post(ctx, c.apiURL, "/conversation/sync", request, &output, true); err != nil {
		return nil, err
	}
	if output == nil {
		output = []SyncedConversation{}
	}
	return output, nil
}

type MessageSyncRequest struct {
	LoginUID         string `json:"login_uid"`
	ChannelID        string `json:"channel_id"`
	ChannelType      uint8  `json:"channel_type"`
	StartMessageSeq  uint64 `json:"start_message_seq"`
	EndMessageSeq    uint64 `json:"end_message_seq"`
	Limit            int    `json:"limit"`
	PullMode         int    `json:"pull_mode"`
	EventSummaryMode string `json:"event_summary_mode"`
}

type MessageSyncResponse struct {
	StartMessageSeq uint64          `json:"start_message_seq"`
	EndMessageSeq   uint64          `json:"end_message_seq"`
	More            int             `json:"more"`
	Messages        []SyncedMessage `json:"messages"`
}

type MessageSearchRequest struct {
	LoginUID     string   `json:"login_uid"`
	ChannelID    string   `json:"channel_id"`
	ChannelType  uint8    `json:"channel_type"`
	MessageSeqs  []uint32 `json:"message_seqs"`
	MessageIDs   []int64  `json:"message_ids"`
	ClientMsgNos []string `json:"client_msg_nos"`
}

// SearchMessages uses the pinned /messages contract. Callers must already
// have authorized the business conversation before supplying the channel.
func (c *Client) SearchMessages(ctx context.Context, request MessageSearchRequest) ([]SyncedMessage, error) {
	request.LoginUID = strings.TrimSpace(request.LoginUID)
	request.ChannelID = strings.TrimSpace(request.ChannelID)
	total := len(request.MessageSeqs) + len(request.MessageIDs) + len(request.ClientMsgNos)
	if request.LoginUID == "" || request.ChannelID == "" || !SupportedChannelType(request.ChannelType) || total == 0 || total > 100 {
		return nil, errors.New("invalid message search request")
	}
	var output struct {
		Messages []SyncedMessage `json:"messages"`
	}
	if err := c.post(ctx, c.apiURL, "/messages", request, &output, true); err != nil {
		return nil, err
	}
	if output.Messages == nil {
		output.Messages = []SyncedMessage{}
	}
	return output.Messages, nil
}

func (c *Client) SyncMessages(ctx context.Context, request MessageSyncRequest) (MessageSyncResponse, error) {
	var output MessageSyncResponse
	if strings.TrimSpace(request.LoginUID) == "" || strings.TrimSpace(request.ChannelID) == "" || !SupportedChannelType(request.ChannelType) || request.Limit <= 0 || request.Limit > 500 || (request.PullMode != 0 && request.PullMode != 1) || (request.EventSummaryMode != "" && request.EventSummaryMode != "basic" && request.EventSummaryMode != "full") {
		return output, errors.New("invalid message sync request")
	}
	if err := c.post(ctx, c.apiURL, "/channel/messagesync", request, &output, true); err != nil {
		return output, err
	}
	if output.Messages == nil {
		output.Messages = []SyncedMessage{}
	}
	return output, nil
}

func (c *Client) ChannelMaxMessageSeq(ctx context.Context, loginUID, channelID string, channelType uint8) (uint64, error) {
	loginUID, channelID = strings.TrimSpace(loginUID), strings.TrimSpace(channelID)
	if loginUID == "" || channelID == "" || !SupportedChannelType(channelType) {
		return 0, errors.New("valid login uid and channel are required")
	}
	query := url.Values{}
	query.Set("login_uid", loginUID)
	query.Set("channel_id", channelID)
	query.Set("channel_type", strconv.Itoa(int(channelType)))
	var output struct {
		MessageSeq uint64 `json:"message_seq"`
	}
	if err := c.do(ctx, http.MethodGet, c.apiURL, "/channel/max_message_seq?"+query.Encode(), nil, &output, true); err != nil {
		return 0, err
	}
	return output.MessageSeq, nil
}

func (c *Client) SetConversationUnread(ctx context.Context, uid, channelID string, channelType uint8, unread int) error {
	uid, channelID = strings.TrimSpace(uid), strings.TrimSpace(channelID)
	if uid == "" || channelID == "" || !SupportedChannelType(channelType) || unread < 0 {
		return errors.New("valid uid, channel and unread count are required")
	}
	return c.post(ctx, c.apiURL, "/conversations/setUnread", map[string]any{
		"uid": uid, "channel_id": channelID, "channel_type": channelType, "unread": unread,
	}, nil, true)
}

func SupportedChannelType(value uint8) bool {
	switch value {
	case ChannelPerson, ChannelGroup, ChannelCustomer, ChannelCommunity, ChannelCommunityTopic, ChannelInfo, ChannelLive, ChannelVisitor:
		return true
	default:
		return false
	}
}

func (c *Client) post(ctx context.Context, baseURL, path string, input, output any, retry bool) error {
	return c.do(ctx, http.MethodPost, baseURL, path, input, output, retry)
}

func (c *Client) do(ctx context.Context, method, baseURL, path string, input, output any, retry bool) error {
	var payload []byte
	var err error
	if input != nil {
		payload, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	attempts := 1
	if retry {
		attempts += c.maxRetries
	}
	for attempt := 0; attempt < attempts; attempt++ {
		request, requestErr := http.NewRequestWithContext(ctx, method, baseURL+path, bytes.NewReader(payload))
		if requestErr != nil {
			return requestErr
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("token", c.managerToken)
		response, requestErr := c.http.Do(request)
		if requestErr != nil {
			if attempt+1 < attempts && ctx.Err() == nil {
				if err = waitRetry(ctx, attempt, ""); err == nil {
					continue
				}
			}
			return requestErr
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			if output == nil || len(bytes.TrimSpace(body)) == 0 {
				return nil
			}
			if err = json.Unmarshal(body, output); err != nil {
				return fmt.Errorf("decode WuKongIM response: %w", err)
			}
			return nil
		}
		httpErr := &HTTPError{StatusCode: response.StatusCode, Method: method, Path: path, Body: strings.TrimSpace(string(body))}
		if attempt+1 < attempts && (response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500) {
			if err = waitRetry(ctx, attempt, response.Header.Get("Retry-After")); err == nil {
				continue
			}
		}
		return httpErr
	}
	return errors.New("WuKongIM request failed after retries")
}

func waitRetry(ctx context.Context, attempt int, retryAfter string) error {
	delay := time.Duration(100*(1<<min(attempt, 4))) * time.Millisecond
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds >= 0 && seconds <= 30 {
		delay = time.Duration(seconds) * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
