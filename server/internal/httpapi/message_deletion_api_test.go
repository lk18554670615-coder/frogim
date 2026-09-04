package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/teststore"
)

func TestMessageDeletionRequestBoundaryAndNoSelfElevation(t *testing.T) {
	a, err := app.New(t.Context(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: strings.Repeat("d", 32), DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	api := New(cfg, a)
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()
	token := loginToken(t, ts.URL, "13800000001")
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = "100"
	}
	oversized, _ := json.Marshal(map[string]any{"conversationId": "c", "messageIds": tooMany, "confirmed": true})
	for _, body := range []string{
		`{"conversationId":"c","messageIds":["100"]}`,
		`{"conversationId":"c","messageIds":[],"confirmed":true}`,
		`{"messageIds":["100"],"confirmed":true}`,
		string(oversized),
	} {
		response := authenticatedRequest(t, http.MethodPost, ts.URL+"/v2/messages/delete-for-everyone", token, body)
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("invalid batch status=%d", response.StatusCode)
		}
	}
	response := authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/users/me", token, `{"canDeleteMessagesForEveryone":true}`)
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatalf("self elevation status=%d", response.StatusCode)
	}
	response = authenticatedRequest(t, http.MethodGet, ts.URL+"/v2/users/me", token, "")
	var profile struct {
		Allowed bool `json:"canDeleteMessagesForEveryone"`
	}
	if err := json.NewDecoder(response.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if profile.Allowed {
		t.Fatal("self elevation changed capability")
	}
	support, err := api.auth.IssueAdmin("support-1", "support", time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, caller := range []struct {
		token  string
		status int
	}{{token, 401}, {support, 403}} {
		response = authenticatedRequest(t, http.MethodPut, ts.URL+"/v2/admin/users/usr_alice/message-permissions", caller.token, `{"canDeleteMessagesForEveryone":true,"reason":"unauthorized","confirmed":true}`)
		response.Body.Close()
		if response.StatusCode != caller.status {
			t.Fatalf("unauthorized permission change status=%d", response.StatusCode)
		}
	}
}
