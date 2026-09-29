package push

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linli/im/server/internal/store"
)

func TestGetuiChecksEveryProviderRequest(t *testing.T) {
	for _, failAt := range []int{1, 2, 3, 4} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			var checked, wire atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire.Add(1)
				if strings.HasSuffix(r.URL.Path, "/auth") {
					writeGetui(w, 0, map[string]any{"token": "synthetic-token", "expire_time": time.Now().Add(time.Hour).UnixMilli()})
				} else {
					writeGetui(w, 10001, map[string]any{})
				}
			}))
			defer server.Close()
			provider := &Getui{AppID: "test", AppKey: "test-key", MasterSecret: "synthetic", BaseURL: server.URL, Client: server.Client()}
			ctx := WithSubmissionCheck(t.Context(), func(context.Context) error {
				if checked.Add(1) == int32(failAt) {
					return errors.New("private database detail")
				}
				return nil
			})
			item := store.OutboxItem{ID: 1, EventType: "message.created", Devices: []store.Device{{ID: "binding", Provider: "getui", PushToken: "synthetic-device"}}}
			e := provider.Send(ctx, item)
			if e == nil || strings.Contains(e.Error(), "private database") || checked.Load() != int32(failAt) || wire.Load() != int32(failAt-1) {
				t.Fatal("submission gate bypassed", checked.Load(), wire.Load(), e)
			}
		})
	}
}

func TestAPNSChecksBeforeWire(t *testing.T) {
	key, _ := testAPNSKey(t)
	provider, e := NewAPNSVoIP("synthetic-key", "synthetic-team", "test.app", false, key)
	if e != nil {
		t.Fatal(e)
	}
	var wire atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { wire.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	provider.BaseURL, provider.Client = server.URL, server.Client()
	checked := false
	ctx := WithSubmissionCheck(t.Context(), func(context.Context) error { checked = true; return errors.New("lost lock") })
	item := testCallOutbox()
	item.Devices = []store.Device{{ID: "ios", Provider: "apns_voip", PushToken: strings.Repeat("a", 64)}}
	if e := provider.Send(ctx, item); e == nil || !checked || wire.Load() != 0 {
		t.Fatal("APNs escaped guard", e, checked, wire.Load())
	}
}

func TestWebPushChecksAfterEncodingBeforeWire(t *testing.T) {
	provider, token := testWebPushProvider(t, "https://push.example.com/synthetic", nil)
	wire := 0
	provider.Client = webPushHTTPFunc(func(*http.Request) (*http.Response, error) { wire++; return nil, errors.New("must not send") })
	checked := false
	ctx := WithSubmissionCheck(t.Context(), func(context.Context) error { checked = true; return errors.New("lost lock") })
	item := store.OutboxItem{ID: 1, EventType: "message.created", Devices: []store.Device{{ID: "binding", Provider: "webpush", PushToken: token}}}
	if e := provider.Send(ctx, item); e == nil || !checked || wire != 0 {
		t.Fatal("Web Push escaped guard", e, checked, wire)
	}
}

func TestSubmissionCancellationAndMissingGuard(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if checkSubmission(ctx) == nil {
		t.Fatal("cancelled legacy request accepted")
	}
	if checkSubmission(t.Context()) != nil {
		t.Fatal("legacy requests require a platform guard")
	}
	if checkSubmission(WithSubmissionCheck(t.Context(), nil)) == nil {
		t.Fatal("nil platform guard accepted")
	}
}
