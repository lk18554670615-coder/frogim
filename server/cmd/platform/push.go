package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/push"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/webpushpolicy"
)

// The adapter reuses provider encoders, never an enterprise repository. Only
// this platform executable receives shared App credentials and decrypted tokens.
type platformPushSender struct{ providers map[string]push.Provider }

func (p platformPushSender) Send(ctx context.Context, d platform.PushDelivery) error {
	if d.BeforeSend == nil || ctx.Err() != nil || d.BeforeSend(ctx) != nil {
		return platform.ErrUnavailable
	}
	ctx = push.WithSubmissionCheck(ctx, d.BeforeSend)
	provider := p.providers[d.Device.Provider]
	if provider == nil {
		return platform.ErrUnavailable
	}
	request := d.Request
	payload := map[string]any{"tenantId": request.TenantID, "localUserId": request.LocalUserID, "assignmentVersion": request.AssignmentVersion,
		"authVersion": request.AuthVersion, "realmVersion": request.RealmVersion, "expiresAt": request.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"pushBindingId": d.BindingID, "pushBindingRevision": d.BindingRevision}
	if request.ConversationID != "" {
		payload["conversationId"] = request.ConversationID
	}
	if request.MessageID != "" {
		payload["message"] = map[string]any{"id": request.MessageID, "conversationId": request.ConversationID, "type": request.MessageType}
	}
	if request.CallID != "" {
		payload["callId"] = request.CallID
		payload["mediaType"] = request.MediaType
	}
	item := store.OutboxItem{ID: d.ID, UserID: request.LocalUserID, EventType: request.EventType, Payload: payload, Devices: []store.Device{{ID: d.BindingID, Platform: d.Device.Platform, Provider: d.Device.Provider, PushToken: d.Device.PushToken, NotificationsEnabled: d.Device.NotificationsEnabled, PreviewEnabled: d.Device.PreviewEnabled, SoundEnabled: d.Device.SoundEnabled, VibrationEnabled: d.Device.VibrationEnabled}}}
	err := provider.Send(ctx, item)
	var delivery *push.DeliveryError
	if errors.As(err, &delivery) && delivery.InvalidOnly && len(delivery.InvalidDeviceIDs) == 1 && delivery.InvalidDeviceIDs[0] == d.BindingID {
		return platform.ErrInvalidPushDevice
	}
	if err != nil {
		return platform.ErrUnavailable
	}
	return nil
}
func configuredPlatformPush(db *platform.Store) (*platform.PushService, error) {
	mode := os.Getenv("PLATFORM_PUSH_PROVIDER")
	if mode != "" && mode != "disabled" && mode != "getui" {
		return nil, errors.New("unsupported platform push provider")
	}
	voip := os.Getenv("PLATFORM_GETUI_VOIP_ENABLED")
	if (voip != "" && voip != "true" && voip != "false") || (voip == "true" && mode != "getui") {
		return nil, errors.New("invalid Getui VoIP capability")
	}
	for _, key := range []string{"PLATFORM_APNS_VOIP_KEY_ID", "PLATFORM_APNS_VOIP_TEAM_ID", "PLATFORM_APNS_VOIP_BUNDLE_ID", "PLATFORM_APNS_VOIP_KEY_FILE", "PLATFORM_APNS_VOIP_SANDBOX"} {
		if os.Getenv(key) != "" {
			return nil, errors.New("direct APNs VoIP is disabled for the platform; remove old configuration")
		}
	}
	webEnabled := os.Getenv("PLATFORM_WEB_PUSH_ENABLED")
	if webEnabled != "" && webEnabled != "false" && webEnabled != "true" {
		return nil, errors.New("invalid platform Web Push mode")
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	providers := map[string]push.Provider{}
	names := []string{}
	var browser *webpushpolicy.Policy
	if webEnabled == "true" {
		public, private, subject := os.Getenv("PLATFORM_WEB_PUSH_PUBLIC_KEY"), os.Getenv("PLATFORM_WEB_PUSH_PRIVATE_KEY"), os.Getenv("PLATFORM_WEB_PUSH_SUBJECT")
		var err error
		browser, err = webpushpolicy.New(public, private, subject, os.Getenv("PLATFORM_WEB_PUSH_ALLOWED_HOSTS"))
		if err != nil {
			return nil, errors.New("platform Web Push configuration invalid")
		}
		providers["webpush"] = &push.WebPush{PublicKey: public, PrivateKey: private, Subject: subject, Policy: browser}
		names = append(names, "webpush")
	} else if strings.TrimSpace(os.Getenv("PLATFORM_WEB_PUSH_PRIVATE_KEY")) != "" {
		return nil, errors.New("platform Web Push key configured but provider disabled")
	}
	if mode == "getui" {
		id, key, secret := os.Getenv("PLATFORM_GETUI_APP_ID"), os.Getenv("PLATFORM_GETUI_APP_KEY"), os.Getenv("PLATFORM_GETUI_MASTER_SECRET")
		if !regexp.MustCompile(`^[A-Za-z0-9_-]{4,128}$`).MatchString(id) || len(key) < 16 || len(secret) < 24 {
			return nil, errors.New("platform Getui credentials incomplete")
		}
		providers["getui"] = &push.Getui{AppID: id, AppKey: key, MasterSecret: secret, Client: client}
		names = append(names, "getui")
		if voip == "true" {
			providers["getui_voip"] = &push.Getui{AppID: id, AppKey: key, MasterSecret: secret, Client: client, VoIP: true}
			names = append(names, "getui_voip")
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	return platform.NewPushService(db, os.Getenv("PLATFORM_PUSH_ENCRYPTION_KEY"), platformPushSender{providers: providers}, names, browser)
}
