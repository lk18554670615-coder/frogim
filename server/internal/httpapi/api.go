package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/auth"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/linkpreview"
	livekitcontrol "github.com/linli/im/server/internal/livekit"
	"github.com/linli/im/server/internal/media"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/netutil"
	"github.com/linli/im/server/internal/push"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/wukong"
	"github.com/linli/im/server/internal/wukongplugin"
)

type API struct {
	cfg             config.Config
	app             *app.App
	auth            auth.Manager
	media           mediaService
	cleaner         mediaCleanupService
	mux             *http.ServeMux
	started         time.Time
	limits          *limiter
	otp             otpProvider
	links           *linkpreview.Service
	wukongClient    *wukong.Client
	imSessions      *wukong.SessionIssuer
	wukongSetupErr  error
	pluginInstaller *wukongplugin.Installer
	pluginSetupErr  error
	livekit         livekitControl
	livekitSetupErr error
}
type mediaService interface {
	Prepare(context.Context, string, string, string, int64) (media.Prepared, error)
	Complete(context.Context, string, string, string) (store.Media, error)
	DownloadURL(context.Context, string) (string, error)
}
type mediaCleanupService interface {
	CleanupOnce(context.Context) (int, error)
}
type livekitControl interface {
	URL() string
	TokenTTL() time.Duration
	EnsureCallRoom(context.Context, string, string, string) error
	DeleteCallRoom(context.Context, string) error
	RemoveParticipant(context.Context, string, string) error
	IssueParticipant(string, string, string, string) (livekitcontrol.ParticipantSession, error)
}
type ctxKey string

type responseCapture struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *responseCapture) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseCapture) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += n
	return n, err
}
func (w *responseCapture) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
func (w *responseCapture) Push(target string, options *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}
func (w *responseCapture) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func newRequestID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(raw[:])
}

