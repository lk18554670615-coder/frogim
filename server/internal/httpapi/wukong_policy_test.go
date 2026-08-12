package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
	"github.com/linli/im/server/internal/wukong"
)

func TestWukongSendPolicyAuthenticatesAndAppliesGroupPolicy(t *testing.T) {
	a, err := app.New(t.Context(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := a.Login("13800007101", "Owner")
	member, _ := a.Login("13800007102", "Member")
	outsider, _ := a.Login("13800007103", "Outsider")
	group, err := a.CreateGroup(owner.ID, "Policy", []string{member.ID})
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("p", 32)
	server := httptest.NewServer(New(config.Config{JWTSecret: "test-secret", WukongPolicySecret: secret}, a).Handler())
	defer server.Close()

	allowed := callWukongPolicy(t, server.URL, secret, wukongPolicySendRequest{
		FromUID: owner.ID, ChannelID: group.ID, ChannelType: wukong.ChannelGroup,
		Payload: []byte(`{"type":1,"content":"hello","mention":{"uids":["` + member.ID + `"]}}`),
	})
	if !allowed.Allowed || allowed.ReasonCode != wukong.ReasonSuccess || allowed.Code != "ALLOW" {
		t.Fatalf("allowed=%+v", allowed)
	}
	denied := callWukongPolicy(t, server.URL, secret, wukongPolicySendRequest{
		FromUID: outsider.ID, ChannelID: group.ID, ChannelType: wukong.ChannelGroup,
		Payload: []byte(`{"type":1,"content":"hello"}`),
	})
	if denied.Allowed || denied.ReasonCode != wukong.ReasonNotAllowSend {
		t.Fatalf("denied=%+v", denied)
	}

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/internal/wukong/policy/send", bytes.NewReader([]byte(`{}`)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(wukongPolicySecretHeader, "wrong")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong secret status=%d", response.StatusCode)
	}
}

