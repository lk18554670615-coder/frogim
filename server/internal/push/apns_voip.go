package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/linli/im/server/internal/store"
)

const (
	apnsProductionURL = "https://api.push.apple.com"
	apnsSandboxURL    = "https://api.development.push.apple.com"
	apnsPayloadLimit  = 5 * 1024
)

var apnsDeviceTokenPattern = regexp.MustCompile(`^[0-9a-fA-F]{64,256}$`)

// APNSVoIP 使用 Apple Provider Token 向 PushKit 设备发送仅用于真实来电的 VoIP 推送。
// 私钥只保存在进程内存中，错误与日志永不包含私钥或设备 token。
type APNSVoIP struct {
	KeyID, TeamID, BundleID string
	Sandbox                 bool
	BaseURL                 string
	Client                  *http.Client
	Now                     func() time.Time
	privateKey              *ecdsa.PrivateKey

	mu          sync.Mutex
	providerJWT string
	jwtIssuedAt time.Time
}

type apnsErrorBody struct {
	Reason    string `json:"reason"`
	Timestamp int64  `json:"timestamp"`
}

func NewAPNSVoIP(keyID, teamID, bundleID string, sandbox bool, privateKeyPEM []byte) (*APNSVoIP, error) {
	key, err := jwt.ParseECPrivateKeyFromPEM(privateKeyPEM)
	if err != nil || key.Curve.Params().Name != "P-256" {
		return nil, errors.New("APNs VoIP private key must be a valid P-256 .p8 key")
	}
	return &APNSVoIP{
		KeyID: strings.TrimSpace(keyID), TeamID: strings.TrimSpace(teamID), BundleID: strings.TrimSpace(bundleID),
		Sandbox: sandbox, privateKey: key,
	}, nil
}

func (a *APNSVoIP) Send(ctx context.Context, item store.OutboxItem) error {
	if item.EventType != "call.invited" {
		return nil
	}
	if a == nil || a.privateKey == nil || a.KeyID == "" || a.TeamID == "" || a.BundleID == "" {
		return permanentDeliveryError(errors.New("APNs VoIP provider is not configured"))
	}
	payload, err := apnsVoIPPayload(item)
	if err != nil {
		return permanentDeliveryError(err)
	}
	var invalidDeviceIDs []string
	var deliveryErrors []error
	retryable := false
	for _, device := range item.Devices {
		if device.Provider != "apns_voip" || strings.TrimSpace(device.PushToken) == "" {
			continue
		}
		outcome := a.sendDevice(ctx, item.ID, device, payload)
		if outcome == nil {
			continue
		}
		var classified *DeliveryError
		if errors.As(outcome, &classified) {
			invalidDeviceIDs = append(invalidDeviceIDs, classified.InvalidDeviceIDs...)
			retryable = retryable || classified.Retryable
		}
		deliveryErrors = append(deliveryErrors, outcome)
	}
	if len(deliveryErrors) == 0 {
		return nil
	}
	return &DeliveryError{
		Err:              errors.Join(deliveryErrors...),
		InvalidDeviceIDs: uniqueStrings(invalidDeviceIDs),
		Retryable:        retryable,
		InvalidOnly:      len(invalidDeviceIDs) == len(deliveryErrors),
	}
}