func newQRLoginSecret(prefix string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func qrLoginSecretHash(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

const userKey ctxKey = "user"
const roleKey ctxKey = "role"

func New(cfg config.Config, a *app.App) *API {
	if cfg.CallInviteTTL == 0 {
		cfg.CallInviteTTL = 30 * time.Second
	}
	x := &API{cfg: cfg, app: a, auth: auth.Manager{Secret: []byte(cfg.JWTSecret), AccessTTL: cfg.AccessTTL, RefreshTTL: cfg.RefreshTTL}, started: time.Now(), limits: newLimiter(), links: linkpreview.New(linkpreview.Config{})}
	if cfg.WukongEnabled {
		x.wukongClient, x.wukongSetupErr = wukong.NewClient(wukong.Config{
			APIURL: cfg.WukongAPIURL, ManagerURL: cfg.WukongManagerURL,
			ManagerToken: cfg.WukongManagerToken, Timeout: 5 * time.Second, MaxRetries: 2,
		})
		if x.wukongSetupErr == nil {
			x.imSessions, x.wukongSetupErr = wukong.NewSessionIssuer(x.wukongClient, cfg.WukongTokenSecret, cfg.WukongTCPURL, cfg.WukongWSURL, a)
			a.SetMessageTransport(newWukongMessageTransport(x.wukongClient, a))
			a.SetMessageSourceLoader(newWukongMessageSourceLoader(x.wukongClient, a))
			a.SetMessageSearchLoader(newWukongMessageSearchLoader(x.wukongClient, a))
			a.SetMessageHistoryLoader(newWukongMessageHistoryLoader(x.wukongClient, a))
			a.SetReadStateTransport(newWukongReadStateTransport(x.wukongClient, a))
			a.SetEventSink(func(userIDs []string, event string, payload any) {
				if event != "typing" || len(userIDs) == 0 {
					return
				}
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					if err := x.wukongClient.SendBusinessEvent(ctx, userIDs, event, payload); err != nil {
						slog.Warn("WuKongIM ephemeral event failed", "event", event, "recipients", len(userIDs), "error", err)
					}
				}()
			})
		}
		if strings.TrimSpace(cfg.WukongPluginDir) != "" || strings.TrimSpace(cfg.WukongPluginTrustedKeys) != "" || strings.TrimSpace(cfg.WukongPluginAllowlist) != "" {
			trustedKeys, keyErr := wukongplugin.ParseTrustedKeys(cfg.WukongPluginTrustedKeys)
			allowlist, allowErr := wukongplugin.ParseAllowlist(cfg.WukongPluginAllowlist)
			if keyErr != nil {
				x.pluginSetupErr = keyErr
			} else if allowErr != nil {
				x.pluginSetupErr = allowErr
			} else {
				x.pluginInstaller, x.pluginSetupErr = wukongplugin.New(wukongplugin.Config{Directory: cfg.WukongPluginDir, TrustedKeys: trustedKeys, Allowlist: allowlist, MaxBytes: cfg.WukongPluginMaxBytes})
			}
		}
	}
	if cfg.LiveKitEnabled {
		x.livekit, x.livekitSetupErr = livekitcontrol.NewControl(livekitcontrol.Config{
			URL: cfg.LiveKitURL, APIURL: cfg.LiveKitAPIURL,
			APIKey: cfg.LiveKitAPIKey, APISecret: cfg.LiveKitAPISecret,
			PrometheusURL: cfg.PrometheusURL, TokenTTL: cfg.LiveKitTokenTTL,
		})
	}
	if cfg.DevMode {
		x.otp = devOTP(cfg.DevOTPCode)
	} else if cfg.OTPWebhookURL != "" {
		provider := newWebhookOTP(cfg.OTPWebhookURL, cfg.OTPWebhookToken)
		x.otp = provider
	}
	x.media, _ = media.New(cfg.S3Endpoint, cfg.S3PublicEndpoint, cfg.S3AndroidPublicEndpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3Region, cfg.S3Secure, cfg.S3PublicSecure, cfg.MediaMaxBytes, a)
	if cleaner, ok := x.media.(mediaCleanupService); ok {
		x.cleaner = cleaner
	}
	a.SetCallInviteTTL(cfg.CallInviteTTL)
	x.mux = http.NewServeMux()
	x.routes()
	return x
}
func (x *API) SetupError() error {
	if x.cfg.WukongEnabled && x.wukongSetupErr != nil {
		return fmt.Errorf("WuKongIM setup: %w", x.wukongSetupErr)
	}
	if !x.cfg.WukongEnabled || x.wukongClient == nil || x.imSessions == nil {
		return errors.New("WuKongIM message transport is required")
	}
	return nil
}
func (x *API) Handler() http.Handler { return x.middleware(x.mux) }
func (x *API) RunMediaCleanup(ctx context.Context) {
	if x.cleaner == nil {
		return
	}
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		if _, err := x.cleaner.CleanupOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("media cleanup failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (x *API) routes() {
	x.mux.HandleFunc("GET /health", x.health)
	x.mux.HandleFunc("GET /ready", x.ready)
	x.mux.HandleFunc("GET /metrics", x.metrics)
	x.mux.HandleFunc("POST /internal/wukong/datasource", x.wukongServerDataSource)
	x.mux.HandleFunc("POST /internal/wukong/policy/send", x.wukongSendPolicy)
	if x.cfg.DevMode {
		x.mux.HandleFunc("POST /internal/wukong/load/pairs", x.wukongLoadPairs)
	}
	x.mux.HandleFunc("GET /v2/avatars/{id}", x.avatarDownload)
	x.mux.HandleFunc("POST /v2/auth/code", x.requestCode)
	x.mux.HandleFunc("POST /v2/auth/login", x.requireClientPlatform(x.login))
	x.mux.HandleFunc("POST /v2/auth/register", x.requireClientPlatform(x.register))
	x.mux.HandleFunc("POST /v2/auth/password-login", x.requireClientPlatform(x.passwordLogin))
	x.mux.HandleFunc("POST /v2/auth/qr/create", x.requireClientPlatform(x.createQRLogin))
	x.mux.HandleFunc("POST /v2/auth/qr/poll", x.requireClientPlatform(x.pollQRLogin))
	x.mux.Handle("POST /v2/auth/qr/inspect", x.requireAuth(http.HandlerFunc(x.inspectQRLogin)))
	x.mux.Handle("POST /v2/auth/qr/confirm", x.requireAuth(http.HandlerFunc(x.confirmQRLogin)))
	x.mux.HandleFunc("POST /v2/auth/password/reset-code", x.passwordResetCode)
	x.mux.HandleFunc("POST /v2/auth/password/reset", x.passwordReset)
	x.mux.HandleFunc("POST /v2/auth/refresh", x.requireClientPlatform(x.refresh))
	x.mux.Handle("POST /v2/auth/im-session", x.requireAuth(http.HandlerFunc(x.imSession)))
	x.mux.Handle("POST /v2/auth/logout", x.requireAuth(http.HandlerFunc(x.logout)))
	x.mux.Handle("POST /v2/im/datasource/conversations", x.requireAuth(http.HandlerFunc(x.wukongConversationSync)))
	x.mux.Handle("POST /v2/im/datasource/messages", x.requireAuth(http.HandlerFunc(x.wukongMessageSync)))
	x.mux.Handle("POST /v2/im/datasource/extensions", x.requireAuth(http.HandlerFunc(x.wukongMessageExtensionSync)))
	x.mux.Handle("POST /v2/im/datasource/message-extras", x.requireAuth(http.HandlerFunc(x.wukongMessageExtraSync)))
	x.mux.Handle("POST /v2/im/datasource/reminders", x.requireAuth(http.HandlerFunc(x.wukongReminderSync)))
	x.mux.Handle("POST /v2/im/datasource/reminders/done", x.requireAuth(http.HandlerFunc(x.wukongReminderDone)))
	x.mux.Handle("POST /v2/im/datasource/channel", x.requireAuth(http.HandlerFunc(x.wukongChannelInfo)))
	x.mux.Handle("POST /v2/im/datasource/members", x.requireAuth(http.HandlerFunc(x.wukongChannelMembers)))
	x.mux.Handle("POST /v2/messages/{id}/recall", x.requireAuth(http.HandlerFunc(x.recall)))
	x.mux.Handle("PATCH /v2/messages/{id}", x.requireAuth(http.HandlerFunc(x.editMessage)))
	x.mux.Handle("GET /v2/messages/{id}/edits", x.requireAuth(http.HandlerFunc(x.messageEdits)))
	x.mux.Handle("PUT /v2/messages/{id}/reactions/{emoji}", x.requireAuth(http.HandlerFunc(x.messageReaction)))
	x.mux.Handle("DELETE /v2/messages/{id}/reactions/{emoji}", x.requireAuth(http.HandlerFunc(x.messageReaction)))
	x.mux.Handle("GET /v2/messages/favorites", x.requireAuth(http.HandlerFunc(x.favorites)))
	x.mux.Handle("PUT /v2/messages/favorites/{messageId}", x.requireAuth(http.HandlerFunc(x.setFavorite)))
	x.mux.Handle("DELETE /v2/messages/favorites/{messageId}", x.requireAuth(http.HandlerFunc(x.setFavorite)))
	x.mux.Handle("GET /v2/messages/pins", x.requireAuth(http.HandlerFunc(x.groupMessagePins)))
	x.mux.Handle("PUT /v2/messages/pins/{messageId}", x.requireAuth(http.HandlerFunc(x.groupMessagePin)))
	x.mux.Handle("DELETE /v2/messages/pins/{messageId}", x.requireAuth(http.HandlerFunc(x.groupMessagePin)))
	x.mux.Handle("GET /v2/messages/search", x.requireAuth(http.HandlerFunc(x.searchMessages)))
	x.mux.Handle("POST /v2/messages/forward", x.requireAuth(http.HandlerFunc(x.forwardMessages)))
	x.mux.Handle("POST /v2/messages/conversations/{id}/send", x.requireAuth(http.HandlerFunc(x.sendMessage)))
	x.mux.Handle("GET /v2/messages/conversations/{id}/history", x.requireAuth(http.HandlerFunc(x.history)))
	x.mux.Handle("POST /v2/messages/conversations/{id}/streams", x.requireAuth(http.HandlerFunc(x.startMessageStream)))
	x.mux.Handle("POST /v2/messages/conversations/{id}/streams/{clientMsgNo}/events", x.requireAuth(http.HandlerFunc(x.appendMessageStreamEvent)))
	x.mux.Handle("GET /v2/messages/conversations/{id}/streams/{clientMsgNo}/events", x.requireAuth(http.HandlerFunc(x.syncMessageStreamEvents)))
	x.mux.Handle("POST /v2/messages/scheduled", x.requireAuth(http.HandlerFunc(x.createScheduledMessage)))
	x.mux.Handle("GET /v2/messages/scheduled", x.requireAuth(http.HandlerFunc(x.scheduledMessages)))
	x.mux.Handle("PATCH /v2/messages/scheduled/{id}", x.requireAuth(http.HandlerFunc(x.updateScheduledMessage)))
	x.mux.Handle("DELETE /v2/messages/scheduled/{id}", x.requireAuth(http.HandlerFunc(x.cancelScheduledMessage)))
	x.mux.Handle("GET /v2/channels/business", x.requireAuth(http.HandlerFunc(x.businessChannels)))
	x.mux.Handle("POST /v2/channels/business", x.requireAuth(http.HandlerFunc(x.createBusinessChannel)))
	x.mux.Handle("GET /v2/channels/conversations", x.requireAuth(http.HandlerFunc(x.conversations)))
	x.mux.Handle("POST /v2/channels/direct", x.requireAuth(http.HandlerFunc(x.direct)))
	x.mux.Handle("PATCH /v2/channels/conversations/{id}/preferences", x.requireAuth(http.HandlerFunc(x.conversationPreferences)))
	x.mux.Handle("DELETE /v2/channels/conversations/{id}", x.requireAuth(http.HandlerFunc(x.hideConversation)))
	x.mux.Handle("PUT /v2/channels/conversations/{id}/read", x.requireAuth(http.HandlerFunc(x.read)))
	x.mux.Handle("PUT /v2/channels/conversations/{id}/delivered", x.requireAuth(http.HandlerFunc(x.delivered)))
	x.mux.Handle("POST /v2/channels/conversations/{id}/typing", x.requireAuth(http.HandlerFunc(x.typing)))
	x.mux.Handle("POST /v2/channels/groups", x.requireAuth(http.HandlerFunc(x.createGroup)))
	x.mux.Handle("GET /v2/channels/groups/{id}", x.requireAuth(http.HandlerFunc(x.groupProfile)))
	x.mux.Handle("PATCH /v2/channels/groups/{id}", x.requireAuth(http.HandlerFunc(x.updateGroupProfile)))
	x.mux.Handle("PUT /v2/channels/groups/{id}/announcement", x.requireAuth(http.HandlerFunc(x.groupAnnouncement)))
	x.mux.Handle("POST /v2/channels/groups/{id}/announcement/read", x.requireAuth(http.HandlerFunc(x.readGroupAnnouncement)))
	x.mux.Handle("POST /v2/channels/groups/{id}/invites", x.requireAuth(http.HandlerFunc(x.groupInvite)))
	x.mux.Handle("GET /v2/channels/group-invitations", x.requireAuth(http.HandlerFunc(x.groupInvites)))
	x.mux.Handle("POST /v2/channels/group-invitations/{id}/{action}", x.requireAuth(http.HandlerFunc(x.groupInviteAction)))
	x.mux.Handle("POST /v2/channels/groups/join/qr", x.requireAuth(http.HandlerFunc(x.joinGroupQR)))
	x.mux.Handle("POST /v2/channels/groups/{id}/owner/transfer", x.requireAuth(http.HandlerFunc(x.transferGroupOwner)))
	x.mux.Handle("POST /v2/channels/groups/{id}/leave", x.requireAuth(http.HandlerFunc(x.leaveGroup)))
	x.mux.Handle("PUT /v2/channels/groups/{id}/mute-all", x.requireAuth(http.HandlerFunc(x.muteGroupAll)))
	x.mux.Handle("PATCH /v2/channels/groups/{id}/nickname", x.requireAuth(http.HandlerFunc(x.groupNickname)))
	x.mux.Handle("POST /v2/channels/groups/{id}/disband", x.requireAuth(http.HandlerFunc(x.userDisbandGroup)))
	x.mux.Handle("POST /v2/channels/groups/{id}/members", x.requireAuth(http.HandlerFunc(x.addMembers)))
	x.mux.Handle("GET /v2/channels/groups/{id}/members", x.requireAuth(http.HandlerFunc(x.groupMembers)))
	x.mux.Handle("DELETE /v2/channels/groups/{id}/members/{userId}", x.requireAuth(http.HandlerFunc(x.removeMember)))
	x.mux.Handle("PUT /v2/channels/groups/{id}/members/{userId}/role", x.requireAuth(http.HandlerFunc(x.groupRole)))
	x.mux.Handle("PUT /v2/channels/groups/{id}/members/{userId}/mute", x.requireAuth(http.HandlerFunc(x.mute)))
	x.mux.Handle("GET /v2/channels/business/{id}", x.requireAuth(http.HandlerFunc(x.businessChannel)))
	x.mux.Handle("PATCH /v2/channels/business/{id}", x.requireAuth(http.HandlerFunc(x.updateBusinessChannel)))
	x.mux.Handle("POST /v2/channels/business/{id}/subscribe", x.requireAuth(http.HandlerFunc(x.subscribeBusinessChannel)))
	x.mux.Handle("DELETE /v2/channels/business/{id}/subscription", x.requireAuth(http.HandlerFunc(x.unsubscribeBusinessChannel)))
	x.mux.Handle("GET /v2/channels/business/{id}/members", x.requireAuth(http.HandlerFunc(x.businessChannelMembers)))
	x.mux.Handle("GET /v2/channels/business/{id}/access", x.requireAuth(http.HandlerFunc(x.businessChannelAccess)))
	x.mux.Handle("PUT /v2/channels/business/{id}/members/{userId}", x.requireAuth(http.HandlerFunc(x.addBusinessChannelMember)))
	x.mux.Handle("DELETE /v2/channels/business/{id}/members/{userId}", x.requireAuth(http.HandlerFunc(x.removeBusinessChannelMember)))
	x.mux.Handle("PATCH /v2/channels/business/{id}/members/{userId}", x.requireAuth(http.HandlerFunc(x.updateBusinessChannelMember)))
	x.mux.Handle("PUT /v2/channels/business/{id}/access/{accessType}/{userId}", x.requireAuth(http.HandlerFunc(x.setBusinessChannelAccess)))
	x.mux.Handle("DELETE /v2/channels/business/{id}/access/{accessType}/{userId}", x.requireAuth(http.HandlerFunc(x.setBusinessChannelAccess)))
	x.mux.Handle("GET /v2/support/skills", x.requireAuth(http.HandlerFunc(x.supportSkills)))
	x.mux.Handle("GET /v2/support/agents", x.requireAuth(http.HandlerFunc(x.supportAgents)))
	x.mux.Handle("PUT /v2/support/agent/status", x.requireAuth(http.HandlerFunc(x.supportAgentStatus)))
	x.mux.Handle("GET /v2/support/sessions", x.requireAuth(http.HandlerFunc(x.supportSessions)))
	x.mux.Handle("POST /v2/support/sessions", x.requireAuth(http.HandlerFunc(x.createSupportSession)))
	x.mux.Handle("GET /v2/support/sessions/{id}", x.requireAuth(http.HandlerFunc(x.supportSession)))
	x.mux.Handle("POST /v2/support/sessions/{id}/claim", x.requireAuth(http.HandlerFunc(x.claimSupportSession)))
	x.mux.Handle("POST /v2/support/sessions/{id}/transfer", x.requireAuth(http.HandlerFunc(x.transferSupportSession)))
	x.mux.Handle("POST /v2/support/sessions/{id}/end", x.requireAuth(http.HandlerFunc(x.endSupportSession)))
	x.mux.Handle("POST /v2/support/sessions/{id}/rating", x.requireAuth(http.HandlerFunc(x.rateSupportSession)))
	x.mux.Handle("GET /v2/moments", x.requireAuth(http.HandlerFunc(x.moments)))
	x.mux.Handle("POST /v2/moments", x.requireAuth(http.HandlerFunc(x.createMoment)))
	x.mux.Handle("DELETE /v2/moments/{id}", x.requireAuth(http.HandlerFunc(x.deleteMoment)))
	x.mux.Handle("PUT /v2/moments/{id}/like", x.requireAuth(http.HandlerFunc(x.setMomentLike)))
	x.mux.Handle("DELETE /v2/moments/{id}/like", x.requireAuth(http.HandlerFunc(x.setMomentLike)))
	x.mux.Handle("POST /v2/moments/{id}/comments", x.requireAuth(http.HandlerFunc(x.createMomentComment)))
	x.mux.Handle("DELETE /v2/moments/{id}/comments/{commentId}", x.requireAuth(http.HandlerFunc(x.deleteMomentComment)))
	x.mux.Handle("GET /v2/moments/reminders", x.requireAuth(http.HandlerFunc(x.momentReminders)))
	x.mux.Handle("POST /v2/moments/reminders/read", x.requireAuth(http.HandlerFunc(x.markMomentRemindersRead)))
	x.mux.Handle("GET /v2/stickers/categories", x.requireAuth(http.HandlerFunc(x.stickerCategories)))
	x.mux.Handle("GET /v2/stickers/packs", x.requireAuth(http.HandlerFunc(x.stickerPacks)))
	x.mux.Handle("GET /v2/stickers/packs/{id}", x.requireAuth(http.HandlerFunc(x.stickerPack)))
	x.mux.Handle("PUT /v2/stickers/packs/{id}/favorite", x.requireAuth(http.HandlerFunc(x.setStickerPackFavorite)))
	x.mux.Handle("DELETE /v2/stickers/packs/{id}/favorite", x.requireAuth(http.HandlerFunc(x.setStickerPackFavorite)))
	x.mux.Handle("GET /v2/stickers/recent", x.requireAuth(http.HandlerFunc(x.recentStickers)))
	x.mux.Handle("GET /v2/stickers/favorites", x.requireAuth(http.HandlerFunc(x.favoriteStickers)))
	x.mux.Handle("PUT /v2/stickers/{id}/favorite", x.requireAuth(http.HandlerFunc(x.setStickerFavorite)))
	x.mux.Handle("DELETE /v2/stickers/{id}/favorite", x.requireAuth(http.HandlerFunc(x.setStickerFavorite)))
	x.mux.Handle("POST /v2/stickers/{id}/used", x.requireAuth(http.HandlerFunc(x.recordStickerUse)))
	x.mux.HandleFunc("GET /v2/config/version", x.clientVersion)
	x.mux.HandleFunc("GET /v2/config/web-push", x.webPushConfig)
	x.mux.HandleFunc("GET /v2/config/auth", x.authPolicy)
	x.mux.HandleFunc("POST /v2/admin/auth/login", x.adminLogin)
	x.mux.Handle("GET /v2/admin/auth/me", x.requireAdminSession(http.HandlerFunc(x.adminMe)))
	x.mux.Handle("POST /v2/admin/auth/change-password", x.requireAdminSession(http.HandlerFunc(x.changeAdminPassword)))
	x.mux.Handle("GET /v2/users/me", x.requireAuth(http.HandlerFunc(x.me)))
	x.mux.Handle("PATCH /v2/users/me", x.requireAuth(http.HandlerFunc(x.updateMe)))
	x.mux.Handle("POST /v2/users/me/deletion/code", x.requireAuth(http.HandlerFunc(x.requestAccountDeletionCode)))
	x.mux.Handle("DELETE /v2/users/me", x.requireUserToken(http.HandlerFunc(x.deleteAccount)))
	x.mux.Handle("POST /v2/users/me/phone/code", x.requireAuth(http.HandlerFunc(x.requestPhoneChangeCode)))
	x.mux.Handle("PATCH /v2/users/me/phone", x.requireAuth(http.HandlerFunc(x.updatePhone)))
	x.mux.Handle("GET /v2/users/me/devices", x.requireAuth(http.HandlerFunc(x.userDevices)))
	x.mux.Handle("POST /v2/users/me/devices", x.requireAuth(http.HandlerFunc(x.registerDevice)))
	x.mux.Handle("PUT /v2/users/me/client-device", x.requireAuth(http.HandlerFunc(x.upsertClientDevice)))
	x.mux.Handle("DELETE /v2/users/me/devices/{id}", x.requireAuth(http.HandlerFunc(x.unregisterDevice)))
	x.mux.Handle("GET /v2/users/me/im-devices", x.requireAuth(http.HandlerFunc(x.userIMDevices)))
	x.mux.Handle("DELETE /v2/users/me/im-devices/{deviceFlag}", x.requireAuth(http.HandlerFunc(x.quitUserIMDevice)))
	x.mux.Handle("POST /v2/feedback", x.requireAuth(http.HandlerFunc(x.feedback)))
	x.mux.Handle("POST /v2/client-diagnostics", x.requireAuth(http.HandlerFunc(x.clientDiagnostic)))
	x.mux.Handle("POST /v2/media/presign", x.requireAuth(http.HandlerFunc(x.mediaPresign)))
	x.mux.Handle("POST /v2/link-preview", x.requireAuth(http.HandlerFunc(x.linkPreview)))
	x.mux.Handle("POST /v2/media/{id}/complete", x.requireAuth(http.HandlerFunc(x.mediaComplete)))
	x.mux.Handle("GET /v2/media/{id}", x.requireAuth(http.HandlerFunc(x.mediaDownload)))
	x.mux.Handle("POST /v2/media/{id}/bind", x.requireAuth(http.HandlerFunc(x.bindWukongMedia)))
	x.mux.Handle("GET /v2/media/{id}/url", x.requireAuth(http.HandlerFunc(x.wukongMediaURL)))
	x.mux.Handle("GET /v2/contacts/search", x.requireAuth(http.HandlerFunc(x.searchUsers)))
	x.mux.Handle("GET /v2/contacts/search/capabilities", x.requireAuth(http.HandlerFunc(x.searchCapabilities)))
	x.mux.Handle("POST /v2/contacts/requests", x.requireAuth(http.HandlerFunc(x.friendRequest)))
	x.mux.Handle("GET /v2/contacts/requests", x.requireAuth(http.HandlerFunc(x.friendRequests)))
	x.mux.Handle("GET /v2/contacts/friends", x.requireAuth(http.HandlerFunc(x.friends)))
	x.mux.Handle("POST /v2/contacts/requests/{id}/accept", x.requireAuth(http.HandlerFunc(x.acceptFriend)))
	x.mux.Handle("POST /v2/contacts/requests/{id}/reject", x.requireAuth(http.HandlerFunc(x.rejectFriend)))
	x.mux.Handle("POST /v2/contacts/requests/{id}/cancel", x.requireAuth(http.HandlerFunc(x.cancelFriendRequest)))
	x.mux.Handle("DELETE /v2/contacts/friends/{id}", x.requireAuth(http.HandlerFunc(x.deleteFriend)))
	x.mux.Handle("PATCH /v2/contacts/friends/{id}", x.requireAuth(http.HandlerFunc(x.friendMetadata)))
	x.mux.Handle("PUT /v2/contacts/blocks/{id}", x.requireAuth(http.HandlerFunc(x.block)))
	x.mux.Handle("GET /v2/contacts/blocks", x.requireAuth(http.HandlerFunc(x.blocks)))
	x.mux.Handle("GET /v2/robots/conversations/{id}", x.requireAuth(http.HandlerFunc(x.conversationRobots)))
	x.mux.Handle("GET /v2/calls/config", x.requireAuth(http.HandlerFunc(x.livekitCallConfig)))
	x.mux.Handle("POST /v2/calls/invite", x.requireAuth(http.HandlerFunc(x.inviteCall)))
	x.mux.Handle("GET /v2/calls/{id}", x.requireAuth(http.HandlerFunc(x.getCall)))
	x.mux.Handle("POST /v2/calls/{id}/accept", x.requireAuth(http.HandlerFunc(x.acceptCall)))
	x.mux.Handle("POST /v2/calls/{id}/reject", x.requireAuth(http.HandlerFunc(x.rejectCall)))
	x.mux.Handle("POST /v2/calls/{id}/cancel", x.requireAuth(http.HandlerFunc(x.cancelCall)))
	x.mux.Handle("POST /v2/calls/{id}/hangup", x.requireAuth(http.HandlerFunc(x.hangupCall)))
	x.mux.Handle("POST /v2/calls/{id}/token", x.requireAuth(http.HandlerFunc(x.livekitCallToken)))
	x.mux.Handle("POST /v2/reports", x.requireAuth(http.HandlerFunc(x.report)))
	x.mux.Handle("GET /v2/announcements", x.requireAuth(http.HandlerFunc(x.announcements)))
	x.mux.Handle("POST /v2/announcements/{id}/read", x.requireAuth(http.HandlerFunc(x.readAnnouncement)))
	x.mux.Handle("GET /v2/admin/stats", x.requireAdmin(http.HandlerFunc(x.adminStats)))
	x.mux.Handle("GET /v2/admin/dashboard", x.requireAdmin(http.HandlerFunc(x.adminStats)))
	x.mux.Handle("GET /v2/admin/users", x.requireAdmin(http.HandlerFunc(x.adminUsers)))
	x.mux.Handle("POST /v2/admin/users", x.requireAdmin(http.HandlerFunc(x.createAdminUser)))
	x.mux.Handle("GET /v2/admin/users/{id}", x.requireAdmin(http.HandlerFunc(x.adminUserOverview)))
	x.mux.Handle("GET /v2/admin/users/{id}/friends", x.requireAdmin(http.HandlerFunc(x.adminUserFriends)))
	x.mux.Handle("GET /v2/admin/users/{id}/blocks", x.requireAdmin(http.HandlerFunc(x.adminUserBlocks)))
	x.mux.Handle("GET /v2/admin/users/{id}/devices", x.requireAdmin(http.HandlerFunc(x.adminUserDevices)))
	x.mux.Handle("GET /v2/admin/users/{id}/friends/{friendId}/messages", x.requireAdmin(http.HandlerFunc(x.adminUserFriendMessages)))
	x.mux.Handle("POST /v2/admin/users/{id}/friends/{friendId}/messages/{messageId}/recall", x.requireAdmin(http.HandlerFunc(x.adminRecallUserMessage)))
	x.mux.Handle("POST /v2/admin/users/{id}/system-message", x.requireAdmin(http.HandlerFunc(x.adminUserSystemMessage)))
	x.mux.Handle("POST /v2/admin/users/{id}/ban", x.requireAdmin(http.HandlerFunc(x.adminBan)))
	x.mux.Handle("POST /v2/admin/users/{id}/unban", x.requireAdmin(http.HandlerFunc(x.adminUnban)))
	x.mux.Handle("GET /v2/admin/reports", x.requireAdmin(http.HandlerFunc(x.adminReports)))
	x.mux.Handle("POST /v2/admin/reports/{id}/resolve", x.requireAdmin(http.HandlerFunc(x.resolveReport)))
	x.mux.Handle("GET /v2/admin/audit-logs", x.requireAdmin(http.HandlerFunc(x.auditLogs)))
	x.mux.Handle("GET /v2/admin/messages", x.requireAdmin(http.HandlerFunc(x.adminMessages)))
	x.mux.Handle("GET /v2/admin/tasks/media-cleanup", x.requireAdmin(http.HandlerFunc(x.adminMediaCleanupStatus)))
	x.mux.Handle("GET /v2/admin/tasks", x.requireAdmin(http.HandlerFunc(x.adminTasks)))
	x.mux.Handle("GET /v2/admin/backups", x.requireAdmin(http.HandlerFunc(x.adminBackups)))
	x.mux.Handle("GET /v2/admin/friendships", x.requireAdmin(http.HandlerFunc(x.adminFriendships)))
	x.mux.Handle("GET /v2/admin/feedback", x.requireAdmin(http.HandlerFunc(x.adminFeedback)))
	x.mux.Handle("GET /v2/admin/client-diagnostics", x.requireAdmin(http.HandlerFunc(x.adminClientDiagnostics)))
	x.mux.Handle("GET /v2/admin/push", x.requireAdmin(http.HandlerFunc(x.adminPushStatus)))
	x.mux.Handle("GET /v2/admin/access", x.requireAdmin(http.HandlerFunc(x.adminAccess)))
	x.mux.Handle("GET /v2/admin/administrators", x.requireAdmin(http.HandlerFunc(x.adminAdministrators)))
	x.mux.Handle("POST /v2/admin/administrators", x.requireAdmin(http.HandlerFunc(x.createAdministrator)))
	x.mux.Handle("PATCH /v2/admin/administrators/{id}", x.requireAdmin(http.HandlerFunc(x.updateAdministrator)))
	x.mux.Handle("POST /v2/admin/administrators/{id}/reset-password", x.requireAdmin(http.HandlerFunc(x.resetAdministratorPassword)))
	x.mux.Handle("GET /v2/admin/roles", x.requireAdmin(http.HandlerFunc(x.adminRoles)))
	x.mux.Handle("POST /v2/admin/roles", x.requireAdmin(http.HandlerFunc(x.createAdminRole)))
	x.mux.Handle("PATCH /v2/admin/roles/{id}", x.requireAdmin(http.HandlerFunc(x.updateAdminRole)))
	x.mux.Handle("DELETE /v2/admin/roles/{id}", x.requireAdmin(http.HandlerFunc(x.deleteAdminRole)))
	x.mux.Handle("GET /v2/admin/media", x.requireAdmin(http.HandlerFunc(x.adminMedia)))
	x.mux.Handle("GET /v2/admin/online", x.requireAdmin(http.HandlerFunc(x.adminOnline)))
	x.mux.Handle("GET /v2/admin/announcements", x.requireAdmin(http.HandlerFunc(x.adminAnnouncements)))
	x.mux.Handle("POST /v2/admin/announcements", x.requireAdmin(http.HandlerFunc(x.createAnnouncement)))
	x.mux.Handle("PUT /v2/admin/announcements/{id}", x.requireAdmin(http.HandlerFunc(x.updateAnnouncement)))
	x.mux.Handle("POST /v2/admin/announcements/{id}/publish", x.requireAdmin(http.HandlerFunc(x.publishAnnouncement)))
	x.mux.Handle("POST /v2/admin/announcements/{id}/withdraw", x.requireAdmin(http.HandlerFunc(x.withdrawAnnouncement)))
	x.mux.Handle("DELETE /v2/admin/announcements/{id}", x.requireAdmin(http.HandlerFunc(x.deleteAnnouncement)))
	x.mux.Handle("GET /v2/admin/calls", x.requireAdmin(http.HandlerFunc(x.adminCalls)))
	x.mux.Handle("GET /v2/admin/health", x.requireAdmin(http.HandlerFunc(x.adminHealth)))
	x.mux.Handle("GET /v2/admin/groups", x.requireAdmin(http.HandlerFunc(x.adminGroups)))
	x.mux.Handle("GET /v2/admin/groups/{id}", x.requireAdmin(http.HandlerFunc(x.adminGroupOverview)))
	x.mux.Handle("GET /v2/admin/groups/{id}/members", x.requireAdmin(http.HandlerFunc(x.adminGroupMembers)))
	x.mux.Handle("PATCH /v2/admin/groups/{id}/members/{userId}", x.requireAdmin(http.HandlerFunc(x.adminGroupMemberAction)))
	x.mux.Handle("DELETE /v2/admin/groups/{id}/members/{userId}", x.requireAdmin(http.HandlerFunc(x.adminGroupMemberAction)))
	x.mux.Handle("POST /v2/admin/groups/{id}/disband", x.requireAdmin(http.HandlerFunc(x.disbandGroup)))
	x.mux.Handle("GET /v2/admin/sensitive-words", x.requireAdmin(http.HandlerFunc(x.sensitiveWords)))
	x.mux.Handle("POST /v2/admin/sensitive-words", x.requireAdmin(http.HandlerFunc(x.addSensitiveWord)))
	x.mux.Handle("DELETE /v2/admin/sensitive-words/{id}", x.requireAdmin(http.HandlerFunc(x.deleteSensitiveWord)))
	x.mux.Handle("GET /v2/admin/settings", x.requireAdmin(http.HandlerFunc(x.settings)))
	x.mux.Handle("PUT /v2/admin/settings", x.requireAdmin(http.HandlerFunc(x.updateSettings)))
	x.mux.Handle("GET /v2/admin/client-versions", x.requireAdmin(http.HandlerFunc(x.adminClientVersions)))
	x.mux.Handle("GET /v2/admin/client-versions/{platform}/history", x.requireAdmin(http.HandlerFunc(x.adminClientVersionHistory)))
	x.mux.Handle("PUT /v2/admin/client-versions/{platform}", x.requireAdmin(http.HandlerFunc(x.updateAdminClientVersion)))
	x.mux.Handle("GET /v2/admin/moments", x.requireAdmin(http.HandlerFunc(x.adminMoments)))
	x.mux.Handle("POST /v2/admin/moments/{id}/moderate", x.requireAdmin(http.HandlerFunc(x.moderateAdminMoment)))
	x.mux.Handle("GET /v2/admin/sticker-packs", x.requireAdmin(http.HandlerFunc(x.adminStickerPacks)))
	x.mux.Handle("GET /v2/admin/sticker-categories", x.requireAdmin(http.HandlerFunc(x.adminStickerCategories)))
	x.mux.Handle("POST /v2/admin/sticker-categories", x.requireAdmin(http.HandlerFunc(x.adminSaveStickerCategory)))
	x.mux.Handle("PUT /v2/admin/sticker-categories/{id}", x.requireAdmin(http.HandlerFunc(x.adminSaveStickerCategory)))
	x.mux.Handle("POST /v2/admin/sticker-packs", x.requireAdmin(http.HandlerFunc(x.adminSaveStickerPack)))
	x.mux.Handle("PUT /v2/admin/sticker-packs/{id}", x.requireAdmin(http.HandlerFunc(x.adminSaveStickerPack)))
	x.mux.Handle("POST /v2/admin/sticker-packs/{id}/items", x.requireAdmin(http.HandlerFunc(x.adminSaveStickerItem)))
	x.mux.Handle("PUT /v2/admin/sticker-packs/{id}/items/{itemId}", x.requireAdmin(http.HandlerFunc(x.adminSaveStickerItem)))
	x.mux.Handle("POST /v2/admin/sticker-packs/{id}/review", x.requireAdmin(http.HandlerFunc(x.reviewAdminStickerPack)))
	x.registerWukongAdminRoutes("/v2/admin")
	x.registerBusinessAdminRoutes("/v2/admin")
	x.mux.HandleFunc("/api/v2/admin/", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
		x.mux.ServeHTTP(w, r)
	})
}

func (x *API) registerWukongAdminRoutes(prefix string) {
	x.mux.Handle("GET "+prefix+"/wukong/overview", x.requireAdmin(http.HandlerFunc(x.adminWukongOverview)))
	x.mux.Handle("GET "+prefix+"/wukong/settings", x.requireAdmin(http.HandlerFunc(x.adminWukongSettings)))
	x.mux.Handle("GET "+prefix+"/wukong/nodes", x.requireAdmin(http.HandlerFunc(x.adminWukongNodes)))
	x.mux.Handle("GET "+prefix+"/wukong/connections", x.requireAdmin(http.HandlerFunc(x.adminWukongConnections)))
	x.mux.Handle("GET "+prefix+"/wukong/channels", x.requireAdmin(http.HandlerFunc(x.adminWukongChannels)))
	x.mux.Handle("GET "+prefix+"/wukong/messages", x.requireAdmin(http.HandlerFunc(x.adminWukongMessages)))
	x.mux.Handle("GET "+prefix+"/wukong/devices", x.requireAdmin(http.HandlerFunc(x.adminWukongDevices)))
	x.mux.Handle("POST "+prefix+"/wukong/devices/{uid}/quit", x.requireAdmin(http.HandlerFunc(x.quitWukongDevice)))
	x.mux.Handle("GET "+prefix+"/wukong/system-users", x.requireAdmin(http.HandlerFunc(x.adminWukongSystemUsers)))
	x.mux.Handle("PUT "+prefix+"/wukong/system-users/{uid}", x.requireAdmin(http.HandlerFunc(x.setWukongSystemUser)))
	x.mux.Handle("GET "+prefix+"/wukong/robots", x.requireAdmin(http.HandlerFunc(x.adminRobotProfiles)))
	x.mux.Handle("PUT "+prefix+"/wukong/robots/{uid}", x.requireAdmin(http.HandlerFunc(x.configureRobotProfile)))
	x.mux.Handle("GET "+prefix+"/wukong/plugins", x.requireAdmin(http.HandlerFunc(x.adminWukongPlugins)))
	x.mux.Handle("GET "+prefix+"/wukong/plugins/{no}/logs", x.requireAdmin(http.HandlerFunc(x.adminWukongPluginLogs)))
	x.mux.Handle("POST "+prefix+"/wukong/plugins/install", x.requireAdmin(http.HandlerFunc(x.installWukongPlugin)))
	x.mux.Handle("PUT "+prefix+"/wukong/plugins/{no}/upgrade", x.requireAdmin(http.HandlerFunc(x.upgradeWukongPlugin)))
	x.mux.Handle("POST "+prefix+"/wukong/plugins/{no}/enable", x.requireAdmin(http.HandlerFunc(x.enableWukongPlugin)))
	x.mux.Handle("POST "+prefix+"/wukong/plugins/{no}/disable", x.requireAdmin(http.HandlerFunc(x.disableWukongPlugin)))
	x.mux.Handle("GET "+prefix+"/wukong/plugin-events", x.requireAdmin(http.HandlerFunc(x.adminWukongPluginEvents)))
	x.mux.Handle("PUT "+prefix+"/wukong/plugins/{no}/config", x.requireAdmin(http.HandlerFunc(x.updateWukongPluginConfig)))
	x.mux.Handle("DELETE "+prefix+"/wukong/plugins/{no}", x.requireAdmin(http.HandlerFunc(x.uninstallWukongPlugin)))
	x.mux.Handle("GET "+prefix+"/livekit/rooms", x.requireAdmin(http.HandlerFunc(x.adminLiveKitRooms)))
	x.mux.Handle("GET "+prefix+"/livekit/metrics", x.requireAdmin(http.HandlerFunc(x.adminLiveKitMetrics)))
	x.mux.Handle("DELETE "+prefix+"/livekit/rooms/{room}", x.requireAdmin(http.HandlerFunc(x.deleteLiveKitRoom)))
	x.mux.Handle("GET "+prefix+"/livekit/rooms/{room}/participants", x.requireAdmin(http.HandlerFunc(x.adminLiveKitParticipants)))
	x.mux.Handle("DELETE "+prefix+"/livekit/rooms/{room}/participants/{identity}", x.requireAdmin(http.HandlerFunc(x.removeLiveKitParticipant)))
}

func (x *API) livekitCallConfig(w http.ResponseWriter, _ *http.Request) {
	if x.livekit == nil || x.livekitSetupErr != nil {
		writeError(w, http.StatusServiceUnavailable, "LIVEKIT_UNAVAILABLE", "LiveKit is unavailable")
		return
	}
	write(w, http.StatusOK, map[string]any{
		"provider":             "livekit",
		"url":                  x.livekit.URL(),
		"inviteTimeoutSeconds": int(x.cfg.CallInviteTTL.Seconds()),
		"tokenTtlSeconds":      int(x.livekit.TokenTTL().Seconds()),
		"maxParticipants":      livekitcontrol.MaxCallParticipants,
		"supportsScreenShare":  true,
	})
}

func (x *API) inviteCall(w http.ResponseWriter, r *http.Request) {
	var p struct {
		CallID, ConversationID, CalleeUserID, MediaType string
	}
	if decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	call, duplicate, err := x.app.InviteCall(uid(r), p.ConversationID, p.CalleeUserID, p.CallID, p.MediaType)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusCreated, map[string]any{"call": call, "duplicate": duplicate})
}

