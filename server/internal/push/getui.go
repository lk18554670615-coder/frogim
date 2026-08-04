package push

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/linli/im/server/internal/store"
)

const getuiDefaultBaseURL = "https://restapi.getui.com/v2"

type Getui struct {
	AppID, AppKey, MasterSecret string
	BaseURL                     string
	Client                      *http.Client
	// 联合 APNs VoIP 模式下，iOS 来电只走 PushKit，避免再出现一条普通通知。
	SuppressIOSCallsWithVoIP bool

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

type getuiResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (g *Getui) Send(ctx context.Context, item store.OutboxItem) error {
	if g == nil || g.AppID == "" || g.AppKey == "" || g.MasterSecret == "" {
		return errors.New("getui provider is not configured")
	}
	hasVoIPDevice := false
	if g.SuppressIOSCallsWithVoIP && item.EventType == "call.invited" {
		for _, device := range item.Devices {
			if device.Provider == "apns_voip" && strings.TrimSpace(device.PushToken) != "" {
				hasVoIPDevice = true
				break
			}
		}
	}
	var joined []error
	var invalid []string
	retryable := false
	invalidOnly := true
	for _, device := range item.Devices {
		if device.Provider != "getui" || strings.TrimSpace(device.PushToken) == "" {
			continue
		}
		if hasVoIPDevice && strings.EqualFold(device.Platform, "ios") {
			continue
		}
		if err := g.sendCID(ctx, item, device); err != nil {
			joined = append(joined, err)
			var delivery *DeliveryError
			if errors.As(err, &delivery) {
				invalid = append(invalid, delivery.InvalidDeviceIDs...)
				retryable = retryable || delivery.Retryable
				invalidOnly = invalidOnly && delivery.InvalidOnly
			} else {
				retryable = true
				invalidOnly = false
			}
		}
	}
	if len(joined) == 0 {
		return nil
	}
	return &DeliveryError{
		Err:              errors.Join(joined...),
		InvalidDeviceIDs: uniqueStrings(invalid),
		Retryable:        retryable,
		InvalidOnly:      invalidOnly,
	}
}

func (g *Getui) sendCID(ctx context.Context, item store.OutboxItem, device store.Device) error {
	token, err := g.accessToken(ctx, false)
	if err != nil {
		return err
	}
	title, summary, navigation := getuiNotification(item)
	if !device.PreviewEnabled {
		summary = "你收到一条新消息"
	}
	payload, err := json.Marshal(navigation)
	if err != nil {
		return err
	}
	requestID := getuiRequestID(item.ID, device.ID)
	iosAPS := map[string]any{
		"alert":             map[string]any{"title": title, "body": summary},
		"content-available": 0,
	}
	if device.SoundEnabled {
		iosAPS["sound"] = "default"
	}
	body := map[string]any{
		"request_id": requestID,
		"settings":   map[string]any{"ttl": 72 * 60 * 60 * 1000},
		"audience":   map[string]any{"cid": []string{device.PushToken}},
		"push_message": map[string]any{
			"transmission": string(payload),
		},
		"push_channel": map[string]any{
			"android": map[string]any{
				"ups": map[string]any{
					"notification": map[string]any{"title": title, "body": summary, "click_type": "payload", "payload": string(payload)},
				},
			},
			"ios": map[string]any{
				"type": "notify", "payload": string(payload), "auto_badge": "+1",
				"aps": iosAPS,
			},
		},
	}
	code, message, err := g.call(ctx, http.MethodPost, "/push/single/cid", token, body, nil)
	if err != nil {
		return err
	}
	if code == 10001 {
		token, err = g.accessToken(ctx, true)
		if err != nil {
			return err
		}
		code, message, err = g.call(ctx, http.MethodPost, "/push/single/cid", token, body, nil)
		if err != nil {
			return err
		}
	}
	if code != 0 {
		if getuiInvalidCID(code, message) {
			return invalidGetuiCIDDeliveryError(device.ID)
		}
		return fmt.Errorf("getui push rejected with code %d", code)
	}
	return nil
}

// getuiNotification deliberately excludes message text, friend verification
// text, file names and other user-authored fields. The notification is only a
// safe preview plus routing identifiers; the client fetches authoritative data
// through sync after opening it.
func getuiNotification(item store.OutboxItem) (string, string, map[string]any) {
	payload := item.Payload
	navigation := map[string]any{"type": item.EventType, "unread": 1, "badge": "+1"}
	for _, key := range []string{"conversationId", "messageId", "requestId", "announcementId", "callId", "mediaType"} {
		if value := stringValue(payload[key]); value != "" {
			navigation[key] = value
		}
	}
	if raw, ok := payload["unreadCount"].(float64); ok && raw >= 0 {
		navigation["unread"] = int64(raw)
	}
	if message, ok := payload["message"].(map[string]any); ok {
		for _, key := range []string{"conversationId", "id"} {
			if value := stringValue(message[key]); value != "" {
				if key == "id" {
					navigation["messageId"] = value
				} else {
					navigation[key] = value
				}
			}
		}
		if messageType := stringValue(message["type"]); messageType != "" {
			navigation["messageType"] = messageType
		}
	}
	switch item.EventType {
	case "message.created":
		return "邻里通讯", messageSummary(navigation["messageType"]), navigation
	case "friend.request":
		return "新的好友请求", "你收到一条好友申请", navigation
	case "announcement.published":
		title := safePushText(stringValue(payload["title"]), 40)
		if title == "" {
			title = "平台公告"
		}
		body := safePushText(stringValue(payload["content"]), 100)
		if body == "" {
			body = "你有一条新的平台公告"
		}
		return title, body, navigation
	case "call.invited":
		if navigation["mediaType"] == "video" {
			return "视频通话", "你收到一个视频通话邀请", navigation
		}
		return "语音通话", "你收到一个语音通话邀请", navigation
	default:
		return "邻里通讯", "你有一条新通知", navigation
	}
}

func messageSummary(value any) string {
	switch stringValue(value) {
	case "image":
		return "你收到一张图片"
	case "audio":
		return "你收到一条语音"
	case "video":
		return "你收到一段视频"
	case "file":
		return "你收到一个文件"
	case "location":
		return "你收到一个位置"
	case "contact":
		return "你收到一张联系人名片"
	default:
		return "你收到一条新消息"
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func safePushText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit]) + "…"
	}
	return value
}

