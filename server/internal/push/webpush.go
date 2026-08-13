package push

import (
	"context"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/linli/im/server/internal/store"
)

const maxWebPushSubscriptionBytes = 8192

// WebPush delivers encrypted notifications to standards-compliant browser
// subscriptions. The public key is exposed to clients; the private key never
// leaves this provider.
type WebPush struct {
	PublicKey, PrivateKey, Subject string
	Client                         webpush.HTTPClient
}

func (w *WebPush) Send(ctx context.Context, item store.OutboxItem) error {
	if w == nil || w.PublicKey == "" || w.PrivateKey == "" || w.Subject == "" {
		return permanentDeliveryError(errors.New("Web Push provider is not configured"))
	}
	var joined []error
	var invalid []string
	retryable := false
	invalidOnly := true
	for _, device := range item.Devices {
		if device.Provider != "webpush" || strings.TrimSpace(device.PushToken) == "" {
			continue
		}
		if err := w.sendDevice(ctx, item, device); err != nil {
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
		Err: errors.Join(joined...), InvalidDeviceIDs: uniqueStrings(invalid),
		Retryable: retryable, InvalidOnly: invalidOnly,
	}
}

func (w *WebPush) sendDevice(ctx context.Context, item store.OutboxItem, device store.Device) error {
	subscription, err := ParseWebPushSubscription(device.PushToken)
	if err != nil {
		return invalidWebPushSubscriptionDeliveryError(device.ID)
	}
	title, body, navigation := getuiNotification(item)
	if !device.PreviewEnabled {
		body = "你收到一条新消息"
	}
	payload, err := json.Marshal(map[string]any{
		"title":   title,
		"body":    body,
		"tag":     "linli-" + webPushTopic(item),
		"icon":    "icons/Icon-192.png",
		"badge":   "icons/Icon-192.png",
		"silent":  !device.SoundEnabled,
		"vibrate": device.VibrationEnabled,
		"data":    navigation,
	})
	if err != nil {
		return permanentDeliveryError(errors.New("Web Push payload cannot be encoded"))
	}
	urgency := webpush.UrgencyNormal
	if item.EventType == "call.invited" {
		urgency = webpush.UrgencyHigh
	}
	client := w.Client
	if client == nil {
		client = newRestrictedWebPushClient()
	}
	response, err := webpush.SendNotificationWithContext(ctx, payload, subscription, &webpush.Options{
		HTTPClient: client, Subscriber: w.Subject,
		VAPIDPublicKey: w.PublicKey, VAPIDPrivateKey: w.PrivateKey,
		TTL: 72 * 60 * 60, Urgency: urgency, Topic: webPushTopic(item),
	})
	if err != nil {
		return retryableDeliveryError(errors.New("Web Push request failed"))
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return nil
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone:
		return invalidWebPushSubscriptionDeliveryError(device.ID)
	case response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		return retryableDeliveryError(fmt.Errorf("Web Push temporary failure: HTTP %d", response.StatusCode))
	default:
		return permanentDeliveryError(fmt.Errorf("Web Push rejected request: HTTP %d", response.StatusCode))
	}
}

func ParseWebPushSubscription(raw string) (*webpush.Subscription, error) {
	if len(raw) == 0 || len(raw) > maxWebPushSubscriptionBytes {
		return nil, errors.New("invalid Web Push subscription size")
	}
	var subscription webpush.Subscription
	if err := json.Unmarshal([]byte(raw), &subscription); err != nil {
		return nil, errors.New("invalid Web Push subscription")
	}
	endpoint, err := url.Parse(subscription.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || len(subscription.Endpoint) > 4096 {
		return nil, errors.New("invalid Web Push endpoint")
	}
	p256dh, err := decodeWebPushKey(subscription.Keys.P256dh)
	if err != nil || len(p256dh) != 65 {
		return nil, errors.New("invalid Web Push p256dh key")
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), p256dh)
	if x == nil || y == nil {
		return nil, errors.New("invalid Web Push p256dh point")
	}
	auth, err := decodeWebPushKey(subscription.Keys.Auth)
	if err != nil || len(auth) != 16 {
		return nil, errors.New("invalid Web Push auth key")
	}
	return &subscription, nil
}

func decodeWebPushKey(value string) ([]byte, error) {
	if value == "" || len(value) > 256 {
		return nil, errors.New("invalid Web Push key")
	}
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
}

func webPushTopic(item store.OutboxItem) string {
	key := item.UserID + ":" + item.EventType
	if conversationID := stringValue(item.Payload["conversationId"]); conversationID != "" {
		key = item.UserID + ":" + conversationID
	} else if message, ok := item.Payload["message"].(map[string]any); ok {
		if conversationID := stringValue(message["conversationId"]); conversationID != "" {
			key = item.UserID + ":" + conversationID
		}
	}
	digest := sha256.Sum256([]byte(key))
	return base64.RawURLEncoding.EncodeToString(digest[:18])
}

func newRestrictedWebPushClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid Web Push address")
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, errors.New("Web Push endpoint lookup failed")
		}
		for _, address := range addresses {
			ip := address.IP
			if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		return nil, errors.New("Web Push endpoint resolved only to non-public addresses")
	}
	return &http.Client{
		Transport: transport,
		Timeout:   12 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("Web Push redirects are forbidden")
		},
	}
}