func (x *API) getCall(w http.ResponseWriter, r *http.Request) {
	call, err := x.app.GetCall(uid(r), r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"call": call})
}

func (x *API) callAction(w http.ResponseWriter, r *http.Request, action string) {
	var p struct{ Reason string }
	if r.ContentLength != 0 && decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	call, duplicate, err := x.app.TransitionCall(uid(r), r.PathValue("id"), action, p.Reason)
	if err != nil {
		handleErr(w, err)
		return
	}
	if x.livekit != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if action == "accept" {
			if err = x.livekit.EnsureCallRoom(ctx, call.ID, call.ConversationID, call.MediaType); err != nil {
				slog.Warn("LiveKit room provisioning failed", "callId", call.ID, "error", err)
				writeError(w, http.StatusServiceUnavailable, "LIVEKIT_UNAVAILABLE", "LiveKit room is unavailable; retry the request")
				return
			}
		} else if isTerminalCallStatus(call.Status) {
			if err = x.livekit.DeleteCallRoom(ctx, call.ID); err != nil {
				slog.Warn("LiveKit room cleanup failed", "callId", call.ID, "error", err)
			}
		} else if action == "hangup" || action == "end" {
			if err = x.livekit.RemoveParticipant(ctx, livekitcontrol.CallRoomName(call.ID), uid(r)); err != nil {
				slog.Warn("LiveKit participant cleanup failed", "callId", call.ID, "userId", uid(r), "error", err)
			}
		}
	}
	write(w, http.StatusOK, map[string]any{"call": call, "duplicate": duplicate})
}

func isTerminalCallStatus(status string) bool {
	return status == "rejected" || status == "cancelled" || status == "missed" || status == "ended"
}