func (a *APNSVoIP) sendDevice(ctx context.Context, outboxID int64, device store.Device, payload []byte) error {
	token := strings.TrimSpace(device.PushToken)
	if len(token)%2 != 0 || !apnsDeviceTokenPattern.MatchString(token) {
		return invalidTokenDeliveryError(device.ID, "BadDeviceToken")
	}
	providerToken, err := a.token()
	if err != nil {
		return permanentDeliveryError(err)
	}
	base := strings.TrimRight(a.BaseURL, "/")
	if base == "" {
		if a.Sandbox {
			base = apnsSandboxURL
		} else {
			base = apnsProductionURL
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/3/device/"+token, bytes.NewReader(payload))
	if err != nil {
		return permanentDeliveryError(errors.New("failed to create APNs VoIP request"))
	}
	req.Header.Set("authorization", "bearer "+providerToken)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("apns-push-type", "voip")
	req.Header.Set("apns-topic", a.BundleID+".voip")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-expiration", "0")
	req.Header.Set("apns-id", apnsRequestID(outboxID, device.ID))
	res, err := a.client().Do(req)
	if err != nil {
		return retryableDeliveryError(errors.New("APNs VoIP request failed"))
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 8<<10))
		return nil
	}
	var response apnsErrorBody
	_ = json.NewDecoder(io.LimitReader(res.Body, 8<<10)).Decode(&response)
	reason := safeAPNSReason(response.Reason)
	switch {
	case res.StatusCode == http.StatusGone,
		res.StatusCode == http.StatusBadRequest && (reason == "BadDeviceToken" || reason == "DeviceTokenNotForTopic"):
		return invalidTokenDeliveryError(device.ID, reason)
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode == http.StatusInternalServerError || res.StatusCode == http.StatusServiceUnavailable:
		return retryableDeliveryError(fmt.Errorf("APNs VoIP temporary failure: HTTP %d reason %s", res.StatusCode, reason))
	default:
		return permanentDeliveryError(fmt.Errorf("APNs VoIP rejected request: HTTP %d reason %s", res.StatusCode, reason))
	}
}

func (a *APNSVoIP) token() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}
	// Apple 要求 token 不超过一小时，同时不要在同一连接上短于 20 分钟频繁刷新。
	if a.providerJWT != "" && !now.Before(a.jwtIssuedAt) && now.Sub(a.jwtIssuedAt) < 50*time.Minute {
		return a.providerJWT, nil
	}
	claims := jwt.MapClaims{"iss": a.TeamID, "iat": now.Unix()}
	value := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	value.Header["kid"] = a.KeyID
	signed, err := value.SignedString(a.privateKey)
	if err != nil {
		return "", errors.New("failed to sign APNs provider token")
	}
	a.providerJWT, a.jwtIssuedAt = signed, now
	return signed, nil
}

func (a *APNSVoIP) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	transport.MaxIdleConnsPerHost = 20
	a.Client = &http.Client{Transport: transport, Timeout: 10 * time.Second}
	return a.Client
}

func apnsVoIPPayload(item store.OutboxItem) ([]byte, error) {
	callID := boundedRoutingID(stringValue(item.Payload["callId"]))
	conversationID := boundedRoutingID(stringValue(item.Payload["conversationId"]))
	if callID == "" || conversationID == "" {
		return nil, errors.New("APNs VoIP call payload is missing routing identifiers")
	}
	mediaType := stringValue(item.Payload["mediaType"])
	if mediaType != "video" {
		mediaType = "audio"
	}
	// 不发送用户昵称、手机号、消息正文或凭据；客户端醒来后向服务端读取权威通话数据。
	payload := map[string]any{
		"aps":            map[string]any{"content-available": 1},
		"callId":         callID,
		"conversationId": conversationID,
		"mediaType":      mediaType,
		"nameCaller":     "青蛙呱呱联系人",
		"handle":         "青蛙呱呱",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("failed to encode APNs VoIP payload")
	}
	if len(raw) > apnsPayloadLimit {
		return nil, errors.New("APNs VoIP payload exceeds 5 KiB")
	}
	return raw, nil
}

func boundedRoutingID(value string) string {
	if value == "" || len(value) > 160 {
		return ""
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7e {
			return ""
		}
	}
	return value
}

func apnsRequestID(outboxID int64, deviceID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", outboxID, deviceID)))
	hexValue := hex.EncodeToString(sum[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexValue[:8], hexValue[8:12], hexValue[12:16], hexValue[16:20], hexValue[20:32])
}

func safeAPNSReason(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 80 {
		return "Unknown"
	}
	for _, r := range value {
		if !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
			return "Unknown"
		}
	}
	return value
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
