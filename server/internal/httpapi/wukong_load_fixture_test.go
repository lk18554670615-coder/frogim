package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/teststore"
)

func TestWukongLoadFixtureIsDevOnlyAndReturnsBusinessPairs(t *testing.T) {
	var provisions atomic.Int64
	var allowlists atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/token":
			provisions.Add(1)
		case "/channel/whitelist_add":
			allowlists.Add(1)
		default:
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	application, err := app.New(t.Context(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("p", 32)
	cfg := config.Config{
		JWTSecret: strings.Repeat("j", 32), DevMode: true, DevOTPCode: "654321",
		AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
		WukongEnabled: true, WukongAPIURL: upstream.URL, WukongManagerURL: upstream.URL,
		WukongManagerToken: "manager-secret", WukongTokenSecret: strings.Repeat("t", 32),
		WukongPolicySecret: secret, WukongTCPURL: "tcp://im:5100", WukongWSURL: "ws://im:5200",
	}
	server := httptest.NewServer(New(cfg, application).Handler())
	body := []byte(`{"runId":"fixture_test","pairs":2}`)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/internal/wukong/load/pairs", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(wukongPolicySecretHeader, secret)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		RunID string `json:"runId"`
		Pairs []struct {
			Sender   struct{ UID, Token string } `json:"sender"`
			Receiver struct{ UID, Token string } `json:"receiver"`
		} `json:"pairs"`
	}
	if err = json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	server.Close()
	if response.StatusCode != http.StatusOK || decoded.RunID != "fixture_test" || len(decoded.Pairs) != 2 {
		t.Fatalf("status=%d response=%+v", response.StatusCode, decoded)
	}
	if provisions.Load() != 4 {
		t.Fatalf("WuKong provisions=%d, want 4", provisions.Load())
	}
	if allowlists.Load() != 4 {
		t.Fatalf("WuKong allowlist stabilizations=%d, want 4", allowlists.Load())
	}
	for index, pair := range decoded.Pairs {
		if pair.Sender.UID == "" || pair.Sender.Token == "" || pair.Receiver.UID == "" || pair.Receiver.Token == "" {
			t.Fatalf("pair %d is incomplete: %+v", index, pair)
		}
		if friends := application.Friends(pair.Sender.UID); len(friends) != 1 || friends[0].ID != pair.Receiver.UID {
			t.Fatalf("pair %d friendship=%+v", index, friends)
		}
	}

	cfg.DevMode = false
	production := httptest.NewServer(New(cfg, application).Handler())
	defer production.Close()
	request, _ = http.NewRequest(http.MethodPost, production.URL+"/internal/wukong/load/pairs", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(wukongPolicySecretHeader, secret)
	response, err = production.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("production fixture status=%d, want 404", response.StatusCode)
	}
}