func (x *API) livekitCallToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if x.livekit == nil || x.livekitSetupErr != nil {
		writeError(w, http.StatusServiceUnavailable, "LIVEKIT_UNAVAILABLE", "LiveKit is unavailable")
		return
	}
	call, err := x.app.GetCall(uid(r), r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	if call.Status != "accepted" {
		writeError(w, http.StatusConflict, "CALL_NOT_ACCEPTED", "call must be accepted before joining media")
		return
	}
	if !callUserCanJoin(call, uid(r)) {
		writeError(w, http.StatusForbidden, "CALL_PARTICIPANT_INACTIVE", "call participant has not accepted or has already left")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err = x.livekit.EnsureCallRoom(ctx, call.ID, call.ConversationID, call.MediaType); err != nil {
		slog.Warn("LiveKit room provisioning failed", "callId", call.ID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "LIVEKIT_UNAVAILABLE", "LiveKit room is unavailable; retry the request")
		return
	}
	session, err := x.livekit.IssueParticipant(call.ID, uid(r), call.ConversationID, call.MediaType)
	if err != nil {
		slog.Error("LiveKit participant token failed", "callId", call.ID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "LIVEKIT_UNAVAILABLE", "LiveKit token is unavailable")
		return
	}
	write(w, http.StatusOK, map[string]any{"session": session})
}
func (x *API) acceptCall(w http.ResponseWriter, r *http.Request) { x.callAction(w, r, "accept") }
func (x *API) rejectCall(w http.ResponseWriter, r *http.Request) { x.callAction(w, r, "reject") }
func (x *API) cancelCall(w http.ResponseWriter, r *http.Request) { x.callAction(w, r, "cancel") }
func (x *API) hangupCall(w http.ResponseWriter, r *http.Request) { x.callAction(w, r, "hangup") }

func callUserCanJoin(call *model.CallSession, userID string) bool {
	joined := false
	for _, candidate := range call.JoinedUserIDs {
		joined = joined || candidate == userID
	}
	if !joined {
		return false
	}
	for _, users := range [][]string{call.DeclinedUserIDs, call.LeftUserIDs} {
		for _, candidate := range users {
			if candidate == userID {
				return false
			}
		}
	}
	return true
}
func (x *API) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(media.WithClientPlatform(r.Context(), r.Header.Get("X-Client-Platform")))
		started := time.Now()
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 80 {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		capture := &responseCapture{ResponseWriter: w}
		w = capture
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/v2/auth/") || r.URL.Path == "/v2/admin/auth/login" {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Pragma", "no-cache")
		}
		origin := r.Header.Get("Origin")
		if origin != "" && x.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Client-Platform,X-Request-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			if origin != "" && !x.originAllowed(origin) {
				writeError(w, http.StatusForbidden, "FORBIDDEN_ORIGIN", "origin is not allowed")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		x.app.Metrics.Requests.Add(1)
		probe := r.URL.Path == "/health" || r.URL.Path == "/ready" || r.URL.Path == "/metrics"
		x.app.Metrics.HTTPInFlight.Add(1)
		defer func() {
			if v := recover(); v != nil {
				slog.Error("请求处理发生异常", "event", "http.panic", "requestId", requestID, "method", r.Method, "path", r.URL.Path, "error", v)
				writeError(w, http.StatusInternalServerError, "INTERNAL", "internal server error")
			}
			x.app.Metrics.HTTPInFlight.Add(-1)
			status := capture.status
			if status == 0 {
				status = http.StatusOK
			}
			duration := time.Since(started)
			x.app.Metrics.ObserveHTTP(duration, status)
			if probe {
				return
			}
			path := r.Pattern
			if path == "" {
				path = r.URL.Path
			}
			attributes := []any{"event", "http.request", "requestId", requestID, "method", r.Method, "path", path, "status", status, "durationMs", duration.Milliseconds(), "bytes", capture.bytes}
			sampleRate := x.cfg.HTTPLogSuccessSampleRate
			sampleEvery := int64(1)
			if sampleRate > 0 && sampleRate < 1 {
				sampleEvery = max(int64(1), int64(1/sampleRate))
			}
			sampled := sampleRate >= 1 || (sampleRate > 0 && x.app.Metrics.Requests.Load()%sampleEvery == 0)
			switch {
			case status >= 500:
				slog.Error("HTTP request completed", attributes...)
			case status >= 400:
				slog.Warn("HTTP request completed", attributes...)
			case duration >= 250*time.Millisecond || sampled:
				slog.Info("HTTP request completed", attributes...)
			}
		}()
		adminPath := strings.HasPrefix(r.URL.Path, "/v2/admin/") || strings.HasPrefix(r.URL.Path, "/api/v2/admin/")
		controlPath := r.URL.Path == "/v2/config/version"
		if !probe && !adminPath && !controlPath {
			if maintenance, announcement := x.app.MaintenanceStatus(); maintenance {
				if announcement == "" {
					announcement = "系统正在维护，请稍后重试"
				}
				writeError(w, http.StatusServiceUnavailable, "MAINTENANCE", announcement)
				return
			}
		}
		limitKey := "http:" + x.clientIP(r)
		limitMax := 300
		if strings.HasPrefix(r.URL.Path, "/internal/wukong/") {
			limitKey = "http:wukong-internal:" + x.clientIP(r)
			limitMax = x.cfg.WukongInternalRateLimitPerMinute
			if limitMax == 0 {
				limitMax = 120000
			}
		}
		if !probe && !x.allow(r.Context(), limitKey, limitMax, time.Minute) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (x *API) allow(parent context.Context, key string, max int, window time.Duration) bool {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	if allowed, err := x.app.AllowRate(ctx, key, max, window); err == nil {
		return allowed
	}
	// Redis 短时不可用时保留单节点保护；生产 readiness 会同时报告
	// Redis 故障，恢复后自动回到全局原子限流。
	return x.limits.allow(key, max, window)
}
func (x *API) originAllowed(origin string) bool {
	for _, allowed := range x.cfg.AllowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}
func (x *API) clientIP(r *http.Request) string { return netutil.ClientIP(r, x.cfg.TrustProxy) }
func (x *API) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, err := x.parseRequestToken(r)
		if err != nil {
			writeError(w, 401, "UNAUTHENTICATED", err.Error())
			return
		}
		u, err := x.app.UserContext(r.Context(), uid)
		if err != nil || u.Banned {
			writeError(w, 403, "FORBIDDEN", "account unavailable")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, uid)))
	})
}
func (x *API) requireUserToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, err := x.parseRequestToken(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", err.Error())
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, userID)))
	})
}
func (x *API) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account, err := x.authenticatedAdmin(r)
		if err != nil {
			reason := "authentication store unavailable"
			if errors.Is(err, errInvalidAdminCredential) {
				reason = "invalid credential"
			}
			x.app.RecordAdminAudit("unknown", "admin.authorization.failed", "admin_request", r.URL.Path, "failed", x.clientIP(r), map[string]any{"method": r.Method, "reason": reason})
			if !errors.Is(err, errInvalidAdminCredential) {
				handleErr(w, err)
				return
			}
			writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid admin credential")
			return
		}
		adminID, role := account.ID, account.RoleID
		if !adminAccountAllowed(account, adminPermission(r.Method, r.URL.Path)) {
			x.app.RecordAdminAudit(adminID, "admin.authorization.denied", "admin_request", r.URL.Path, "failed", x.clientIP(r), map[string]any{"method": r.Method, "role": role})
			writeError(w, 403, "FORBIDDEN", "administrator role is not permitted")
			return
		}
		reason := ""
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			var err error
			reason, err = adminWriteControl(r)
			if err != nil {
				x.app.RecordAdminAudit(adminID, "admin.request", "admin_request", r.URL.Path, "failed", x.clientIP(r), map[string]any{"method": r.Method, "role": role, "status": http.StatusBadRequest, "reason": reason})
				writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
				return
			}
		}
		ctx := context.WithValue(context.WithValue(r.Context(), userKey, adminID), roleKey, role)
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		capture := &responseCapture{ResponseWriter: w}
		next.ServeHTTP(capture, r.WithContext(ctx))
		status := capture.status
		if status == 0 {
			status = http.StatusOK
		}
		result := "success"
		if status >= 400 {
			result = "failed"
		}
		metadata := map[string]any{"method": r.Method, "role": role, "status": status}
		if reason != "" {
			metadata["reason"] = reason
		}
		x.app.RecordAdminAudit(adminID, "admin.request", "admin_request", r.URL.Path, result, x.clientIP(r), metadata)
	})
}

var errInvalidAdminCredential = errors.New("invalid administrator credential")

func (x *API) authenticatedAdmin(r *http.Request) (*store.AdminAccount, error) {
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if raw == "" {
		return nil, errInvalidAdminCredential
	}
	claims, err := x.auth.ParseClaims(raw, "admin")
	if err != nil || claims.AuthVersion <= 0 {
		return nil, errInvalidAdminCredential
	}
	account, err := x.app.AdminAccount(r.Context(), claims.Subject)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errInvalidAdminCredential
		}
		return nil, err
	}
	if account.Status != "active" || account.AuthVersion != claims.AuthVersion {
		return nil, errInvalidAdminCredential
	}
	return account, nil
}

func (x *API) requireAdminSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account, err := x.authenticatedAdmin(r)
		if err != nil {
			if !errors.Is(err, errInvalidAdminCredential) {
				handleErr(w, err)
				return
			}
			writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid admin credential")
			return
		}
		ctx := context.WithValue(context.WithValue(r.Context(), userKey, account.ID), roleKey, account.RoleID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func adminWriteControl(r *http.Request) (reason string, err error) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "multipart/form-data") {
		// Signed plugin packages retain their existing manifest-aware
		// confirmation and reason validation in the multipart handler.
		return "", nil
	}
	const maxAdminWriteBody = 1024 * 1024
	raw, readErr := io.ReadAll(io.LimitReader(r.Body, maxAdminWriteBody+1))
	if r.Body != nil {
		_ = r.Body.Close()
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if readErr != nil || len(raw) > maxAdminWriteBody {
		return "", errors.New("invalid admin write body")
	}
	var control struct {
		Confirmed bool   `json:"confirmed"`
		Reason    string `json:"reason"`
	}
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &control) != nil {
		return "", errors.New("invalid admin write control")
	}
	reason = strings.TrimSpace(control.Reason)
	if !confirmedReason(control.Confirmed, reason) {
		return reason, errors.New("admin write is not confirmed")
	}
	return reason, nil
}
func (x *API) parseRequestToken(r *http.Request) (string, error) {
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if raw == "" {
		return "", errors.New("missing access token")
	}
	return x.auth.Parse(raw, "access")
}
func uid(r *http.Request) string { v, _ := r.Context().Value(userKey).(string); return v }
func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	d := json.NewDecoder(io.LimitReader(r.Body, 1024*1024))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code, msg string) {
	write(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}
func handleErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrInvalid):
		writeError(w, 400, "INVALID_ARGUMENT", err.Error())
	case errors.Is(err, app.ErrForbidden), errors.Is(err, store.ErrForbidden):
		writeError(w, 403, "FORBIDDEN", err.Error())
	case errors.Is(err, app.ErrNotFound), errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "NOT_FOUND", err.Error())
	case errors.Is(err, app.ErrConflict), errors.Is(err, store.ErrConflict):
		writeError(w, 409, "CONFLICT", err.Error())
	case errors.Is(err, app.ErrUnavailable), errors.Is(err, store.ErrUnsupported):
		writeError(w, 503, "IM_UNAVAILABLE", "instant messaging service is temporarily unavailable")
	default:
		slog.Error("HTTP handler failed", "event", "http.handler.error", "error", err)
		writeError(w, 500, "INTERNAL", "internal server error")
	}
}

func (x *API) health(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"status": "ok", "service": "qingwaguagua-im", "uptimeSeconds": int(time.Since(x.started).Seconds())})
}
func (x *API) authPolicy(w http.ResponseWriter, _ *http.Request) {
	write(w, http.StatusOK, x.app.AuthPolicy())
}
func (x *API) ready(w http.ResponseWriter, r *http.Request) {
	if err := x.SetupError(); err != nil {
		writeError(w, 503, "NOT_READY", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := x.app.Ready(ctx); err != nil {
		writeError(w, 503, "NOT_READY", err.Error())
		return
	}
	if x.imSessions != nil {
		if err := x.imSessions.Ready(ctx); err != nil {
			writeError(w, 503, "NOT_READY", "instant messaging service is not ready")
			return
		}
	}
	write(w, 200, map[string]string{"status": "ready"})
}
func (x *API) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	metrics := &x.app.Metrics
	fmt.Fprintf(w, "im_http_requests_total %d\nim_http_requests_in_flight %d\nim_messages_sent_total %d\nim_errors_total %d\nim_runtime_retention_deleted_total %d\n",
		metrics.Requests.Load(), metrics.HTTPInFlight.Load(), metrics.Messages.Load(), metrics.Errors.Load(), metrics.RetentionDeleted.Load())
	for index, boundary := range app.HTTPDurationBuckets() {
		fmt.Fprintf(w, "im_http_request_duration_seconds_bucket{le=\"%g\"} %d\n", boundary.Seconds(), metrics.HTTPDurationBuckets[index].Load())
	}
	count := metrics.HTTPDurationCount.Load()
	fmt.Fprintf(w, "im_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\nim_http_request_duration_seconds_sum %g\nim_http_request_duration_seconds_count %d\n", count, float64(metrics.HTTPDurationNanoseconds.Load())/float64(time.Second), count)
	for index := range metrics.HTTPStatusClasses {
		fmt.Fprintf(w, "im_http_responses_total{class=\"%dxx\"} %d\n", index+1, metrics.HTTPStatusClasses[index].Load())
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	fmt.Fprintf(w, "im_runtime_goroutines %d\nim_runtime_heap_alloc_bytes %d\nim_runtime_heap_inuse_bytes %d\nim_runtime_gc_cycles_total %d\n", runtime.NumGoroutine(), memory.HeapAlloc, memory.HeapInuse, memory.NumGC)
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	stats, err := x.app.RuntimeStats(ctx)
	if err != nil {
		fmt.Fprintln(w, "im_store_metrics_up 0")
		return
	}
	fmt.Fprintf(w, "im_store_metrics_up 1\nim_db_pool_max_connections %d\nim_db_pool_total_connections %d\nim_db_pool_idle_connections %d\nim_db_pool_acquired_connections %d\nim_db_pool_acquires_total %d\nim_db_pool_empty_acquires_total %d\nim_db_pool_canceled_acquires_total %d\nim_db_pool_acquire_duration_seconds_total %g\n",
		stats.DBMaxConnections, stats.DBTotalConnections, stats.DBIdleConnections, stats.DBAcquiredConnections, stats.DBAcquireCount, stats.DBEmptyAcquireCount, stats.DBCanceledAcquireCount, stats.DBAcquireDurationSeconds)
	fmt.Fprintf(w, "im_redis_pool_total_connections %d\nim_redis_pool_idle_connections %d\nim_redis_pool_timeouts_total %d\nim_push_outbox_pending %d\nim_push_outbox_oldest_seconds %g\n",
		stats.RedisTotalConnections, stats.RedisIdleConnections, stats.RedisTimeouts, stats.PushPending, stats.OldestPushSeconds)
	fmt.Fprintf(w, "im_wukong_outbox_pending %d\nim_wukong_outbox_oldest_seconds %g\nim_wukong_outbox_failed %d\nim_wukong_webhook_pending %d\nim_wukong_webhook_oldest_seconds %g\nim_wukong_webhook_failed %d\n",
		stats.WukongOutboxPending, stats.OldestWukongOutboxSeconds, stats.WukongOutboxFailed, stats.WukongWebhookPending, stats.OldestWukongWebhookSeconds, stats.WukongWebhookFailed)
}
func (x *API) requestCode(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Phone string `json:"phone"`
	}
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "valid phone is required")
		return
	}
	p.Phone = strings.TrimSpace(p.Phone)
	if !app.ValidPhoneNumber(p.Phone) {
		writeError(w, 400, "INVALID_ARGUMENT", "valid phone is required")
		return
	}
	if !x.allow(r.Context(), "otp-ip:"+x.clientIP(r), 5, 10*time.Minute) || !x.allow(r.Context(), "otp-phone:"+p.Phone, 3, 10*time.Minute) {
		w.Header().Set("Retry-After", "600")
		writeError(w, 429, "RATE_LIMITED", "verification requests are temporarily limited")
		return
	}
	if x.otp == nil {
		writeError(w, http.StatusServiceUnavailable, "SMS_NOT_CONFIGURED", "verification provider is not configured")
		return
	}
	if err := x.otp.Request(r.Context(), p.Phone); err != nil {
		writeError(w, http.StatusBadGateway, "SMS_UNAVAILABLE", "verification provider is unavailable")
		return
	}
	resp := map[string]any{"expiresIn": 300}
	write(w, 202, resp)
}
func (x *API) login(w http.ResponseWriter, r *http.Request) {
	var p struct{ Phone, Code, Name string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "phone and code are required")
		return
	}
	p.Phone, p.Code = strings.TrimSpace(p.Phone), strings.TrimSpace(p.Code)
	if !app.ValidPhoneNumber(p.Phone) || p.Code == "" {
		writeError(w, 400, "INVALID_ARGUMENT", "valid phone and code are required")
		return
	}
	if !x.allow(r.Context(), "login-ip:"+x.clientIP(r), 10, 10*time.Minute) || !x.allow(r.Context(), "login-phone:"+p.Phone, 8, 10*time.Minute) {
		w.Header().Set("Retry-After", "600")
		writeError(w, 429, "RATE_LIMITED", "too many login attempts")
		return
	}
	if x.otp == nil {
		writeError(w, http.StatusServiceUnavailable, "SMS_NOT_CONFIGURED", "verification provider is not configured")
		return
	}
	if err := x.otp.Verify(r.Context(), p.Phone, p.Code); err != nil {
		if errors.Is(err, errInvalidOTP) {
			writeError(w, 401, "INVALID_CODE", errInvalidOTP.Error())
		} else {
			writeError(w, http.StatusBadGateway, "SMS_UNAVAILABLE", "verification provider is unavailable")
		}
		return
	}
	u, err := x.app.Login(p.Phone, p.Name)
	if err != nil {
		handleErr(w, err)
		return
	}
	x.issueUserSession(w, r, u)
}
func (x *API) issueUserSession(w http.ResponseWriter, r *http.Request, u *model.User) {
	imSession, err := x.issueIMSession(r.Context(), u.ID, r.Header.Get("X-Client-Platform"))
	if err != nil {
		slog.Warn("WuKongIM user provisioning failed", "user_id", u.ID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "IM_UNAVAILABLE", "instant messaging service is temporarily unavailable")
		return
	}
	a, refresh, err := x.auth.Issue(u.ID)
	if err != nil {
		handleErr(w, err)
		return
	}
	refreshClaims, err := x.auth.ParseClaims(refresh, "refresh")
	if err != nil {
		handleErr(w, err)
		return
	}
	sum := sha256.Sum256([]byte(refresh))
	if err = x.app.CreateRefreshSession(refreshClaims.ID, u.ID, sum[:], refreshClaims.ExpiresAt.Time); err != nil {
		handleErr(w, err)
		return
	}
	response := map[string]any{"user": u, "accessToken": a, "refreshToken": refresh, "expiresIn": int(x.cfg.AccessTTL.Seconds())}
	if imSession != nil {
		response["imSession"] = imSession
	}
	write(w, 200, response)
}
func (x *API) register(w http.ResponseWriter, r *http.Request) {
	var p struct{ Phone, Code, Password, Name string }
	if decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "phone, code, password and name are required")
		return
	}
	p.Phone, p.Code, p.Name = strings.TrimSpace(p.Phone), strings.TrimSpace(p.Code), strings.TrimSpace(p.Name)
	if !app.ValidPhoneNumber(p.Phone) || p.Code == "" || p.Password == "" || p.Name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "valid phone, code, password and name are required")
		return
	}
	if !x.allow(r.Context(), "register-ip:"+x.clientIP(r), 8, 10*time.Minute) || !x.allow(r.Context(), "register-phone:"+p.Phone, 5, 10*time.Minute) {
		w.Header().Set("Retry-After", "600")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many registration attempts")
		return
	}
	if x.otp == nil {
		writeError(w, http.StatusServiceUnavailable, "SMS_NOT_CONFIGURED", "verification provider is not configured")
		return
	}
	if err := x.otp.Verify(r.Context(), p.Phone, p.Code); err != nil {
		writeError(w, http.StatusUnauthorized, "INVALID_CODE", "invalid or expired verification code")
		return
	}
	u, err := x.app.RegisterWithPassword(p.Phone, p.Name, p.Password)
	if err != nil {
		if err == app.ErrConflict {
			writeError(w, http.StatusConflict, "ACCOUNT_EXISTS", "account already exists")
		} else {
			handleErr(w, err)
		}
		return
	}
	x.issueUserSession(w, r, u)
}
func (x *API) passwordLogin(w http.ResponseWriter, r *http.Request) {
	var p struct{ Phone, Password string }
	if decode(r, &p) != nil {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "phone or password is incorrect")
		return
	}
	p.Phone = strings.TrimSpace(p.Phone)
	if !app.ValidPhoneNumber(p.Phone) {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "valid phone is required")
		return
	}
	if p.Password == "" {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "phone or password is incorrect")
		return
	}
	if !x.allow(r.Context(), "password-login-ip:"+x.clientIP(r), 12, 10*time.Minute) || !x.allow(r.Context(), "password-login-phone:"+p.Phone, 8, 10*time.Minute) {
		w.Header().Set("Retry-After", "600")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many login attempts")
		return
	}
	u, err := x.app.PasswordLogin(p.Phone, p.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "phone or password is incorrect")
		return
	}
	x.issueUserSession(w, r, u)
}