func (g *Getui) accessToken(ctx context.Context, force bool) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !force && g.token != "" && time.Until(g.expiresAt) > time.Minute {
		return g.token, nil
	}
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	sum := sha256.Sum256([]byte(g.AppKey + timestamp + g.MasterSecret))
	request := map[string]string{"sign": hex.EncodeToString(sum[:]), "timestamp": timestamp, "appkey": g.AppKey}
	var data struct {
		Token      string          `json:"token"`
		ExpireTime json.RawMessage `json:"expire_time"`
	}
	code, _, err := g.call(ctx, http.MethodPost, "/auth", "", request, &data)
	if err != nil {
		return "", err
	}
	if code != 0 || data.Token == "" {
		return "", fmt.Errorf("getui authentication rejected with code %d", code)
	}
	expires, err := parseGetuiMillis(data.ExpireTime)
	if err != nil {
		return "", errors.New("getui authentication returned an invalid expiry")
	}
	g.token, g.expiresAt = data.Token, time.UnixMilli(expires)
	return g.token, nil
}

func (g *Getui) call(ctx context.Context, method, path, token string, body, data any) (int, string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, "", err
	}
	base := strings.TrimRight(g.BaseURL, "/")
	if base == "" {
		base = getuiDefaultBaseURL
	}
	url := base + "/" + g.AppID + path
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(raw))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json;charset=utf-8")
	if token != "" {
		req.Header.Set("token", token)
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("getui request failed: %w", err)
	}
	defer res.Body.Close()
	var response getuiResponse
	decoder := json.NewDecoder(io.LimitReader(res.Body, 64<<10))
	decodeErr := decoder.Decode(&response)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// 个推业务错误（包括失效 CID）可能使用 4xx HTTP 状态返回，
		// 因此先保留已成功解析的业务码，再由上层进行安全分类。
		if decodeErr == nil && response.Code != 0 {
			return response.Code, response.Msg, nil
		}
		return 0, "", fmt.Errorf("getui request returned HTTP %d", res.StatusCode)
	}
	if decodeErr != nil {
		return 0, "", errors.New("getui returned an invalid response")
	}
	if data != nil && len(response.Data) > 0 && string(response.Data) != "null" {
		if err = json.Unmarshal(response.Data, data); err != nil {
			return 0, "", errors.New("getui returned invalid response data")
		}
	}
	return response.Code, response.Msg, nil
}

func getuiInvalidCID(code int, message string) bool {
	if code != 20001 {
		return false
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(message), " "))
	return strings.Contains(normalized, "target user is invalid")
}

func getuiRequestID(outboxID int64, deviceID string) string {
	sum := sha256.Sum256([]byte(strconv.FormatInt(outboxID, 10) + ":" + deviceID))
	return "im" + hex.EncodeToString(sum[:])[:28]
}

func parseGetuiMillis(raw json.RawMessage) (int64, error) {
	value := strings.Trim(string(raw), `"`)
	return strconv.ParseInt(value, 10, 64)
}
