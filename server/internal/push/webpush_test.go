package push

import (
	"context"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/linli/im/server/internal/store"
)

func TestWebPushSendsEncryptedPrivacySafeNotification(t *testing.T) {
	const privateConversationID = "conversation-private-routing-marker-4f7d8b2c"
	var body []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") == "" || r.Header.Get("TTL") != "259200" {
			t.Errorf("unexpected Web Push request method=%s headers=%v", r.Method, r.Header)
		}
		if r.Header.Get("Content-Encoding") != "aes128gcm" {
			t.Errorf("content encoding=%q", r.Header.Get("Content-Encoding"))
		}
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	provider, token := testWebPushProvider(t, server.URL, server.Client())
	item := store.OutboxItem{
		ID: 7, UserID: "u1", EventType: "message.created",
		Payload: map[string]any{"message": map[string]any{
			"id": "m1", "conversationId": privateConversationID, "type": "text",
			"text": "private message body must not be forwarded",
		}},
		Devices: []store.Device{{
			ID: "web-1", Platform: "web", Provider: "webpush", PushToken: token,
			NotificationsEnabled: true, PreviewEnabled: false, SoundEnabled: false,
		}},
	}
	if err := provider.Send(context.Background(), item); err != nil {
		t.Fatalf("send Web Push: %v", err)
	}
	if len(body) == 0 || strings.Contains(string(body), "private message body") || strings.Contains(string(body), privateConversationID) {
		t.Fatalf("payload was not encrypted or leaked routing data: %q", body)
	}
}

func TestWebPushInvalidatesGoneSubscriptionWithoutLeakingEndpoint(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer server.Close()
	provider, token := testWebPushProvider(t, server.URL, server.Client())
	item := store.OutboxItem{
		ID: 8, UserID: "u1", EventType: "friend.request",
		Devices: []store.Device{{ID: "gone-web", Platform: "web", Provider: "webpush", PushToken: token}},
	}
	err := provider.Send(context.Background(), item)
	var delivery *DeliveryError
	if !errors.As(err, &delivery) || !delivery.InvalidOnly || len(delivery.InvalidDeviceIDs) != 1 || delivery.InvalidDeviceIDs[0] != "gone-web" {
		t.Fatalf("gone subscription classification=%#v err=%v", delivery, err)
	}
	if strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), token) {
		t.Fatalf("subscription leaked in error: %v", err)
	}
}

func TestParseWebPushSubscriptionRejectsMalformedValues(t *testing.T) {
	_, token := testWebPushProvider(t, "https://push.example.com/subscription", nil)
	if _, err := ParseWebPushSubscription(token); err != nil {
		t.Fatalf("valid subscription: %v", err)
	}
	for _, raw := range []string{
		`{"endpoint":"http://push.example.com","keys":{"p256dh":"x","auth":"y"}}`,
		`{"endpoint":"https://push.example.com","keys":{"p256dh":"x","auth":"y"}}`,
		strings.Repeat("x", maxWebPushSubscriptionBytes+1),
	} {
		if _, err := ParseWebPushSubscription(raw); err == nil {
			t.Fatalf("malformed subscription accepted: %.80q", raw)
		}
	}
}

func testWebPushProvider(t *testing.T, endpoint string, client webpush.HTTPClient) (*WebPush, string) {
	t.Helper()
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate VAPID keys: %v", err)
	}
	_, x, y, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate subscription key: %v", err)
	}
	auth := make([]byte, 16)
	if _, err = rand.Read(auth); err != nil {
		t.Fatalf("generate auth key: %v", err)
	}
	raw, err := json.Marshal(map[string]any{
		"endpoint": endpoint,
		"keys": map[string]string{
			"p256dh": base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), x, y)),
			"auth":   base64.RawURLEncoding.EncodeToString(auth),
		},
	})
	if err != nil {
		t.Fatalf("encode subscription: %v", err)
	}
	return &WebPush{PublicKey: publicKey, PrivateKey: privateKey, Subject: "https://chat.example.com", Client: client}, string(raw)
}