func (x *API) createQRLogin(w http.ResponseWriter, r *http.Request) {
	platform := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Client-Platform")))
	if platform != "web" && platform != "macos" {
		writeError(w, http.StatusBadRequest, "INVALID_PLATFORM", "QR login is available only on Web and desktop clients")
		return
	}
	if !x.allow(r.Context(), "qr-login-create:"+x.clientIP(r), 20, 10*time.Minute) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many QR login requests")
		return
	}
	var payload struct {
		ClientName string `json:"clientName"`
	}
	if decode(r, &payload) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	clientName := strings.TrimSpace(payload.ClientName)
	if clientName == "" {
		if platform == "web" {
			clientName = "青蛙呱呱网页版"
		} else {
			clientName = "青蛙呱呱桌面端"
		}
	}
	if len([]rune(clientName)) > 60 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "client name is too long")
		return
	}
	qrToken, err := newQRLoginSecret("ql_")
	if err != nil {
		handleErr(w, err)
		return
	}
	pollToken, err := newQRLoginSecret("qp_")
	if err != nil {
		handleErr(w, err)
		return
	}
	now := time.Now().UTC()
	ticket := store.QRLoginTicket{
		ID:             "qrlogin_" + newRequestID(),
		QRTokenHash:    qrLoginSecretHash(qrToken),
		PollTokenHash:  qrLoginSecretHash(pollToken),
		ClientPlatform: platform,
		ClientName:     clientName,
		CreatedAt:      now,
		ExpiresAt:      now.Add(2 * time.Minute),
	}
	if err = x.app.CreateQRLoginTicket(ticket); err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusCreated, map[string]any{
		"id":        ticket.ID,
		"qrPayload": "qingwaguagua://login/" + qrToken,
		"pollToken": pollToken,
		"expiresAt": ticket.ExpiresAt,
		"expiresIn": 120,
	})
}

func (x *API) inspectQRLogin(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Token string `json:"token"`
	}
	payload.Token = ""
	if decode(r, &payload) != nil || strings.TrimSpace(payload.Token) == "" || len(payload.Token) > 128 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "QR login token is required")
		return
	}
	now := time.Now().UTC()
	ticket, err := x.app.QRLoginTicketByToken(qrLoginSecretHash(strings.TrimSpace(payload.Token)))
	if err != nil {
		writeError(w, http.StatusNotFound, "QR_LOGIN_NOT_FOUND", "QR login request was not found")
		return
	}
	switch ticket.State(now) {
	case "expired":
		writeError(w, http.StatusGone, "QR_LOGIN_EXPIRED", "QR login request has expired")
		return
	case "confirmed", "consumed":
		writeError(w, http.StatusConflict, "QR_LOGIN_USED", "QR login request has already been used")
		return
	}
	write(w, http.StatusOK, map[string]any{
		"id":             ticket.ID,
		"status":         "pending",
		"clientPlatform": ticket.ClientPlatform,
		"clientName":     ticket.ClientName,
		"expiresAt":      ticket.ExpiresAt,
	})
}

func (x *API) confirmQRLogin(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Token string `json:"token"`
	}
	if decode(r, &payload) != nil || strings.TrimSpace(payload.Token) == "" || len(payload.Token) > 128 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "QR login token is required")
		return
	}
	if !x.allow(r.Context(), "qr-login-confirm:"+uid(r), 20, 10*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many QR login confirmations")
		return
	}
	ticket, err := x.app.ConfirmQRLoginTicket(qrLoginSecretHash(strings.TrimSpace(payload.Token)), uid(r), time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, app.ErrNotFound):
			writeError(w, http.StatusNotFound, "QR_LOGIN_NOT_FOUND", "QR login request was not found")
		case errors.Is(err, app.ErrForbidden):
			writeError(w, http.StatusGone, "QR_LOGIN_EXPIRED", "QR login request has expired or was already used")
		case errors.Is(err, app.ErrConflict):
			writeError(w, http.StatusConflict, "QR_LOGIN_USED", "QR login request has already been confirmed")
		default:
			handleErr(w, err)
		}
		return
	}
	write(w, http.StatusOK, map[string]any{"id": ticket.ID, "status": "confirmed"})
}

func (x *API) pollQRLogin(w http.ResponseWriter, r *http.Request) {
	platform := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Client-Platform")))
	if platform != "web" && platform != "macos" {
		writeError(w, http.StatusBadRequest, "INVALID_PLATFORM", "QR login is available only on Web and desktop clients")
		return
	}
	var payload struct {
		ID, PollToken string
	}
	if decode(r, &payload) != nil || strings.TrimSpace(payload.ID) == "" || strings.TrimSpace(payload.PollToken) == "" || len(payload.ID) > 80 || len(payload.PollToken) > 128 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "QR login ticket and poll token are required")
		return
	}
	if !x.allow(r.Context(), "qr-login-poll-ip:"+x.clientIP(r), 180, 5*time.Minute) || !x.allow(r.Context(), "qr-login-poll-ticket:"+payload.ID, 90, 5*time.Minute) {
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "QR login polling is temporarily limited")
		return
	}
	now := time.Now().UTC()
	ticket, consumed, err := x.app.ConsumeQRLoginTicket(strings.TrimSpace(payload.ID), qrLoginSecretHash(strings.TrimSpace(payload.PollToken)), now)
	if err != nil {
		writeError(w, http.StatusNotFound, "QR_LOGIN_NOT_FOUND", "QR login request was not found")
		return
	}
	if consumed {
		if ticket.UserID == "" || !x.app.IsActiveUser(ticket.UserID) {
			writeError(w, http.StatusForbidden, "QR_LOGIN_ACCOUNT_UNAVAILABLE", "the confirming account is unavailable")
			return
		}
		user, err := x.app.User(ticket.UserID)
		if err != nil {
			handleErr(w, err)
			return
		}
		x.issueUserSession(w, r, user)
		return
	}
	switch ticket.State(now) {
	case "pending":
		write(w, http.StatusAccepted, map[string]any{"status": "pending", "expiresAt": ticket.ExpiresAt})
	case "expired":
		writeError(w, http.StatusGone, "QR_LOGIN_EXPIRED", "QR login request has expired")
	default:
		writeError(w, http.StatusConflict, "QR_LOGIN_USED", "QR login request has already been used")
	}
}

func (x *API) passwordResetCode(w http.ResponseWriter, r *http.Request) {
	var p struct{ Phone string }
	if decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "valid phone is required")
		return
	}
	p.Phone = strings.TrimSpace(p.Phone)
	if !app.ValidPhoneNumber(p.Phone) {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "valid phone is required")
		return
	}
	if !x.allow(r.Context(), "reset-ip:"+x.clientIP(r), 5, 10*time.Minute) || !x.allow(r.Context(), "reset-phone:"+p.Phone, 3, 10*time.Minute) {
		w.Header().Set("Retry-After", "600")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "verification requests are temporarily limited")
		return
	}
	if x.otp == nil {
		writeError(w, http.StatusServiceUnavailable, "SMS_NOT_CONFIGURED", "verification provider is not configured")
		return
	}
	if err := x.otp.Request(r.Context(), p.Phone); err != nil {
		writeError(w, http.StatusBadGateway, "SMS_UNAVAILABLE", "verification provider is unavailable")
		return
	}
	write(w, http.StatusAccepted, map[string]any{"expiresIn": 300})
}
func (x *API) passwordReset(w http.ResponseWriter, r *http.Request) {
	var p struct{ Phone, Code, Password string }
	if decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "phone, code and password are required")
		return
	}
	p.Phone, p.Code = strings.TrimSpace(p.Phone), strings.TrimSpace(p.Code)
	if !app.ValidPhoneNumber(p.Phone) || p.Code == "" || p.Password == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "valid phone, code and password are required")
		return
	}
	if !x.allow(r.Context(), "reset-confirm-ip:"+x.clientIP(r), 8, 10*time.Minute) || !x.allow(r.Context(), "reset-confirm-phone:"+p.Phone, 5, 10*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many reset attempts")
		return
	}
	if x.otp == nil {
		writeError(w, http.StatusServiceUnavailable, "SMS_NOT_CONFIGURED", "verification provider is not configured")
		return
	}
	if err := x.otp.Verify(r.Context(), p.Phone, p.Code); err != nil {
		writeError(w, http.StatusUnauthorized, "INVALID_CODE", "invalid or expired verification code")
		return
	}
	if err := x.app.ResetPassword(p.Phone, p.Password); err != nil && err != app.ErrNotFound {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) logout(w http.ResponseWriter, r *http.Request) {
	var p struct{ RefreshToken string }
	if decode(r, &p) != nil || p.RefreshToken == "" {
		writeError(w, 400, "INVALID_ARGUMENT", "refreshToken is required")
		return
	}
	claims, err := x.auth.ParseClaims(p.RefreshToken, "refresh")
	if err != nil || claims.Subject != uid(r) {
		writeError(w, 401, "UNAUTHENTICATED", "invalid refresh token")
		return
	}
	if err = x.app.RevokeRefreshSession(claims.ID, claims.Subject); err != nil && err != app.ErrNotFound {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) adminLogin(w http.ResponseWriter, r *http.Request) {
	if !x.allow(r.Context(), "admin-login:"+x.clientIP(r), 5, 10*time.Minute) {
		x.app.RecordAdminAudit("unknown", "admin.login", "admin_session", "login", "failed", x.clientIP(r), map[string]any{"reason": "rate limited"})
		writeError(w, 429, "RATE_LIMITED", "too many administrator login attempts")
		return
	}
	var p struct{ Email, Password string }
	if decode(r, &p) != nil {
		x.app.RecordAdminAudit("unknown", "admin.login", "admin_session", "login", "failed", x.clientIP(r), map[string]any{"reason": "invalid request"})
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	account, err := x.app.AuthenticateAdmin(r.Context(), p.Email, p.Password)
	if err != nil {
		if app.IsAdminCredentialError(err) {
			x.app.RecordAdminAudit("unknown", "admin.login", "admin_session", "login", "failed", x.clientIP(r), map[string]any{"reason": "invalid credentials"})
			writeError(w, 401, "UNAUTHENTICATED", "invalid administrator credentials")
		} else {
			x.app.RecordAdminAudit("unknown", "admin.login", "admin_session", "login", "failed", x.clientIP(r), map[string]any{"reason": "authentication store unavailable"})
			handleErr(w, err)
		}
		return
	}
	if err = x.app.RecordAdminLogin(r.Context(), account.ID, time.Now().UTC()); err != nil {
		x.app.RecordAdminAudit(account.ID, "admin.login", "admin_session", "login", "failed", x.clientIP(r), map[string]any{"reason": "last login update failed"})
		handleErr(w, err)
		return
	}
	token, err := x.auth.IssueAdmin(account.ID, account.RoleID, 15*time.Minute, account.AuthVersion)
	if err != nil {
		x.app.RecordAdminAudit(account.ID, "admin.login", "admin_session", "login", "failed", x.clientIP(r), map[string]any{"reason": "token issuance failed"})
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(account.ID, "admin.login", "admin_session", "login", "success", x.clientIP(r), map[string]any{"role": account.RoleID})
	write(w, 200, map[string]any{"accessToken": token, "expiresIn": 900, "id": account.ID, "email": account.Email, "displayName": account.DisplayName, "roleId": account.RoleID, "roleName": account.RoleName, "permissions": account.Permissions})
}
func (x *API) refresh(w http.ResponseWriter, r *http.Request) {
	var p struct{ RefreshToken string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	claims, err := x.auth.ParseClaims(p.RefreshToken, "refresh")
	if err != nil {
		writeError(w, 401, "UNAUTHENTICATED", err.Error())
		return
	}
	user := claims.Subject
	u, err := x.app.User(user)
	if err != nil || u.Banned {
		writeError(w, 403, "FORBIDDEN", "account unavailable")
		return
	}
	imSession, err := x.issueIMSession(r.Context(), user, r.Header.Get("X-Client-Platform"))
	if err != nil {
		slog.Warn("WuKongIM user reprovisioning failed", "user_id", user, "error", err)
		writeError(w, http.StatusServiceUnavailable, "IM_UNAVAILABLE", "instant messaging service is temporarily unavailable")
		return
	}
	a, refresh, err := x.auth.Issue(user)
	if err != nil {
		handleErr(w, err)
		return
	}
	newClaims, err := x.auth.ParseClaims(refresh, "refresh")
	if err != nil {
		handleErr(w, err)
		return
	}
	sum := sha256.Sum256([]byte(refresh))
	if err = x.app.RotateRefreshSession(claims.ID, newClaims.ID, user, sum[:], newClaims.ExpiresAt.Time); err != nil {
		_ = x.app.RevokeAllRefreshSessions(user)
		writeError(w, 401, "REFRESH_REUSED", "refresh token is revoked or already used")
		return
	}
	response := map[string]any{"accessToken": a, "refreshToken": refresh, "expiresIn": int(x.cfg.AccessTTL.Seconds())}
	if imSession != nil {
		response["imSession"] = imSession
	}
	write(w, 200, response)
}

func (x *API) issueIMSession(ctx context.Context, userID, platform string) (*wukong.ImSession, error) {
	if !x.cfg.WukongEnabled {
		return nil, nil
	}
	if x.wukongSetupErr != nil {
		return nil, x.wukongSetupErr
	}
	if strings.TrimSpace(platform) == "" {
		return nil, nil
	}
	return x.imSessions.Issue(ctx, userID, platform)
}

func (x *API) requireClientPlatform(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		platform := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Client-Platform")))
		switch platform {
		case "android", "ios", "web", "macos":
			next(w, r)
		default:
			writeError(w, http.StatusBadRequest, "INVALID_PLATFORM", "X-Client-Platform must be android, ios, web or macos")
		}
	}
}

func (x *API) imSession(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Platform string `json:"platform"`
	}
	if decode(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if strings.TrimSpace(request.Platform) == "" {
		request.Platform = r.Header.Get("X-Client-Platform")
	}
	session, err := x.issueIMSession(r.Context(), uid(r), request.Platform)
	if err != nil {
		slog.Warn("WuKongIM session issue failed", "user_id", uid(r), "error", err)
		writeError(w, http.StatusServiceUnavailable, "IM_UNAVAILABLE", "instant messaging service is temporarily unavailable")
		return
	}
	if session == nil {
		writeError(w, http.StatusServiceUnavailable, "IM_DISABLED", "instant messaging service is not enabled")
		return
	}
	write(w, http.StatusOK, map[string]any{"imSession": session})
}
func (x *API) me(w http.ResponseWriter, r *http.Request) {
	u, err := x.app.User(uid(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	x.signAvatarURL(u)
	x.app.DecorateOwnProfile(u)
	write(w, 200, u)
}
func (x *API) updateMe(w http.ResponseWriter, r *http.Request) {
	var p store.UserProfileUpdate
	if decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	u, err := x.app.UpdateUserProfile(uid(r), p)
	if err != nil {
		if p.Handle != nil && errors.Is(err, app.ErrConflict) {
			writeError(w, http.StatusConflict, "HANDLE_TAKEN", "handle is already in use")
			return
		}
		if p.Handle != nil && errors.Is(err, app.ErrForbidden) {
			writeError(w, http.StatusForbidden, "HANDLE_CHANGE_LIMIT", "handle change limit reached")
			return
		}
		handleErr(w, err)
		return
	}
	x.signAvatarURL(u)
	x.app.DecorateOwnProfile(u)
	write(w, http.StatusOK, u)
}

func (x *API) requestAccountDeletionCode(w http.ResponseWriter, r *http.Request) {
	if !x.allow(r.Context(), "account-delete-code-user:"+uid(r), 3, 30*time.Minute) {
		w.Header().Set("Retry-After", "1800")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "verification requests are temporarily limited")
		return
	}
	if x.otp == nil {
		writeError(w, http.StatusServiceUnavailable, "SMS_NOT_CONFIGURED", "verification provider is not configured")
		return
	}
	user, err := x.app.User(uid(r))
	if err != nil || user.Phone == "" {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "account unavailable")
		return
	}
	if err = x.otp.Request(r.Context(), user.Phone); err != nil {
		writeError(w, http.StatusBadGateway, "SMS_UNAVAILABLE", "verification provider is unavailable")
		return
	}
	write(w, http.StatusAccepted, map[string]any{"expiresIn": 300})
}

func (x *API) deleteAccount(w http.ResponseWriter, r *http.Request) {
	deleted, err := x.app.AccountDeleted(uid(r))
	if err == nil && deleted {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		handleErr(w, err)
		return
	}
	var request struct {
		Code string `json:"code"`
	}
	if decode(r, &request) != nil || strings.TrimSpace(request.Code) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "verification code is required")
		return
	}
	if !x.allow(r.Context(), "account-delete-user:"+uid(r), 5, 30*time.Minute) {
		w.Header().Set("Retry-After", "1800")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many deletion attempts")
		return
	}
	if x.otp == nil {
		writeError(w, http.StatusServiceUnavailable, "SMS_NOT_CONFIGURED", "verification provider is not configured")
		return
	}
	user, err := x.app.User(uid(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	if err = x.otp.Verify(r.Context(), user.Phone, request.Code); err != nil {
		if errors.Is(err, errInvalidOTP) {
			writeError(w, http.StatusUnauthorized, "INVALID_CODE", errInvalidOTP.Error())
		} else {
			writeError(w, http.StatusBadGateway, "SMS_UNAVAILABLE", "verification provider is unavailable")
		}
		return
	}
	_, err = x.app.DeleteAccount(uid(r))
	if err == app.ErrConflict {
		writeError(w, http.StatusConflict, "GROUP_OWNERSHIP_REQUIRED", "transfer ownership or disband owned groups before deleting the account")
		return
	}
	if err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (x *API) signAvatarURL(user *model.User) {
	if user == nil {
		return
	}
	mediaID := user.AvatarMediaID
	if mediaID == "" {
		mediaID = avatarMediaIDFromPath(user.AvatarURL)
	}
	if mediaID == "" {
		return
	}
	user.AvatarURL = x.signedAvatarValue(mediaID)
}

func (x *API) signedAvatarValue(mediaID string) string {
	expires := time.Now().Add(15 * time.Minute).Unix()
	payload := mediaID + ":" + strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, []byte(x.cfg.JWTSecret))
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return "/v2/avatars/" + mediaID + "?expires=" + strconv.FormatInt(expires, 10) + "&signature=" + signature
}

func avatarMediaIDFromPath(value string) string {
	const prefix = "/v2/media/"
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	id := strings.TrimPrefix(value, prefix)
	if index := strings.IndexByte(id, '?'); index >= 0 {
		id = id[:index]
	}
	return strings.TrimSpace(id)
}

func (x *API) avatarDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	expires, err := strconv.ParseInt(r.URL.Query().Get("expires"), 10, 64)
	if err != nil || expires < time.Now().Unix() || expires > time.Now().Add(20*time.Minute).Unix() {
		writeError(w, http.StatusForbidden, "INVALID_AVATAR_URL", "avatar URL is invalid or expired")
		return
	}
	payload := id + ":" + strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, []byte(x.cfg.JWTSecret))
	_, _ = mac.Write([]byte(payload))
	expected := mac.Sum(nil)
	provided, err := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("signature"))
	if err != nil || subtle.ConstantTimeCompare(expected, provided) != 1 {
		writeError(w, http.StatusForbidden, "INVALID_AVATAR_URL", "avatar URL is invalid or expired")
		return
	}
	url, err := x.media.DownloadURL(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "avatar is unavailable")
		return
	}
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}
func (x *API) requestPhoneChangeCode(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Phone string `json:"phone"`
	}
	if decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "valid phone is required")
		return
	}
	p.Phone = strings.TrimSpace(p.Phone)
	if !app.ValidPhoneNumber(p.Phone) {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "valid phone is required")
		return
	}
	if !x.allow(r.Context(), "phone-change-user:"+uid(r), 3, 10*time.Minute) || !x.allow(r.Context(), "phone-change-phone:"+p.Phone, 3, 10*time.Minute) {
		w.Header().Set("Retry-After", "600")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "verification requests are temporarily limited")
		return
	}
	if x.otp == nil {
		writeError(w, http.StatusServiceUnavailable, "SMS_NOT_CONFIGURED", "verification provider is not configured")
		return
	}
	if err := x.otp.Request(r.Context(), p.Phone); err != nil {
		writeError(w, http.StatusBadGateway, "SMS_UNAVAILABLE", "verification provider is unavailable")
		return
	}
	write(w, http.StatusAccepted, map[string]any{"expiresIn": 300})
}
func (x *API) updatePhone(w http.ResponseWriter, r *http.Request) {
	var p struct{ Phone, Code string }
	if decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "phone and code are required")
		return
	}
	p.Phone, p.Code = strings.TrimSpace(p.Phone), strings.TrimSpace(p.Code)
	if !app.ValidPhoneNumber(p.Phone) || p.Code == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "valid phone and code are required")
		return
	}
	if x.otp == nil {
		writeError(w, http.StatusServiceUnavailable, "SMS_NOT_CONFIGURED", "verification provider is not configured")
		return
	}
	if err := x.otp.Verify(r.Context(), p.Phone, p.Code); err != nil {
		if errors.Is(err, errInvalidOTP) {
			writeError(w, http.StatusUnauthorized, "INVALID_CODE", errInvalidOTP.Error())
		} else {
			writeError(w, http.StatusBadGateway, "SMS_UNAVAILABLE", "verification provider is unavailable")
		}
		return
	}
	u, err := x.app.UpdateUserPhone(uid(r), p.Phone)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, u)
}
func (x *API) userDevices(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.UserDevices(uid(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}
func (x *API) favorites(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := x.app.Favorites(uid(r), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}
func (x *API) setFavorite(w http.ResponseWriter, r *http.Request) {
	if err := x.app.SetFavorite(uid(r), r.PathValue("messageId"), r.Method == http.MethodPut); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) feedback(w http.ResponseWriter, r *http.Request) {
	var p struct{ Category, Content, Contact string }
	if decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	id, err := x.app.CreateFeedback(uid(r), p.Category, p.Content, p.Contact)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusCreated, map[string]string{"id": id, "status": "received"})
}
func (x *API) registerDevice(w http.ResponseWriter, r *http.Request) {
	var p struct {
		DeviceID, Platform, Provider, PushToken            string
		NotificationsEnabled, PreviewEnabled, SoundEnabled *bool
		VibrationEnabled                                   *bool
	}
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if p.Provider == "webpush" {
		if p.Platform != "web" {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "Web Push requires the web platform")
			return
		}
		if _, err := push.ParseWebPushSubscription(p.PushToken); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid Web Push subscription")
			return
		}
	}
	d := store.Device{ID: p.DeviceID, Platform: p.Platform, Provider: p.Provider, PushToken: p.PushToken, NotificationsEnabled: true, PreviewEnabled: true, SoundEnabled: true, VibrationEnabled: true}
	if p.NotificationsEnabled != nil {
		d.NotificationsEnabled = *p.NotificationsEnabled
	}
	if p.PreviewEnabled != nil {
		d.PreviewEnabled = *p.PreviewEnabled
	}
	if p.SoundEnabled != nil {
		d.SoundEnabled = *p.SoundEnabled
	}
	if p.VibrationEnabled != nil {
		d.VibrationEnabled = *p.VibrationEnabled
	}
	registered, err := x.app.RegisterDevice(uid(r), d)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 201, registered)
}

