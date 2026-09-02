package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/teststore"
)

func TestApplicationRateLimitUsesConfiguredPerIPQuota(t *testing.T) {
	for _, tc := range []struct {
		name              string
		configured, quota int
	}{
		{name: "default 30000", quota: 30000},
		{name: "explicit 30000", configured: 30000, quota: 30000},
		{name: "custom quota", configured: 3, quota: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := app.New(context.Background(), teststore.Memory{})
			if err != nil {
				t.Fatal(err)
			}
			api := New(config.Config{
				JWTSecret: "test-secret", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
				HTTPRateLimitPerMinute: tc.configured,
			}, a)
			api.mux.HandleFunc("GET /rate-limit-test", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
			h := api.Handler()
			request := func(ip, path string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodGet, path, nil)
				r.RemoteAddr = ip + ":1234"
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			for i := 1; i <= tc.quota; i++ {
				if w := request("203.0.113.10", "/rate-limit-test"); w.Code != http.StatusNoContent {
					t.Fatalf("request %d/%d returned %d", i, tc.quota, w.Code)
				}
			}
			w := request("203.0.113.10", "/rate-limit-test")
			if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "60" || !strings.Contains(w.Body.String(), "RATE_LIMITED") {
				t.Fatalf("quota boundary response=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
			}
			if w := request("203.0.113.11", "/rate-limit-test"); w.Code != http.StatusNoContent {
				t.Fatalf("other IP shares exhausted quota: %d", w.Code)
			}
			if w := request("203.0.113.10", "/health"); w.Code != http.StatusOK {
				t.Fatalf("health probe blocked by exhausted quota: %d", w.Code)
			}
		})
	}
}
