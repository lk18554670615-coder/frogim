package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/linli/im/server/internal/store"
)

func TestAPNSVoIPSendsHTTP2PrivacySafeCallPayloadAndCachesJWT(t *testing.T) {
	keyPEM, publicKey := testAPNSKey(t)
	var mu sync.Mutex
	var authorizations []string
	var requestCount int
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Fatalf("APNs request protocol=%s", r.Proto)
		}
		if r.Header.Get("apns-push-type") != "voip" || r.Header.Get("apns-topic") != "com.linlitong.imapp.voip" || r.Header.Get("apns-expiration") != "0" || r.Header.Get("apns-priority") != "10" {
			t.Fatalf("invalid APNs headers: %v", r.Header)
		}
		if !strings.HasPrefix(r.URL.Path, "/3/device/") || len(r.Header.Get("apns-id")) != 36 {
			t.Fatalf("path=%q apns-id=%q", r.URL.Path, r.Header.Get("apns-id"))
		}
		authorization := strings.TrimPrefix(r.Header.Get("authorization"), "bearer ")
		parsed, err := jwt.Parse(authorization, func(token *jwt.Token) (any, error) { return publicKey, nil }, jwt.WithValidMethods([]string{"ES256"}))
		if err != nil || !parsed.Valid || parsed.Header["kid"] != "KEYID12345" {
			t.Fatalf("provider JWT invalid: kid=%v err=%v", parsed.Header["kid"], err)
		}
		claims := parsed.Claims.(jwt.MapClaims)
		if claims["iss"] != "TEAMID1234" {
			t.Fatalf("issuer=%v", claims["iss"])
		}
		var body map[string]any
		if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(body)
		if body["callId"] != "call-1" || body["conversationId"] != "conv-1" || body["mediaType"] != "video" || !strings.Contains(string(raw), "content-available") {
			t.Fatalf("payload=%s", raw)
		}
		for _, secret := range []string{"private caller", "13800000000", "verification text", "token-secret"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("private value %q leaked in payload: %s", secret, raw)
			}
		}
		mu.Lock()
		authorizations = append(authorizations, authorization)
		requestCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	provider, err := NewAPNSVoIP("KEYID12345", "TEAMID1234", "com.linlitong.imapp", true, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	provider.BaseURL = server.URL
	provider.Client = server.Client()
	provider.Now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	item := testCallOutbox()
	item.Payload["callerName"] = "private caller"
	item.Payload["phone"] = "13800000000"
	item.Payload["message"] = "verification text"
	item.Devices = []store.Device{
		{ID: "ios-1", Provider: "apns_voip", PushToken: strings.Repeat("a", 64)},
		{ID: "ios-2", Provider: "apns_voip", PushToken: strings.Repeat("b", 64)},
		{ID: "android", Provider: "getui", PushToken: "token-secret"},
	}
	if err = provider.Send(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if requestCount != 2 || len(authorizations) != 2 || authorizations[0] != authorizations[1] {
		t.Fatalf("requests=%d cached JWT=%v", requestCount, len(authorizations) == 2 && authorizations[0] == authorizations[1])
	}
}

func TestAPNSVoIPInvalidatesGoneTokenWithoutLeakingIt(t *testing.T) {
	keyPEM, _ := testAPNSKey(t)
	token := strings.Repeat("c", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		_ = json.NewEncoder(w).Encode(map[string]any{"reason": "Unregistered", "timestamp": 1_700_000_000_000})
	}))
	defer server.Close()
	provider, err := NewAPNSVoIP("KEYID12345", "TEAMID1234", "com.linlitong.imapp", false, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	provider.BaseURL, provider.Client = server.URL, server.Client()
	item := testCallOutbox()
	item.Devices = []store.Device{{ID: "gone-device", Provider: "apns_voip", PushToken: token}}
	err = provider.Send(context.Background(), item)
	var delivery *DeliveryError
	if !errors.As(err, &delivery) || delivery.Retryable || !delivery.InvalidOnly || len(delivery.InvalidDeviceIDs) != 1 || delivery.InvalidDeviceIDs[0] != "gone-device" {
		t.Fatalf("delivery error=%#v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("device token leaked: %v", err)
	}
}

func TestAPNSVoIPClassifiesTemporaryFailureForRetry(t *testing.T) {
	keyPEM, _ := testAPNSKey(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"reason": "Shutdown"})
	}))
	defer server.Close()
	provider, _ := NewAPNSVoIP("KEYID12345", "TEAMID1234", "com.linlitong.imapp", false, keyPEM)
	provider.BaseURL, provider.Client = server.URL, server.Client()
	item := testCallOutbox()
	item.Devices = []store.Device{{ID: "ios", Provider: "apns_voip", PushToken: strings.Repeat("d", 64)}}
	err := provider.Send(context.Background(), item)
	var delivery *DeliveryError
	if !errors.As(err, &delivery) || !delivery.Retryable || delivery.Permanent() {
		t.Fatalf("temporary error=%#v", err)
	}
}

func TestAPNSVoIPRejectsInvalidKeyAndMalformedToken(t *testing.T) {
	if _, err := NewAPNSVoIP("KEYID12345", "TEAMID1234", "com.linlitong.imapp", false, []byte("not-a-key")); err == nil {
		t.Fatal("invalid .p8 key must fail closed")
	}
	keyPEM, _ := testAPNSKey(t)
	provider, _ := NewAPNSVoIP("KEYID12345", "TEAMID1234", "com.linlitong.imapp", false, keyPEM)
	item := testCallOutbox()
	item.Devices = []store.Device{{ID: "bad", Provider: "apns_voip", PushToken: "not-a-device-token"}}
	err := provider.Send(context.Background(), item)
	var delivery *DeliveryError
	if !errors.As(err, &delivery) || !delivery.InvalidOnly || len(delivery.InvalidDeviceIDs) != 1 {
		t.Fatalf("malformed token error=%#v", err)
	}
}

func testAPNSKey(t *testing.T) ([]byte, *ecdsa.PublicKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), &key.PublicKey
}

func testCallOutbox() store.OutboxItem {
	return store.OutboxItem{ID: 41, EventType: "call.invited", Payload: map[string]any{"callId": "call-1", "conversationId": "conv-1", "mediaType": "video"}}
}