func (x *API) upsertClientDevice(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		InstallationID string `json:"installationId"`
		Platform       string `json:"platform"`
		DeviceName     string `json:"deviceName"`
		DeviceModel    string `json:"deviceModel"`
		OSVersion      string `json:"osVersion"`
		AppVersion     string `json:"appVersion"`
	}
	if decode(r, &payload) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid client device")
		return
	}
	item, err := x.app.UpsertClientDevice(r.Context(), uid(r), store.ClientDevice{
		InstallationID: payload.InstallationID, Platform: payload.Platform, DeviceName: payload.DeviceName,
		DeviceModel: payload.DeviceModel, OSVersion: payload.OSVersion, AppVersion: payload.AppVersion,
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": item})
}

func (x *API) webPushConfig(w http.ResponseWriter, _ *http.Request) {
	write(w, http.StatusOK, map[string]any{
		"enabled":   x.cfg.WebPushEnabled(),
		"publicKey": x.cfg.WebPushPublicKey,
	})
}
func (x *API) unregisterDevice(w http.ResponseWriter, r *http.Request) {
	if err := x.app.UnregisterDevice(uid(r), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) mediaPresign(w http.ResponseWriter, r *http.Request) {
	var p struct {
		MIME, FileName string
		Size           int64
	}
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	v, err := x.media.Prepare(r.Context(), uid(r), p.MIME, p.FileName, p.Size)
	if err != nil {
		slog.Error("media presign failed", "error", err)
		if err == media.ErrInvalid {
			writeError(w, 400, "INVALID_MEDIA", err.Error())
		} else if err == media.ErrUnavailable {
			writeError(w, 503, "MEDIA_UNAVAILABLE", err.Error())
		} else {
			handleErr(w, err)
		}
		return
	}
	write(w, 201, v)
}

func (x *API) linkPreview(w http.ResponseWriter, r *http.Request) {
	var request struct {
		URL string `json:"url"`
	}
	if decode(r, &request) != nil || strings.TrimSpace(request.URL) == "" || len(request.URL) > 2048 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid link preview URL")
		return
	}
	preview, err := x.links.Fetch(r.Context(), request.URL)
	if err != nil {
		if errors.Is(err, linkpreview.ErrUnsafeURL) || errors.Is(err, linkpreview.ErrUnsupportedContent) || errors.Is(err, linkpreview.ErrResponseTooLarge) {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "link cannot be previewed")
			return
		}
		writeError(w, http.StatusBadGateway, "PREVIEW_UNAVAILABLE", "link preview unavailable")
		return
	}
	write(w, http.StatusOK, map[string]any{"preview": preview})
}
func (x *API) mediaComplete(w http.ResponseWriter, r *http.Request) {
	var p struct{ Checksum string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	v, err := x.media.Complete(r.Context(), uid(r), r.PathValue("id"), p.Checksum)
	if err != nil {
		if err == media.ErrInvalid {
			writeError(w, 400, "INVALID_MEDIA", err.Error())
		} else if err == media.ErrForbidden {
			writeError(w, 403, "FORBIDDEN", err.Error())
		} else {
			handleErr(w, err)
		}
		return
	}
	write(w, 200, v)
}
func (x *API) mediaDownload(w http.ResponseWriter, r *http.Request) {
	allowed, err := x.app.CanAccessMedia(uid(r), r.PathValue("id"))
	if err != nil || !allowed {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "media is unavailable")
		return
	}
	url, err := x.media.DownloadURL(r.Context(), r.PathValue("id"))
	if err != nil {
		if err == media.ErrForbidden {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "media is unavailable")
		} else {
			handleErr(w, err)
		}
		return
	}
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (x *API) bindWukongMedia(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ChannelID   string `json:"channelId"`
		ChannelType uint8  `json:"channelType"`
	}
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid media binding")
		return
	}
	input.ChannelID = strings.TrimSpace(input.ChannelID)
	userID := uid(r)
	if !x.canAccessWukongChannel(userID, input.ChannelID, input.ChannelType) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "channel access denied")
		return
	}
	mediaID := strings.TrimSpace(r.PathValue("id"))
	item, err := x.app.GetMedia(mediaID)
	if err != nil || item.OwnerID != userID || item.Status != "ready" {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "media is not ready or owned by sender")
		return
	}
	err = x.app.BindMediaChannel(store.MediaChannelBinding{
		MediaID: mediaID, ChannelID: input.ChannelID,
		ChannelType: input.ChannelType, SenderID: userID,
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	url, err := x.media.DownloadURL(r.Context(), mediaID)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"mediaId": mediaID, "url": url})
}

func (x *API) wukongMediaURL(w http.ResponseWriter, r *http.Request) {
	mediaID := strings.TrimSpace(r.PathValue("id"))
	allowed, err := x.app.CanAccessMedia(uid(r), mediaID)
	if err != nil || !allowed {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "media is unavailable")
		return
	}
	url, err := x.media.DownloadURL(r.Context(), mediaID)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"mediaId": mediaID, "url": url})
}

