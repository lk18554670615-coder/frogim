package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/auth"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
)

func TestWukongMediaBindingGrantsOnlyChannelMembersSignedURL(t *testing.T) {
	a, err := app.New(t.Context(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := a.Login("13800003001", "Owner")
	recipient, _ := a.Login("13800003002", "Recipient")
	outsider, _ := a.Login("13800003003", "Outsider")
	request, err := a.RequestFriendWithSource(owner.ID, recipient.ID, "hello", "search")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.AcceptFriend(recipient.ID, request.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.CreateMedia(store.Media{
		ID: "med_wukong", OwnerID: owner.ID, ObjectKey: "objects/med_wukong",
		MIME: "image/png", Size: 10, Status: "ready",
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{JWTSecret: strings.Repeat("m", 32), AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	api := New(cfg, a)
	api.media = signedMediaService{}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	manager := auth.Manager{Secret: []byte(cfg.JWTSecret), AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
	ownerToken, _, _ := manager.Issue(owner.ID)
	recipientToken, _, _ := manager.Issue(recipient.ID)
	outsiderToken, _, _ := manager.Issue(outsider.ID)

	bound := authenticatedRequest(t, http.MethodPost, server.URL+"/v2/media/med_wukong/bind", ownerToken,
		`{"channelId":"`+recipient.ID+`","channelType":1}`)
	defer bound.Body.Close()
	if bound.StatusCode != http.StatusOK {
		t.Fatalf("bind status=%d", bound.StatusCode)
	}
	var response struct {
		URL string `json:"url"`
	}
	if err = json.NewDecoder(bound.Body).Decode(&response); err != nil || !strings.Contains(response.URL, "X-Amz-Signature") {
		t.Fatalf("bind response=%+v err=%v", response, err)
	}

	download := authenticatedRequest(t, http.MethodGet, server.URL+"/v2/media/med_wukong/url", recipientToken, "")
	defer download.Body.Close()
	if download.StatusCode != http.StatusOK {
		t.Fatalf("recipient status=%d", download.StatusCode)
	}

	denied := authenticatedRequest(t, http.MethodGet, server.URL+"/v2/media/med_wukong/url", outsiderToken, "")
	defer denied.Body.Close()
	if denied.StatusCode != http.StatusNotFound {
		t.Fatalf("outsider status=%d", denied.StatusCode)
	}
}
