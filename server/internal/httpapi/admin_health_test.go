package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/teststore"
)

func TestAdminHealthReportsRealtimeTCPFailureInsteadOfFalseHealthy(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()
	a, err := app.New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	x := New(config.Config{
		JWTSecret: strings.Repeat("j", 32), AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
		WukongEnabled: true, WukongAPIURL: upstream.URL, WukongManagerURL: upstream.URL,
		WukongManagerToken: "manager-secret", WukongTokenSecret: strings.Repeat("t", 32),
		WukongTCPURL: "tcp://" + tcpListener.Addr().String(), WukongWSURL: "ws://127.0.0.1:5200",
	}, a)

	items := readAdminHealth(t, x)
	assertAdminServiceStatus(t, items, "PostgreSQL 数据库", "healthy")
	assertAdminServiceStatus(t, items, "WuKongIM 实时消息", "healthy")
	assertAdminServiceStatus(t, items, "WuKongIM 管理接口", "healthy")

	if err = tcpListener.Close(); err != nil {
		t.Fatal(err)
	}
	items = readAdminHealth(t, x)
	assertAdminServiceStatus(t, items, "WuKongIM 实时消息", "down")
	assertAdminServiceStatus(t, items, "WuKongIM 管理接口", "healthy")
}

func readAdminHealth(t *testing.T, api *API) []adminHealthService {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/v2/admin/health", nil)
	recorder := httptest.NewRecorder()
	api.adminHealth(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin health status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Items []adminHealthService `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Items
}

func assertAdminServiceStatus(t *testing.T, items []adminHealthService, name, status string) {
	t.Helper()
	for _, item := range items {
		if item.Name == name {
			if item.Status != status {
				t.Fatalf("service %q status=%q want=%q", name, item.Status, status)
			}
			return
		}
	}
	t.Fatalf("service %q missing from %+v", name, items)
}