func (x *API) messageWithDownloadURL(ctx context.Context, userID string, message *model.Message) (*model.Message, error) {
	if message == nil {
		return nil, nil
	}
	copy := *message
	copy.Body = make(map[string]any, len(message.Body)+1)
	for key, value := range message.Body {
		copy.Body[key] = value
	}
	if message.Type != "image" && message.Type != "audio" && message.Type != "video" && message.Type != "file" {
		return &copy, nil
	}
	mediaID, _ := copy.Body["mediaId"].(string)
	if mediaID == "" {
		return &copy, nil
	}
	allowed, err := x.app.CanAccessMedia(userID, mediaID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return &copy, nil
	}
	url, err := x.media.DownloadURL(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	copy.Body["downloadUrl"] = url
	return &copy, nil
}

func (x *API) searchUsers(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.SearchUsersByIdentifier(r.URL.Query().Get("q"), r.URL.Query().Get("by"))
	if err != nil {
		handleErr(w, err)
		return
	}
	for _, item := range items {
		x.signAvatarURL(item)
	}
	write(w, 200, map[string]any{"items": items, "capabilities": x.app.SearchCapabilities()})
}
func (x *API) searchCapabilities(w http.ResponseWriter, r *http.Request) {
	write(w, 200, x.app.SearchCapabilities())
}
func (x *API) friendRequest(w http.ResponseWriter, r *http.Request) {
	var p struct{ UserID, Message, Source, SourceID string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	v, err := x.app.RequestFriendWithContext(uid(r), p.UserID, p.Message, p.Source, p.SourceID)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 201, v)
}
func (x *API) friendRequests(w http.ResponseWriter, r *http.Request) {
	userID := uid(r)
	requests, err := x.app.FriendRequestsContext(r.Context(), userID)
	if err != nil {
		handleErr(w, err)
		return
	}
	type friendRequestItem struct {
		*model.FriendRequest
		User map[string]any `json:"user,omitempty"`
	}
	items := make([]friendRequestItem, 0, len(requests))
	for _, request := range requests {
		item := friendRequestItem{FriendRequest: request}
		peerID := request.FromUserID
		if peerID == userID {
			peerID = request.ToUserID
		}
		peer, lookupErr := x.app.UserContext(r.Context(), peerID)
		if lookupErr == nil && peer != nil {
			x.signAvatarURL(peer)
			item.User = map[string]any{
				"id": peer.ID, "name": peer.Name, "handle": peer.Handle,
				"signature": peer.Signature, "avatarMediaId": peer.AvatarMediaID,
				"avatarUrl": peer.AvatarURL,
			}
		}
		items = append(items, item)
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}
func (x *API) friends(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.FriendsContext(r.Context(), uid(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	for _, item := range items {
		x.signAvatarURL(item)
	}
	write(w, 200, map[string]any{"items": items})
}
func (x *API) acceptFriend(w http.ResponseWriter, r *http.Request) {
	if err := x.app.AcceptFriend(uid(r), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]bool{"accepted": true})
}
func (x *API) rejectFriend(w http.ResponseWriter, r *http.Request) {
	if err := x.app.RejectFriend(uid(r), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]bool{"rejected": true})
}
func (x *API) cancelFriendRequest(w http.ResponseWriter, r *http.Request) {
	if err := x.app.CancelFriendRequest(uid(r), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]bool{"cancelled": true})
}
func (x *API) deleteFriend(w http.ResponseWriter, r *http.Request) {
	if err := x.app.DeleteFriend(uid(r), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) friendMetadata(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Remark string
		Tags   []string
	}
	if decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if err := x.app.UpdateFriendMetadata(uid(r), r.PathValue("id"), p.Remark, p.Tags); err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"remark": p.Remark, "tags": p.Tags})
}
func (x *API) block(w http.ResponseWriter, r *http.Request) {
	var p struct{ Blocked bool }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if err := x.app.Block(uid(r), r.PathValue("id"), p.Blocked); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, p)
}
func (x *API) blocks(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.BlockedUsers(uid(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	for _, item := range items {
		x.signAvatarURL(item)
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}
func (x *API) conversations(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.ConversationsContext(r.Context(), uid(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	for _, item := range items {
		if conversation, ok := item["conversation"].(*model.Conversation); ok {
			if mediaID := avatarMediaIDFromPath(conversation.AvatarURL); mediaID != "" {
				conversation.AvatarURL = x.signedAvatarValue(mediaID)
			}
		}
		if members, ok := item["members"].([]*model.ConversationMember); ok {
			for _, member := range members {
				if mediaID := avatarMediaIDFromPath(member.AvatarURL); mediaID != "" {
					member.AvatarURL = x.signedAvatarValue(mediaID)
				}
			}
		}
	}
	write(w, 200, map[string]any{"items": items})
}
func (x *API) direct(w http.ResponseWriter, r *http.Request) {
	var p struct{ UserID string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	v, err := x.app.DirectConversation(uid(r), p.UserID)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 201, v)
}
func (x *API) createGroup(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Name      string
		MemberIDs []string
	}
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	v, err := x.app.CreateGroup(uid(r), p.Name, p.MemberIDs)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 201, v)
}
func (x *API) groupProfile(w http.ResponseWriter, r *http.Request) {
	g, err := x.app.GroupProfile(uid(r), r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	if mediaID := avatarMediaIDFromPath(g.AvatarURL); mediaID != "" {
		g.AvatarURL = x.signedAvatarValue(mediaID)
	}
	write(w, http.StatusOK, g)
}
func (x *API) updateGroupProfile(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Name, AvatarMediaID, JoinPolicy *string
		AllowMemberAddFriend            *bool
		RotateQR                        bool
	}
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	g, err := x.app.UpdateGroupProfile(uid(r), r.PathValue("id"), store.GroupProfileUpdate{Name: p.Name, AvatarMediaID: p.AvatarMediaID, JoinPolicy: p.JoinPolicy, AllowMemberAddFriend: p.AllowMemberAddFriend, RotateQR: p.RotateQR})
	if err != nil {
		handleErr(w, err)
		return
	}
	if mediaID := avatarMediaIDFromPath(g.AvatarURL); mediaID != "" {
		g.AvatarURL = x.signedAvatarValue(mediaID)
	}
	write(w, 200, g)
}
func (x *API) groupAnnouncement(w http.ResponseWriter, r *http.Request) {
	var p struct{ Content string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	g, err := x.app.SetGroupAnnouncement(uid(r), r.PathValue("id"), p.Content)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, g)
}
func (x *API) readGroupAnnouncement(w http.ResponseWriter, r *http.Request) {
	if err := x.app.ReadGroupAnnouncement(uid(r), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) groupInvite(w http.ResponseWriter, r *http.Request) {
	var p struct{ UserID string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	i, dup, err := x.app.InviteGroupMember(uid(r), r.PathValue("id"), p.UserID)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, map[bool]int{true: 200, false: 201}[dup], map[string]any{"invite": i, "duplicate": dup})
}
func (x *API) groupInviteAction(w http.ResponseWriter, r *http.Request) {
	i, dup, err := x.app.TransitionGroupInvite(uid(r), r.PathValue("id"), r.PathValue("action"))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]any{"invite": i, "duplicate": dup})
}
func (x *API) groupInvites(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := x.app.GroupInvites(uid(r), r.URL.Query().Get("status"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	for _, item := range items {
		if inviter, ok := item["inviter"].(*model.User); ok {
			x.signAvatarURL(inviter)
		}
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}
func (x *API) joinGroupQR(w http.ResponseWriter, r *http.Request) {
	var p struct{ Token string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if err := x.app.JoinGroupByQR(uid(r), p.Token); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]bool{"joined": true})
}
func (x *API) transferGroupOwner(w http.ResponseWriter, r *http.Request) {
	var p struct{ UserID string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if err := x.app.TransferGroupOwner(uid(r), r.PathValue("id"), p.UserID); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]bool{"transferred": true})
}
func (x *API) leaveGroup(w http.ResponseWriter, r *http.Request) {
	if err := x.app.RemoveGroupMember(uid(r), r.PathValue("id"), uid(r)); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) muteGroupAll(w http.ResponseWriter, r *http.Request) {
	var p struct{ Until *time.Time }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	until := p.Until
	if until == nil {
		now := time.Now()
		until = &now
	}
	g, err := x.app.UpdateGroupProfile(uid(r), r.PathValue("id"), store.GroupProfileUpdate{AllMutedUntil: until})
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, g)
}
func (x *API) groupNickname(w http.ResponseWriter, r *http.Request) {
	var p struct{ Nickname string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if err := x.app.SetGroupNickname(uid(r), r.PathValue("id"), p.Nickname); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, p)
}
func (x *API) userDisbandGroup(w http.ResponseWriter, r *http.Request) {
	var p struct{ Reason string }
	_ = decode(r, &p)
	if err := x.app.DisbandGroup(uid(r), r.PathValue("id"), p.Reason); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]bool{"disbanded": true})
}
func (x *API) addMembers(w http.ResponseWriter, r *http.Request) {
	var p struct{ UserIDs []string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if err := x.app.AddGroupMembers(uid(r), r.PathValue("id"), p.UserIDs); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]bool{"updated": true})
}
func (x *API) groupMembers(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, err := x.app.GroupMembersPage(r.Context(), uid(r), r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	for _, item := range items {
		if mediaID := avatarMediaIDFromPath(item.AvatarURL); mediaID != "" {
			item.AvatarURL = x.signedAvatarValue(mediaID)
		}
	}
	write(w, 200, map[string]any{"items": items, "nextCursor": next})
}
func (x *API) removeMember(w http.ResponseWriter, r *http.Request) {
	if err := x.app.RemoveGroupMember(uid(r), r.PathValue("id"), r.PathValue("userId")); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) groupRole(w http.ResponseWriter, r *http.Request) {
	var p struct{ Role string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if err := x.app.SetGroupRole(uid(r), r.PathValue("id"), r.PathValue("userId"), p.Role); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, p)
}
func (x *API) mute(w http.ResponseWriter, r *http.Request) {
	var p struct{ Until *time.Time }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if err := x.app.MuteMember(uid(r), r.PathValue("id"), r.PathValue("userId"), p.Until); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, p)
}
func (x *API) sendMessage(w http.ResponseWriter, r *http.Request) {
	var p struct {
		ClientMsgID, Type, ReplyToID string
		Body                         map[string]any
		ExpiresInSeconds             int64 `json:"expiresInSeconds"`
	}
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	m, duplicate, err := x.app.SendMessageWithExpiryContext(r.Context(), uid(r), r.PathValue("id"), p.ClientMsgID, p.Type, p.Body, p.ReplyToID, p.ExpiresInSeconds)
	if err != nil {
		handleErr(w, err)
		return
	}
	m, err = x.messageWithDownloadURL(r.Context(), uid(r), m)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, map[bool]int{true: 200, false: 201}[duplicate], map[string]any{"message": m, "duplicate": duplicate})
}
func (x *API) createScheduledMessage(w http.ResponseWriter, r *http.Request) {
	var p struct {
		ConversationID, ClientMsgID, Type, ReplyToID string
		Body                                         map[string]any
		ExpiresInSeconds                             int64     `json:"expiresInSeconds"`
		ScheduledAt                                  time.Time `json:"scheduledAt"`
	}
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	item, duplicate, err := x.app.CreateScheduledMessage(uid(r), p.ConversationID, p.ClientMsgID, p.Type, p.Body, p.ReplyToID, p.ExpiresInSeconds, p.ScheduledAt)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, map[bool]int{true: 200, false: 201}[duplicate], map[string]any{"scheduledMessage": item, "duplicate": duplicate})
}
func (x *API) scheduledMessages(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := x.app.ListScheduledMessages(uid(r), r.URL.Query().Get("status"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]any{"items": items})
}
func (x *API) updateScheduledMessage(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Type             *string         `json:"type"`
		Body             *map[string]any `json:"body"`
		ReplyToID        *string         `json:"replyToId"`
		ExpiresInSeconds *int64          `json:"expiresInSeconds"`
		ScheduledAt      *time.Time      `json:"scheduledAt"`
	}
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	update := store.ScheduledMessageUpdate{Type: p.Type, ReplyToID: p.ReplyToID, ExpiresInSeconds: p.ExpiresInSeconds, ScheduledAt: p.ScheduledAt}
	if p.Body != nil {
		update.Body, update.BodySet = *p.Body, true
	}
	item, err := x.app.UpdateScheduledMessage(uid(r), r.PathValue("id"), update)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]any{"scheduledMessage": item})
}
func (x *API) cancelScheduledMessage(w http.ResponseWriter, r *http.Request) {
	item, err := x.app.CancelScheduledMessage(uid(r), r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]any{"scheduledMessage": item})
}
func (x *API) forwardMessages(w http.ResponseWriter, r *http.Request) {
	var p struct {
		TargetConversationID string   `json:"targetConversationId"`
		SourceMessageIDs     []string `json:"sourceMessageIds"`
		Mode                 string   `json:"mode"`
		ClientBatchID        string   `json:"clientBatchId"`
	}
	if decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	messages, duplicate, err := x.app.ForwardMessages(uid(r), p.TargetConversationID, p.SourceMessageIDs, p.Mode, p.ClientBatchID)
	if err != nil {
		handleErr(w, err)
		return
	}
	for index, message := range messages {
		messages[index], err = x.messageWithDownloadURL(r.Context(), uid(r), message)
		if err != nil {
			handleErr(w, err)
			return
		}
	}
	write(w, http.StatusOK, map[string]any{"messages": messages, "duplicate": duplicate})
}
func (x *API) history(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("beforeSeq"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := x.app.History(uid(r), r.PathValue("id"), before, limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	for index, message := range items {
		items[index], err = x.messageWithDownloadURL(r.Context(), uid(r), message)
		if err != nil {
			handleErr(w, err)
			return
		}
	}
	write(w, 200, map[string]any{"items": items})
}
func (x *API) searchMessages(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("beforeSeq"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := x.app.SearchConversationMessages(uid(r), r.URL.Query().Get("conversationId"), r.URL.Query().Get("q"), before, limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}
func (x *API) recall(w http.ResponseWriter, r *http.Request) {
	if err := x.app.Recall(uid(r), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]bool{"recalled": true})
}
func (x *API) editMessage(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		EditID     string   `json:"editId"`
		Text       string   `json:"text"`
		Mentions   []string `json:"mentions"`
		MentionAll bool     `json:"mentionAll"`
	}
	if decode(r, &payload) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	body := map[string]any{"text": payload.Text}
	if len(payload.Mentions) > 0 {
		body["mentions"] = payload.Mentions
	}
	if payload.MentionAll {
		body["mentionAll"] = true
	}
	message, duplicate, err := x.app.EditMessage(uid(r), r.PathValue("id"), payload.EditID, body)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"message": message, "duplicate": duplicate})
}
func (x *API) messageEdits(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.MessageEdits(uid(r), r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}
func (x *API) messageReaction(w http.ResponseWriter, r *http.Request) {
	summary, duplicate, err := x.app.SetMessageReaction(uid(r), r.PathValue("id"), r.PathValue("emoji"), r.Method == http.MethodPut)
	if err != nil {
		handleErr(w, err)
		return
	}
	message, err := x.app.CollaborationMessage(uid(r), r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"message": message, "reaction": summary, "duplicate": duplicate})
}
func (x *API) groupMessagePin(w http.ResponseWriter, r *http.Request) {
	item, duplicate, err := x.app.SetGroupMessagePin(uid(r), r.URL.Query().Get("conversationId"), r.PathValue("messageId"), r.Method == http.MethodPut)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": item, "duplicate": duplicate})
}
func (x *API) groupMessagePins(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := x.app.GroupMessagePins(uid(r), r.URL.Query().Get("conversationId"), before, limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	for _, item := range items {
		item.Message, err = x.messageWithDownloadURL(r.Context(), uid(r), item.Message)
		if err != nil {
			handleErr(w, err)
			return
		}
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}
func (x *API) read(w http.ResponseWriter, r *http.Request) {
	var p struct{ Seq int64 }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if err := x.app.Read(uid(r), r.PathValue("id"), p.Seq); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, p)
}
func (x *API) delivered(w http.ResponseWriter, r *http.Request) {
	var p struct{ Seq int64 }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	actual, err := x.app.Delivered(uid(r), r.PathValue("id"), p.Seq)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]any{"seq": actual})
}
func (x *API) conversationPreferences(w http.ResponseWriter, r *http.Request) {
	var p store.ConversationPreferences
	if decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if err := x.app.UpdateConversationPreferences(uid(r), r.PathValue("id"), p); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) hideConversation(w http.ResponseWriter, r *http.Request) {
	if err := x.app.HideConversation(uid(r), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) typing(w http.ResponseWriter, r *http.Request) {
	var p struct{ Typing bool }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if err := x.app.SetTypingContext(r.Context(), uid(r), r.PathValue("id"), p.Typing); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(204)
}
func (x *API) report(w http.ResponseWriter, r *http.Request) {
	var p struct{ TargetType, TargetID, Reason, Details string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	v, err := x.app.Report(uid(r), p.TargetType, p.TargetID, p.Reason, p.Details)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 201, v)
}
func (x *API) announcements(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.Announcements(uid(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}
func (x *API) readAnnouncement(w http.ResponseWriter, r *http.Request) {
	if err := x.app.MarkAnnouncementRead(uid(r), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) adminStats(w http.ResponseWriter, r *http.Request) {
	stats, err := x.app.AdminStatsContext(r.Context())
	if err != nil {
		handleErr(w, err)
		return
	}
	stats["wukongConnections"] = int64(0)
	stats["wukongStatus"] = "disabled"
	if x.cfg.WukongEnabled && x.wukongClient != nil && x.wukongSetupErr == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		overview, err := x.wukongClient.ManagerVarz(ctx)
		if err == nil {
			stats["wukongConnections"] = wukongInt64(overview["connections"])
			stats["wukongStatus"] = "ok"
		} else {
			stats["wukongStatus"] = "unavailable"
		}
	}
	write(w, http.StatusOK, stats)
}
func (x *API) adminUsers(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, total, next, err := x.app.AdminUsersPage(query.Get("q"), query.Get("status"), query.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	for _, item := range items {
		x.signAvatarURL(item)
	}
	write(w, 200, map[string]any{"items": items, "total": total, "nextCursor": next})
}
func (x *API) adminUserOverview(w http.ResponseWriter, r *http.Request) {
	item, err := x.app.AdminUserOverview(r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	if user, ok := item["user"].(*model.User); ok {
		x.signAvatarURL(user)
	}
	write(w, http.StatusOK, item)
}
func (x *API) adminUserFriends(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if _, err := x.app.UserContext(r.Context(), userID); err != nil {
		handleErr(w, err)
		return
	}
	items, err := x.app.AdminUserFriends(r.Context(), userID)
	if err != nil {
		handleErr(w, err)
		return
	}
	for _, item := range items {
		x.signAvatarURL(item.User)
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}
func (x *API) adminUserBlocks(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if _, err := x.app.UserContext(r.Context(), userID); err != nil {
		handleErr(w, err)
		return
	}
	items, err := x.app.AdminUserBlocks(r.Context(), userID)
	if err != nil {
		handleErr(w, err)
		return
	}
	for _, item := range items {
		x.signAvatarURL(item.User)
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}
func (x *API) adminUserDevices(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if _, err := x.app.UserContext(r.Context(), userID); err != nil {
		handleErr(w, err)
		return
	}
	pushRegistrations, err := x.app.UserDevices(userID)
	if err != nil {
		handleErr(w, err)
		return
	}
	items, err := x.app.ClientDevices(r.Context(), userID)
	if err != nil && err != app.ErrUnavailable {
		handleErr(w, err)
		return
	}
	if items == nil {
		items = []store.ClientDevice{}
	}
	write(w, http.StatusOK, map[string]any{"items": items, "pushRegistrations": pushRegistrations})
}
func (x *API) adminUserSystemMessage(w http.ResponseWriter, r *http.Request) {
	var p struct {
		SenderUID string `json:"senderUid"`
		Content   string `json:"content"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(r, &p) != nil || !confirmedReason(p.Confirmed, p.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	p.Content = strings.TrimSpace(p.Content)
	if p.Content == "" || len([]rune(p.Content)) > 2000 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "system message content must contain 1 to 2000 characters")
		return
	}
	targetID := strings.TrimSpace(r.PathValue("id"))
	if _, err := x.app.UserContext(r.Context(), targetID); err != nil {
		handleErr(w, err)
		return
	}
	if !x.cfg.WukongEnabled || x.wukongClient == nil || x.wukongSetupErr != nil {
		writeError(w, http.StatusServiceUnavailable, "WUKONG_UNAVAILABLE", "WuKongIM is unavailable")
		return
	}
	systemUsers, err := x.app.WukongSystemUsers(r.Context())
	if err != nil {
		handleErr(w, err)
		return
	}
	senderID := strings.TrimSpace(p.SenderUID)
	validSender := false
	for _, candidate := range systemUsers {
		if candidate.UserID == senderID && candidate.Enabled && candidate.SyncStatus == "synced" {
			validSender = true
			break
		}
	}
	if senderID == "" || senderID == targetID || !validSender {
		writeError(w, http.StatusBadRequest, "INVALID_SYSTEM_SENDER", "senderUid must be a synced enabled WuKongIM system user distinct from the target")
		return
	}
	conversation, err := x.app.DirectConversation(senderID, targetID)
	if err != nil {
		handleErr(w, err)
		return
	}
	random := make([]byte, 12)
	if _, err = rand.Read(random); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to create message identifier")
		return
	}
	clientMsgNo := "admin-notice-" + hex.EncodeToString(random)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	result, err := x.wukongClient.SendStoredMessage(ctx, wukong.StoredMessageRequest{
		ClientMsgNo: clientMsgNo,
		FromUID:     senderID,
		ChannelID:   targetID,
		ChannelType: wukong.ChannelPerson,
		Payload: map[string]any{
			"type": wukong.ContentTypeSystemEvent, "schemaVersion": 1,
			"event": "admin.notice", "content": p.Content, "digest": p.Content,
		},
	})
	if err != nil {
		slog.Warn("admin system message failed", "event", "admin.user.system_message.failed", "target_uid", targetID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "WUKONG_UNAVAILABLE", "system message could not be delivered")
		return
	}
	x.app.RecordAdminAudit(uid(r), "user.system_message.sent", "user", targetID, "success", x.clientIP(r), map[string]any{
		"reason": strings.TrimSpace(p.Reason), "senderUid": senderID, "messageId": result.MessageID, "conversationId": conversation.ID,
	})
	write(w, http.StatusCreated, map[string]any{
		"targetUid": targetID, "senderUid": senderID, "conversationId": conversation.ID,
		"messageId": result.MessageID, "clientMsgNo": result.ClientMsgNo,
	})
}
func (x *API) adminBan(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Reason        string
		Confirmed     bool
		DurationHours int `json:"durationHours"`
	}
	if decode(r, &p) != nil || !confirmedReason(p.Confirmed, p.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	if err := x.app.AdminBan(uid(r), r.PathValue("id"), true, p.DurationHours, p.Reason); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]bool{"banned": true})
}
func (x *API) adminUnban(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Reason    string
		Confirmed bool
	}
	if decode(r, &p) != nil || !confirmedReason(p.Confirmed, p.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	if err := x.app.AdminBan(uid(r), r.PathValue("id"), false, 0, p.Reason); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]bool{"banned": false})
}
func (x *API) adminReports(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, total, next, err := x.app.AdminReportsPage(query.Get("q"), query.Get("status"), query.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]any{"items": items, "total": total, "nextCursor": next})
}
func (x *API) resolveReport(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Action, Reason string
		Confirmed      bool
	}
	if decode(r, &p) != nil || !confirmedReason(p.Confirmed, p.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	status, err := x.app.ResolveReport(uid(r), r.PathValue("id"), p.Action, p.Reason)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]string{"status": status, "action": p.Action})
}
func (x *API) auditLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, total, next, err := x.app.AdminAuditsPage(query.Get("q"), query.Get("status"), query.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]any{"items": items, "total": total, "nextCursor": next})
}
func (x *API) adminMessages(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, total, next, err := x.app.AdminMessagesPage(query.Get("q"), query.Get("type"), query.Get("cursor"), limit)
	if err != nil {
		x.recordAdminMessageView(r, "failed", 0, 0, 0, err)
		handleErr(w, err)
		return
	}
	loaded, missing, err := x.loadAdminMessageBodies(r.Context(), items)
	if err != nil {
		x.recordAdminMessageView(r, "failed", len(items), loaded, missing, err)
		handleErr(w, err)
		return
	}
	x.recordAdminMessageView(r, "success", len(items), loaded, missing, nil)
	write(w, http.StatusOK, map[string]any{"items": items, "total": total, "nextCursor": next})
}
func (x *API) adminMediaCleanupStatus(w http.ResponseWriter, r *http.Request) {
	status, err := x.app.MediaCleanupStatus()
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"task": "media-cleanup", "status": status})
}
func (x *API) adminTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := x.app.AdminTaskStatus()
	if err != nil {
		handleErr(w, err)
		return
	}
	mediaStatus, err := x.app.MediaCleanupStatus()
	if err != nil {
		handleErr(w, err)
		return
	}
	tasks["mediaCleanup"] = mediaStatus
	write(w, http.StatusOK, map[string]any{"tasks": tasks})
}
func (x *API) adminFriendships(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, total, next, err := x.app.AdminFriendshipsPage(q.Get("q"), q.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "total": total, "nextCursor": next})
}
func (x *API) adminFeedback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, total, next, err := x.app.AdminFeedbackPage(q.Get("q"), q.Get("category"), q.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "total": total, "nextCursor": next})
}
func (x *API) adminPushStatus(w http.ResponseWriter, r *http.Request) {
	status, err := x.app.AdminPushStatus()
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, status)
}
func (x *API) adminAccess(w http.ResponseWriter, r *http.Request) {
	account, err := x.app.AdminAccount(r.Context(), uid(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	roles, err := x.app.AdminRoles(r.Context())
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"current": account, "roles": roles, "note": "管理员账号、角色和权限由 PostgreSQL 实时验证。"})
}
func (x *API) adminMedia(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, total, next, err := x.app.AdminMediaPage(query.Get("q"), query.Get("status"), query.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "total": total, "nextCursor": next})
}
func (x *API) adminOnline(w http.ResponseWriter, r *http.Request) {
	if x.wukongSetupErr != nil || x.wukongClient == nil {
		writeError(w, http.StatusServiceUnavailable, "WUKONG_UNAVAILABLE", "WuKongIM connection state is unavailable")
		return
	}
	connections, err := x.wukongClient.Connections(r.Context(), "", 0, 10000)
	if err != nil {
		slog.Warn("WuKongIM connection query failed", "error", err)
		writeError(w, http.StatusBadGateway, "WUKONG_UNAVAILABLE", "WuKongIM connection state is unavailable")
		return
	}
	counts := make(map[string]int, len(connections.Connections))
	for _, connection := range connections.Connections {
		if connection.UID != "" {
			counts[connection.UID]++
		}
	}
	type onlineRecord struct {
		UserID      string `json:"userId"`
		Connections int    `json:"connections"`
	}
	items := make([]onlineRecord, 0, len(counts))
	for userID, count := range counts {
		items = append(items, onlineRecord{UserID: userID, Connections: count})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UserID < items[j].UserID })
	write(w, http.StatusOK, map[string]any{"items": items, "totalUsers": len(items), "totalConnections": connections.Total, "source": "wukongim"})
}
func announcementInput(r *http.Request) (store.AnnouncementInput, bool, string, error) {
	var p struct {
		Title         string     `json:"title"`
		Content       string     `json:"content"`
		Status        string     `json:"status"`
		Pinned        bool       `json:"pinned"`
		TargetType    string     `json:"targetType"`
		TargetUserIDs []string   `json:"targetUserIds"`
		ScheduledAt   *time.Time `json:"scheduledAt"`
		PushOnPublish bool       `json:"pushOnPublish"`
		Confirmed     bool       `json:"confirmed"`
		Reason        string     `json:"reason"`
	}
	err := decode(r, &p)
	return store.AnnouncementInput{Title: p.Title, Content: p.Content, Status: p.Status, Pinned: p.Pinned, TargetType: p.TargetType, TargetUserIDs: p.TargetUserIDs, ScheduledAt: p.ScheduledAt, PushOnPublish: p.PushOnPublish}, p.Confirmed, p.Reason, err
}
func (x *API) adminAnnouncements(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, total, next, err := x.app.AdminAnnouncements(q.Get("q"), q.Get("status"), q.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "total": total, "nextCursor": next})
}
func (x *API) createAnnouncement(w http.ResponseWriter, r *http.Request) {
	input, confirmed, reason, err := announcementInput(r)
	if err != nil || !confirmedReason(confirmed, reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	item, err := x.app.CreateAnnouncement(uid(r), input)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusCreated, item)
}
func (x *API) updateAnnouncement(w http.ResponseWriter, r *http.Request) {
	input, confirmed, reason, err := announcementInput(r)
	if err != nil || !confirmedReason(confirmed, reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	item, err := x.app.UpdateAnnouncement(uid(r), r.PathValue("id"), input)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, item)
}
func (x *API) publishAnnouncement(w http.ResponseWriter, r *http.Request) {
	var p struct {
		EnqueuePush bool   `json:"enqueuePush"`
		Confirmed   bool   `json:"confirmed"`
		Reason      string `json:"reason"`
	}
	if decode(r, &p) != nil || !confirmedReason(p.Confirmed, p.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	item, err := x.app.PublishAnnouncement(uid(r), r.PathValue("id"), p.EnqueuePush)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, item)
}
func (x *API) withdrawAnnouncement(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Confirmed bool   `json:"confirmed"`
		Reason    string `json:"reason"`
	}
	if decode(r, &p) != nil || !confirmedReason(p.Confirmed, p.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	item, err := x.app.WithdrawAnnouncement(uid(r), r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, item)
}
func (x *API) deleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(r, &p) != nil || !confirmedReason(p.Confirmed, p.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	if err := x.app.DeleteAnnouncement(uid(r), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "announcement.delete_reason", "announcement", r.PathValue("id"), "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(p.Reason)})
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) adminCalls(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, total, next, err := x.app.AdminCalls(query.Get("q"), query.Get("status"), query.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "total": total, "nextCursor": next})
}
func (x *API) adminGroups(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, total, next, err := x.app.AdminGroupsPage(q.Get("q"), q.Get("status"), q.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]any{"items": items, "total": total, "nextCursor": next})
}
func (x *API) adminGroupOverview(w http.ResponseWriter, r *http.Request) {
	item, err := x.app.AdminGroupOverview(r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, item)
}
func (x *API) adminGroupMembers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, total, next, err := x.app.AdminGroupMembers(r.PathValue("id"), q.Get("q"), q.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "total": total, "nextCursor": next})
}
func (x *API) adminGroupMemberAction(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Action     string     `json:"action"`
		Role       string     `json:"role"`
		Reason     string     `json:"reason"`
		MutedUntil *time.Time `json:"mutedUntil"`
		Confirmed  bool       `json:"confirmed"`
	}
	if decode(r, &p) != nil || !confirmedReason(p.Confirmed, p.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	if r.Method == http.MethodDelete {
		p.Action = "remove"
	}
	if err := x.app.AdminModerateGroupMember(uid(r), r.PathValue("id"), r.PathValue("userId"), p.Action, p.Role, p.Reason, p.MutedUntil); err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "group.member."+p.Action, "group_member", r.PathValue("id")+":"+r.PathValue("userId"), "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(p.Reason), "role": p.Role, "mutedUntil": p.MutedUntil})
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) disbandGroup(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Reason    string
		Confirmed bool
	}
	if decode(r, &p) != nil || !confirmedReason(p.Confirmed, p.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	if err := x.app.DisbandGroup(uid(r), r.PathValue("id"), p.Reason); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]bool{"disbanded": true})
}
func (x *API) sensitiveWords(w http.ResponseWriter, r *http.Request) {
	entries := x.app.SensitiveWords()
	items := make([]map[string]string, 0, len(entries))
	for wid, value := range entries {
		parts := strings.SplitN(value, "|", 2)
		item := map[string]string{"id": wid, "word": parts[0]}
		if len(parts) > 1 {
			item["category"] = parts[1]
		}
		items = append(items, item)
	}
	write(w, 200, map[string]any{"items": items})
}
func (x *API) addSensitiveWord(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Word, Category string
		Reason         string
		Confirmed      bool
	}
	if decode(r, &p) != nil || !confirmedReason(p.Confirmed, p.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	wid, err := x.app.AddSensitiveWord(uid(r), p.Word, p.Category)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 201, map[string]string{"id": wid, "word": p.Word, "category": p.Category})
}
func (x *API) deleteSensitiveWord(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(r, &p) != nil || !confirmedReason(p.Confirmed, p.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	if err := x.app.DeleteSensitiveWord(uid(r), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "sensitive_word.delete_reason", "sensitive_word", r.PathValue("id"), "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(p.Reason)})
	w.WriteHeader(http.StatusNoContent)
}
func (x *API) settingsPayload() map[string]any {
	values := x.app.Settings()
	getuiConfigured := x.cfg.GetuiAppID != "" && x.cfg.GetuiAppKey != "" && x.cfg.GetuiMasterSecret != ""
	apnsVoIPConfigured := x.cfg.APNSVoIPKeyID != "" && x.cfg.APNSVoIPTeamID != "" && x.cfg.APNSVoIPBundleID != "" && x.cfg.APNSVoIPKeyFile != ""
	pushConfigured := (x.cfg.PushProvider == "getui" && getuiConfigured) ||
		(x.cfg.PushProvider == "apns_voip" && apnsVoIPConfigured) ||
		(x.cfg.PushProvider == "getui_apns_voip" && getuiConfigured && apnsVoIPConfigured) ||
		(x.cfg.PushProvider == "webhook" && x.cfg.PushWebhookURL != "" && x.cfg.PushWebhookToken != "")
	values["configurationStatus"] = map[string]any{
		"database":      x.cfg.DatabaseURL != "",
		"redis":         x.cfg.RedisURL != "",
		"objectStorage": x.cfg.S3Endpoint != "" && x.cfg.S3AccessKey != "" && x.cfg.S3SecretKey != "",
		"otpProvider":   x.cfg.DevMode || (x.cfg.OTPWebhookURL != "" && x.cfg.OTPWebhookToken != ""),
		"pushProvider":  pushConfigured,
		"apnsVoIP":      apnsVoIPConfigured,
		"webPush":       x.cfg.WebPushEnabled(),
		"liveKit":       x.cfg.LiveKitEnabled && x.livekit != nil && x.livekitSetupErr == nil,
	}
	values["infrastructure"] = map[string]any{
		"pushProvider": x.cfg.PushProvider, "mediaMaxSizeMB": x.cfg.MediaMaxBytes / (1 << 20),
		"apnsVoipSandbox":          x.cfg.APNSVoIPSandbox,
		"webPushEnabled":           x.cfg.WebPushEnabled(),
		"callInviteTimeoutSeconds": int64(x.cfg.CallInviteTTL / time.Second), "accessTokenMinutes": int64(x.cfg.AccessTTL / time.Minute),
		"refreshTokenHours": int64(x.cfg.RefreshTTL / time.Hour),
	}
	values["restartRequiredKeys"] = []string{"pushProvider", "mediaMaxSizeMB", "callInviteTimeoutSeconds", "accessTokenMinutes", "refreshTokenHours"}
	return values
}
func (x *API) settings(w http.ResponseWriter, r *http.Request) { write(w, 200, x.settingsPayload()) }
func (x *API) updateSettings(w http.ResponseWriter, r *http.Request) {
	var p map[string]any
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	confirmed, _ := p["confirmed"].(bool)
	reason, _ := p["reason"].(string)
	if !confirmedReason(confirmed, reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	delete(p, "confirmed")
	delete(p, "reason")
	if err := x.app.UpdateSettings(uid(r), p); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, x.settingsPayload())
}
