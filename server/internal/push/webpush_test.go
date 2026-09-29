package push

import (
	"bytes"
	"context"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/webpushpolicy"
)

type webPushHTTPFunc func(*http.Request) (*http.Response, error)

func (f webPushHTTPFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestPlatformWebPushEncryptedScopeTTLAndRejection(t *testing.T) {
	provider, token := testWebPushProvider(t, "https://push.example.com/private-subscription", nil)
	policy, e := webpushpolicy.New(provider.PublicKey, provider.PrivateKey, provider.Subject, "push.example.com")
	if e != nil {
		t.Fatal(e)
	}
	provider.Policy = policy
	calls := 0
	status := 201
	provider.Client = webPushHTTPFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		ttl, e := strconv.Atoi(r.Header.Get("TTL"))
		if e != nil || ttl < 1 || ttl > 60 || r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("Authorization") == "" {
			t.Error("missing encryption or excessive TTL")
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 || bytes.Contains(body, []byte("tenant-private")) || bytes.Contains(body, []byte("binding-private")) {
			t.Error("unencrypted scope")
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("private provider error"))}, nil
	})
	item := store.OutboxItem{UserID: "user", EventType: "message.created", Payload: map[string]any{
		"tenantId": "tenant-private", "localUserId": "user", "assignmentVersion": 1, "authVersion": 2, "realmVersion": 3,
		"pushBindingId": "binding-private", "pushBindingRevision": 1, "expiresAt": time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano), "conversationId": "conversation",
	}, Devices: []store.Device{{ID: "binding-private", Platform: "web", Provider: "webpush", PushToken: token}}}
	if e = provider.Send(t.Context(), item); e != nil || calls != 1 {
		t.Fatal("valid managed delivery", e)
	}
	_, _, navigation := getuiNotification(item)
	if navigation["pushBindingId"] != "binding-private" || navigation["pushBindingRevision"] != int64(1) {
		t.Fatal("missing binding fence")
	}
	originalTopic := webPushTopic(item)
	item.Payload["pushBindingId"] = "other-binding"
	if webPushTopic(item) == originalTopic {
		t.Fatal("topics collide across bindings")
	}
	if e = provider.Send(t.Context(), item); e == nil || calls != 1 {
		t.Fatal("mismatching device sent")
	}
	item.Payload["pushBindingId"] = "binding-private"
	for _, field := range []string{"tenantId", "authVersion", "pushBindingRevision", "expiresAt"} {
		saved := item.Payload[field]
		delete(item.Payload, field)
		if e = provider.Send(t.Context(), item); e == nil || calls != 1 {
			t.Fatal("incomplete platform scope sent", field)
		}
		item.Payload[field] = saved
	}
	item.Payload["expiresAt"] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	if e = provider.Send(t.Context(), item); e == nil || calls != 1 {
		t.Fatal("expired scope sent")
	}
	item.Payload["expiresAt"] = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	for _, code := range []int{404, 410, 429, 503, 403} {
		status = code
		e = provider.Send(t.Context(), item)
		var d *DeliveryError
		if !errors.As(e, &d) || d.InvalidOnly != (code == 404 || code == 410) || d.Retryable != (code == 429 || code == 503) || strings.Contains(e.Error(), "private") {
			t.Fatal("unsafe classification", code)
		}
	}
}

func TestRestrictedWebPushTransport(t *testing.T) {
	client := newRestrictedWebPushClient(&webpushpolicy.Policy{Hosts: map[string]bool{"127.0.0.1": true}})
	if client.CheckRedirect(nil, nil) == nil || client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("redirect/proxy boundary missing")
	}
	for _, address := range []string{"127.0.0.1:443", "denied.example:443", "127.0.0.1:80"} {
		if conn, e := client.Transport.(*http.Transport).DialContext(t.Context(), "tcp", address); e == nil {
			conn.Close()
			t.Fatal("unsafe destination dialled")
		}
	}
}

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