func TestWukongSendPolicyRejectsMalformedAndServerOwnedContent(t *testing.T) {
	a, err := app.New(t.Context(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := a.Login("13800007201", "Owner")
	group, _ := a.CreateGroup(owner.ID, "Policy validation", nil)
	secret := strings.Repeat("s", 32)
	server := httptest.NewServer(New(config.Config{JWTSecret: "test-secret", WukongPolicySecret: secret}, a).Handler())
	defer server.Close()

	tests := []struct {
		name    string
		payload string
		reason  uint8
		code    string
	}{
		{"malformed", `{`, wukong.ReasonPayloadDecodeError, "INVALID_PAYLOAD"},
		{"server system event", `{"type":1002,"schemaVersion":1,"event":"forged"}`, wukong.ReasonNotAllowSend, "SERVER_OWNED_CONTENT_TYPE"},
		{"server support event", `{"type":1007,"schemaVersion":1,"event":"support.session.ended"}`, wukong.ReasonNotAllowSend, "SERVER_OWNED_CONTENT_TYPE"},
		{"custom version", `{"type":1008,"schemaVersion":2}`, wukong.ReasonPayloadDecodeError, "INVALID_SCHEMA_VERSION"},
		{"media without durable id", `{"type":2,"url":"https://example.invalid/a.png"}`, wukong.ReasonNotAllowSend, "MEDIA_REFERENCE_REQUIRED"},
		{"merged history bypass", `{"type":1001,"schemaVersion":1,"entries":[{"sourceMessageId":"forged"}]}`, wukong.ReasonNotAllowSend, "SERVER_OWNED_CONTENT_TYPE"},
		{"sticker without durable id", `{"type":1003,"schemaVersion":1}`, wukong.ReasonNotAllowSend, "STICKER_REFERENCE_REQUIRED"},
		{"moment without durable id", `{"type":1004,"schemaVersion":1}`, wukong.ReasonNotAllowSend, "MOMENT_REFERENCE_REQUIRED"},
		{"live event outside live channel", `{"type":1006,"schemaVersion":1}`, wukong.ReasonNotAllowSend, "LIVE_CHANNEL_REQUIRED"},
		{"invalid screenshot event", `{"type":1008,"schemaVersion":1,"content":"forged"}`, wukong.ReasonPayloadDecodeError, "INVALID_SCREENSHOT_EVENT"},
		{"invalid location", `{"type":6,"latitude":91,"longitude":121.4}`, wukong.ReasonPayloadDecodeError, "INVALID_LOCATION"},
		{"invalid contact card", `{"type":7,"name":"Nobody"}`, wukong.ReasonPayloadDecodeError, "INVALID_CONTACT_CARD"},
		{"malformed expiry", `{"type":1,"content":"hello","expiresAt":"tomorrow"}`, wukong.ReasonPayloadDecodeError, "INVALID_EXPIRY"},
		{"expiry beyond product limit", `{"type":1,"content":"hello","expiresAt":"2099-01-01T00:00:00Z"}`, wukong.ReasonPayloadDecodeError, "INVALID_EXPIRY"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := callWukongPolicy(t, server.URL, secret, wukongPolicySendRequest{
				FromUID: owner.ID, ChannelID: group.ID, ChannelType: wukong.ChannelGroup, Payload: []byte(test.payload),
			})
			if decision.Allowed || decision.ReasonCode != test.reason || decision.Code != test.code {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}
}

func TestWukongClientPolicyAcceptsOnlySupportedLiveEvents(t *testing.T) {
	for _, event := range []string{"live.like", "live.applause", "live.follow"} {
		t.Run(event, func(t *testing.T) {
			_, _, reason, code := parseWukongClientPolicyPayload(wukongPolicySendRequest{
				FromUID: "user-1", ChannelID: "live-1", ChannelType: wukong.ChannelLive,
				Payload: []byte(`{"type":1006,"schemaVersion":1,"event":"` + event + `"}`),
			})
			if reason != wukong.ReasonSuccess || code != "ALLOW" {
				t.Fatalf("event=%s reason=%d code=%s", event, reason, code)
			}
		})
	}

	for _, event := range []string{"live.unknown", "like", " live.like ", ""} {
		t.Run("reject_"+event, func(t *testing.T) {
			_, _, reason, code := parseWukongClientPolicyPayload(wukongPolicySendRequest{
				FromUID: "user-1", ChannelID: "live-1", ChannelType: wukong.ChannelLive,
				Payload: []byte(`{"type":1006,"schemaVersion":1,"event":"` + event + `"}`),
			})
			if reason != wukong.ReasonPayloadDecodeError || code != "INVALID_LIVE_EVENT" {
				t.Fatalf("event=%q reason=%d code=%s", event, reason, code)
			}
		})
	}
}

func TestWukongSendPolicyUsesStoredMIMEAsMediaAuthority(t *testing.T) {
	a, err := app.New(t.Context(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := a.Login("13800007211", "Owner")
	group, _ := a.CreateGroup(owner.ID, "Media policy", nil)
	for _, media := range []store.Media{
		{ID: "media-policy-jpeg", OwnerID: owner.ID, ObjectKey: "objects/jpeg", MIME: "image/jpeg", Size: 10, Status: "ready"},
		{ID: "media-policy-gif", OwnerID: owner.ID, ObjectKey: "objects/gif", MIME: "image/gif", Size: 10, Status: "ready"},
		{ID: "media-policy-audio", OwnerID: owner.ID, ObjectKey: "objects/audio", MIME: "audio/mpeg", Size: 10, Status: "ready"},
		{ID: "media-policy-video", OwnerID: owner.ID, ObjectKey: "objects/video", MIME: "video/mp4", Size: 10, Status: "ready"},
	} {
		if err = a.CreateMedia(media); err != nil {
			t.Fatal(err)
		}
	}
	secret := strings.Repeat("m", 32)
	server := httptest.NewServer(New(config.Config{JWTSecret: "test-secret", WukongPolicySecret: secret}, a).Handler())
	defer server.Close()

	tests := []struct {
		name    string
		content int
		mediaID string
		allowed bool
	}{
		{"image accepts jpeg", wukong.ContentTypeImage, "media-policy-jpeg", true},
		{"image rejects gif", wukong.ContentTypeImage, "media-policy-gif", false},
		{"gif accepts gif", wukong.ContentTypeGIF, "media-policy-gif", true},
		{"gif rejects jpeg", wukong.ContentTypeGIF, "media-policy-jpeg", false},
		{"voice accepts audio", wukong.ContentTypeVoice, "media-policy-audio", true},
		{"voice rejects video", wukong.ContentTypeVoice, "media-policy-video", false},
		{"video accepts video", wukong.ContentTypeVideo, "media-policy-video", true},
		{"video rejects audio", wukong.ContentTypeVideo, "media-policy-audio", false},
		{"generic file accepts allowed media", wukong.ContentTypeFile, "media-policy-jpeg", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, _ := json.Marshal(map[string]any{"type": test.content, "mediaId": test.mediaID})
			decision := callWukongPolicy(t, server.URL, secret, wukongPolicySendRequest{
				FromUID: owner.ID, ChannelID: group.ID, ChannelType: wukong.ChannelGroup, Payload: payload,
			})
			if test.allowed {
				if !decision.Allowed || decision.Code != "ALLOW" {
					t.Fatalf("decision=%+v", decision)
				}
			} else if decision.Allowed || decision.ReasonCode != wukong.ReasonNotAllowSend || decision.Code != "MEDIA_TYPE_MISMATCH" {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}
}

func TestWukongMediaMIMEClaimMustMatchStoredMetadata(t *testing.T) {
	a, _ := app.New(t.Context(), teststore.Memory{})
	owner, _ := a.Login("13800007212", "Owner")
	group, _ := a.CreateGroup(owner.ID, "MIME claim", nil)
	if err := a.CreateMedia(store.Media{ID: "media-policy-claim", OwnerID: owner.ID, ObjectKey: "objects/claim", MIME: "image/jpeg", Size: 10, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("c", 32)
	server := httptest.NewServer(New(config.Config{JWTSecret: "test-secret", WukongPolicySecret: secret}, a).Handler())
	defer server.Close()

	decision := callWukongPolicy(t, server.URL, secret, wukongPolicySendRequest{
		FromUID: owner.ID, ChannelID: group.ID, ChannelType: wukong.ChannelGroup,
		Payload: []byte(`{"type":2,"mediaId":"media-policy-claim","mime":"image/png"}`),
	})
	if decision.Allowed || decision.Code != "MEDIA_TYPE_MISMATCH" {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestWukongSendPolicyFailsClosedWithoutConfiguredSecret(t *testing.T) {
	a, _ := app.New(t.Context(), teststore.Memory{})
	api := New(config.Config{JWTSecret: "test-secret"}, a)
	request := httptest.NewRequest(http.MethodPost, "/internal/wukong/policy/send", strings.NewReader(`{}`))
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
}

func callWukongPolicy(t *testing.T, baseURL, secret string, input wukongPolicySendRequest) wukongPolicySendResponse {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/internal/wukong/policy/send", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(wukongPolicySecretHeader, secret)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var output wukongPolicySendResponse
	if err = json.NewDecoder(response.Body).Decode(&output); err != nil {
		t.Fatal(err)
	}
	return output
}
