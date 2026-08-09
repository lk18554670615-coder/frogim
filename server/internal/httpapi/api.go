package httpapi

import (
	"bufio"
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
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/auth"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/linkpreview"
	"github.com/linli/im/server/internal/media"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/netutil"
	"github.com/linli/im/server/internal/realtime"
	"github.com/linli/im/server/internal/store"
)

type API struct {
	cfg     config.Config
	app     *app.App
	auth    auth.Manager
	hub     *realtime.Hub
	media   mediaService
	cleaner mediaCleanupService
	mux     *http.ServeMux
	started time.Time
	limits  *limiter
	otp     otpProvider
	links   *linkpreview.Service
}
type mediaService interface {
	Prepare(context.Context, string, string, string, int64) (media.Prepared, error)
	Complete(context.Context, string, string, string) (store.Media, error)
	DownloadURL(context.Context, string) (string, error)
}
type mediaCleanupService interface {
	CleanupOnce(context.Context) (int, error)
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
func (w *responseCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	w.status = http.StatusSwitchingProtocols
	return hijacker.Hijack()
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

const userKey ctxKey = "user"
const roleKey ctxKey = "role"

func New(cfg config.Config, a *app.App) *API {
	if cfg.CallInviteTTL == 0 {
		cfg.CallInviteTTL = 30 * time.Second
	}
	x := &API{cfg: cfg, app: a, auth: auth.Manager{Secret: []byte(cfg.JWTSecret), AccessTTL: cfg.AccessTTL, RefreshTTL: cfg.RefreshTTL}, started: time.Now(), limits: newLimiter(), links: linkpreview.New(linkpreview.Config{})}
	if cfg.DevMode {
		x.otp = devOTP(cfg.DevOTPCode)
	} else if cfg.OTPWebhookURL != "" {
		provider := newWebhookOTP(cfg.OTPWebhookURL, cfg.OTPWebhookToken)
		x.otp = provider
	}
	x.media, _ = media.New(cfg.S3Endpoint, cfg.S3PublicEndpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3Region, cfg.S3Secure, cfg.S3PublicSecure, cfg.MediaMaxBytes, a)
	if cleaner, ok := x.media.(mediaCleanupService); ok {
		x.cleaner = cleaner
	}
	a.SetCallInviteTTL(cfg.CallInviteTTL)
	x.hub = realtime.New(a, cfg.AllowedOrigins, cfg.WSMaxPerUser, cfg.WSMaxPerIP, cfg.WSMaxConnections, cfg.TrustProxy)
	x.mux = http.NewServeMux()
	x.routes()
	return x
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
	x.mux.HandleFunc("GET /v1/avatars/{id}", x.avatarDownload)
	x.mux.HandleFunc("POST /v1/auth/code", x.requestCode)
	x.mux.HandleFunc("POST /v1/auth/login", x.login)
	x.mux.HandleFunc("POST /v1/auth/register", x.register)
	x.mux.HandleFunc("POST /v1/auth/password-login", x.passwordLogin)
	x.mux.HandleFunc("POST /v1/auth/password/reset-code", x.passwordResetCode)
	x.mux.HandleFunc("POST /v1/auth/password/reset", x.passwordReset)
	x.mux.HandleFunc("POST /v1/auth/refresh", x.refresh)
	x.mux.Handle("POST /v1/auth/logout", x.requireAuth(http.HandlerFunc(x.logout)))
	x.mux.HandleFunc("POST /v1/admin/auth/login", x.adminLogin)
	x.mux.Handle("GET /v1/me", x.requireAuth(http.HandlerFunc(x.me)))
	x.mux.Handle("GET /v1/users/me", x.requireAuth(http.HandlerFunc(x.me)))
	x.mux.Handle("PATCH /v1/users/me", x.requireAuth(http.HandlerFunc(x.updateMe)))
	x.mux.Handle("POST /v1/users/me/deletion/code", x.requireAuth(http.HandlerFunc(x.requestAccountDeletionCode)))
	x.mux.Handle("DELETE /v1/users/me", x.requireUserToken(http.HandlerFunc(x.deleteAccount)))
	x.mux.Handle("POST /v1/users/me/phone/code", x.requireAuth(http.HandlerFunc(x.requestPhoneChangeCode)))
	x.mux.Handle("PATCH /v1/users/me/phone", x.requireAuth(http.HandlerFunc(x.updatePhone)))
	x.mux.Handle("GET /v1/users/me/devices", x.requireAuth(http.HandlerFunc(x.userDevices)))
	x.mux.Handle("DELETE /v1/users/me/devices/{id}", x.requireAuth(http.HandlerFunc(x.unregisterDevice)))
	x.mux.Handle("GET /v1/users/me/favorites", x.requireAuth(http.HandlerFunc(x.favorites)))
	x.mux.Handle("PUT /v1/users/me/favorites/{messageId}", x.requireAuth(http.HandlerFunc(x.setFavorite)))
	x.mux.Handle("DELETE /v1/users/me/favorites/{messageId}", x.requireAuth(http.HandlerFunc(x.setFavorite)))
	x.mux.Handle("POST /v1/feedback", x.requireAuth(http.HandlerFunc(x.feedback)))
	x.mux.Handle("POST /v1/devices", x.requireAuth(http.HandlerFunc(x.registerDevice)))
	x.mux.Handle("DELETE /v1/devices/{id}", x.requireAuth(http.HandlerFunc(x.unregisterDevice)))
	x.mux.Handle("POST /v1/media/presign", x.requireAuth(http.HandlerFunc(x.mediaPresign)))
	x.mux.Handle("POST /v1/link-preview", x.requireAuth(http.HandlerFunc(x.linkPreview)))
	x.mux.Handle("POST /v1/media/{id}/complete", x.requireAuth(http.HandlerFunc(x.mediaComplete)))
	x.mux.Handle("GET /v1/media/{id}", x.requireAuth(http.HandlerFunc(x.mediaDownload)))
	x.mux.Handle("GET /v1/users/search", x.requireAuth(http.HandlerFunc(x.searchUsers)))
	x.mux.Handle("GET /v1/users/search/capabilities", x.requireAuth(http.HandlerFunc(x.searchCapabilities)))
	x.mux.Handle("POST /v1/friend-requests", x.requireAuth(http.HandlerFunc(x.friendRequest)))
	x.mux.Handle("GET /v1/friend-requests", x.requireAuth(http.HandlerFunc(x.friendRequests)))
	x.mux.Handle("GET /v1/friends", x.requireAuth(http.HandlerFunc(x.friends)))
	x.mux.Handle("POST /v1/friend-requests/{id}/accept", x.requireAuth(http.HandlerFunc(x.acceptFriend)))
	x.mux.Handle("POST /v1/friend-requests/{id}/reject", x.requireAuth(http.HandlerFunc(x.rejectFriend)))
	x.mux.Handle("POST /v1/friend-requests/{id}/cancel", x.requireAuth(http.HandlerFunc(x.cancelFriendRequest)))
	x.mux.Handle("DELETE /v1/friends/{id}", x.requireAuth(http.HandlerFunc(x.deleteFriend)))
	x.mux.Handle("PATCH /v1/friends/{id}", x.requireAuth(http.HandlerFunc(x.friendMetadata)))
	x.mux.Handle("PUT /v1/users/{id}/block", x.requireAuth(http.HandlerFunc(x.block)))
	x.mux.Handle("GET /v1/users/me/blocks", x.requireAuth(http.HandlerFunc(x.blocks)))
	x.mux.Handle("GET /v1/conversations", x.requireAuth(http.HandlerFunc(x.conversations)))
	x.mux.Handle("PATCH /v1/conversations/{id}/preferences", x.requireAuth(http.HandlerFunc(x.conversationPreferences)))
	x.mux.Handle("DELETE /v1/conversations/{id}", x.requireAuth(http.HandlerFunc(x.hideConversation)))
	x.mux.Handle("POST /v1/conversations/direct", x.requireAuth(http.HandlerFunc(x.direct)))
	x.mux.Handle("POST /v1/groups", x.requireAuth(http.HandlerFunc(x.createGroup)))
	x.mux.Handle("GET /v1/groups/{id}", x.requireAuth(http.HandlerFunc(x.groupProfile)))
	x.mux.Handle("PATCH /v1/groups/{id}", x.requireAuth(http.HandlerFunc(x.updateGroupProfile)))
	x.mux.Handle("PUT /v1/groups/{id}/announcement", x.requireAuth(http.HandlerFunc(x.groupAnnouncement)))
	x.mux.Handle("POST /v1/groups/{id}/announcement/read", x.requireAuth(http.HandlerFunc(x.readGroupAnnouncement)))
	x.mux.Handle("POST /v1/groups/{id}/invites", x.requireAuth(http.HandlerFunc(x.groupInvite)))
	x.mux.Handle("GET /v1/group-invites", x.requireAuth(http.HandlerFunc(x.groupInvites)))
	x.mux.Handle("POST /v1/group-invites/{id}/{action}", x.requireAuth(http.HandlerFunc(x.groupInviteAction)))
	x.mux.Handle("POST /v1/groups/join/qr", x.requireAuth(http.HandlerFunc(x.joinGroupQR)))
	x.mux.Handle("POST /v1/groups/{id}/owner/transfer", x.requireAuth(http.HandlerFunc(x.transferGroupOwner)))
	x.mux.Handle("POST /v1/groups/{id}/leave", x.requireAuth(http.HandlerFunc(x.leaveGroup)))
	x.mux.Handle("PUT /v1/groups/{id}/mute-all", x.requireAuth(http.HandlerFunc(x.muteGroupAll)))
	x.mux.Handle("PATCH /v1/groups/{id}/nickname", x.requireAuth(http.HandlerFunc(x.groupNickname)))
	x.mux.Handle("POST /v1/groups/{id}/disband", x.requireAuth(http.HandlerFunc(x.userDisbandGroup)))
	x.mux.Handle("POST /v1/groups/{id}/members", x.requireAuth(http.HandlerFunc(x.addMembers)))
	x.mux.Handle("GET /v1/groups/{id}/members", x.requireAuth(http.HandlerFunc(x.groupMembers)))
	x.mux.Handle("DELETE /v1/groups/{id}/members/{userId}", x.requireAuth(http.HandlerFunc(x.removeMember)))
	x.mux.Handle("PUT /v1/groups/{id}/members/{userId}/role", x.requireAuth(http.HandlerFunc(x.groupRole)))
	x.mux.Handle("PUT /v1/groups/{id}/members/{userId}/mute", x.requireAuth(http.HandlerFunc(x.mute)))
	x.mux.Handle("POST /v1/conversations/{id}/messages", x.requireAuth(http.HandlerFunc(x.sendMessage)))
	x.mux.Handle("POST /v1/scheduled-messages", x.requireAuth(http.HandlerFunc(x.createScheduledMessage)))
	x.mux.Handle("GET /v1/scheduled-messages", x.requireAuth(http.HandlerFunc(x.scheduledMessages)))
	x.mux.Handle("PATCH /v1/scheduled-messages/{id}", x.requireAuth(http.HandlerFunc(x.updateScheduledMessage)))
	x.mux.Handle("DELETE /v1/scheduled-messages/{id}", x.requireAuth(http.HandlerFunc(x.cancelScheduledMessage)))
	x.mux.Handle("POST /v1/conversations/{targetId}/forward", x.requireAuth(http.HandlerFunc(x.forwardMessages)))
	x.mux.Handle("GET /v1/conversations/{id}/messages", x.requireAuth(http.HandlerFunc(x.history)))
	x.mux.Handle("GET /v1/conversations/{id}/messages/search", x.requireAuth(http.HandlerFunc(x.searchMessages)))
	x.mux.Handle("POST /v1/messages/{id}/recall", x.requireAuth(http.HandlerFunc(x.recall)))
	x.mux.Handle("PATCH /v1/messages/{id}", x.requireAuth(http.HandlerFunc(x.editMessage)))
	x.mux.Handle("GET /v1/messages/{id}/edits", x.requireAuth(http.HandlerFunc(x.messageEdits)))
	x.mux.Handle("PUT /v1/messages/{id}/reactions/{emoji}", x.requireAuth(http.HandlerFunc(x.messageReaction)))
	x.mux.Handle("DELETE /v1/messages/{id}/reactions/{emoji}", x.requireAuth(http.HandlerFunc(x.messageReaction)))
	x.mux.Handle("PUT /v1/groups/{id}/pinned-messages/{messageId}", x.requireAuth(http.HandlerFunc(x.groupMessagePin)))
	x.mux.Handle("DELETE /v1/groups/{id}/pinned-messages/{messageId}", x.requireAuth(http.HandlerFunc(x.groupMessagePin)))
	x.mux.Handle("GET /v1/groups/{id}/pinned-messages", x.requireAuth(http.HandlerFunc(x.groupMessagePins)))
	x.mux.Handle("PUT /v1/conversations/{id}/pinned-messages/{messageId}", x.requireAuth(http.HandlerFunc(x.groupMessagePin)))
	x.mux.Handle("DELETE /v1/conversations/{id}/pinned-messages/{messageId}", x.requireAuth(http.HandlerFunc(x.groupMessagePin)))
	x.mux.Handle("GET /v1/conversations/{id}/pinned-messages", x.requireAuth(http.HandlerFunc(x.groupMessagePins)))
	x.mux.Handle("PUT /v1/conversations/{id}/read", x.requireAuth(http.HandlerFunc(x.read)))
	x.mux.Handle("PUT /v1/conversations/{id}/delivered", x.requireAuth(http.HandlerFunc(x.delivered)))
	x.mux.Handle("POST /v1/conversations/{id}/typing", x.requireAuth(http.HandlerFunc(x.typing)))
	x.mux.Handle("GET /v1/sync", x.requireAuth(http.HandlerFunc(x.sync)))
	x.mux.Handle("GET /v1/calls/config", x.requireAuth(http.HandlerFunc(x.callConfig)))
	x.mux.Handle("POST /v1/calls/invite", x.requireAuth(http.HandlerFunc(x.inviteCall)))
	x.mux.Handle("GET /v1/calls/{id}", x.requireAuth(http.HandlerFunc(x.getCall)))
	x.mux.Handle("POST /v1/calls/{id}/accept", x.requireAuth(http.HandlerFunc(x.acceptCall)))
	x.mux.Handle("POST /v1/calls/{id}/reject", x.requireAuth(http.HandlerFunc(x.rejectCall)))
	x.mux.Handle("POST /v1/calls/{id}/cancel", x.requireAuth(http.HandlerFunc(x.cancelCall)))
	x.mux.Handle("POST /v1/calls/{id}/hangup", x.requireAuth(http.HandlerFunc(x.hangupCall)))
	x.mux.Handle("POST /v1/reports", x.requireAuth(http.HandlerFunc(x.report)))
	x.mux.Handle("GET /v1/announcements", x.requireAuth(http.HandlerFunc(x.announcements)))
	x.mux.Handle("POST /v1/announcements/{id}/read", x.requireAuth(http.HandlerFunc(x.readAnnouncement)))
	x.mux.Handle("POST /v1/ws/ticket", x.requireAuth(http.HandlerFunc(x.websocketTicket)))
	x.mux.HandleFunc("GET /v1/ws", x.websocket)
	x.mux.Handle("GET /v1/admin/stats", x.requireAdmin(http.HandlerFunc(x.adminStats)))
	x.mux.Handle("GET /v1/admin/dashboard", x.requireAdmin(http.HandlerFunc(x.adminStats)))
	x.mux.Handle("GET /v1/admin/users", x.requireAdmin(http.HandlerFunc(x.adminUsers)))
	x.mux.Handle("GET /v1/admin/users/{id}", x.requireAdmin(http.HandlerFunc(x.adminUserOverview)))
	x.mux.Handle("POST /v1/admin/users/{id}/ban", x.requireAdmin(http.HandlerFunc(x.adminBan)))
	x.mux.Handle("POST /v1/admin/users/{id}/unban", x.requireAdmin(http.HandlerFunc(x.adminUnban)))
	x.mux.Handle("GET /v1/admin/reports", x.requireAdmin(http.HandlerFunc(x.adminReports)))
	x.mux.Handle("POST /v1/admin/reports/{id}/resolve", x.requireAdmin(http.HandlerFunc(x.resolveReport)))
	x.mux.Handle("GET /v1/admin/audit-logs", x.requireAdmin(http.HandlerFunc(x.auditLogs)))
	x.mux.Handle("GET /v1/admin/messages", x.requireAdmin(http.HandlerFunc(x.adminMessages)))
	x.mux.Handle("GET /v1/admin/tasks/media-cleanup", x.requireAdmin(http.HandlerFunc(x.adminMediaCleanupStatus)))
	x.mux.Handle("GET /v1/admin/tasks", x.requireAdmin(http.HandlerFunc(x.adminTasks)))
	x.mux.Handle("GET /v1/admin/friendships", x.requireAdmin(http.HandlerFunc(x.adminFriendships)))
	x.mux.Handle("GET /v1/admin/feedback", x.requireAdmin(http.HandlerFunc(x.adminFeedback)))
	x.mux.Handle("GET /v1/admin/push", x.requireAdmin(http.HandlerFunc(x.adminPushStatus)))
	x.mux.Handle("GET /v1/admin/access", x.requireAdmin(http.HandlerFunc(x.adminAccess)))
	x.mux.Handle("GET /v1/admin/media", x.requireAdmin(http.HandlerFunc(x.adminMedia)))
	x.mux.Handle("GET /v1/admin/online", x.requireAdmin(http.HandlerFunc(x.adminOnline)))
	x.mux.Handle("GET /v1/admin/announcements", x.requireAdmin(http.HandlerFunc(x.adminAnnouncements)))
	x.mux.Handle("POST /v1/admin/announcements", x.requireAdmin(http.HandlerFunc(x.createAnnouncement)))
	x.mux.Handle("PUT /v1/admin/announcements/{id}", x.requireAdmin(http.HandlerFunc(x.updateAnnouncement)))
	x.mux.Handle("POST /v1/admin/announcements/{id}/publish", x.requireAdmin(http.HandlerFunc(x.publishAnnouncement)))
	x.mux.Handle("POST /v1/admin/announcements/{id}/withdraw", x.requireAdmin(http.HandlerFunc(x.withdrawAnnouncement)))
	x.mux.Handle("DELETE /v1/admin/announcements/{id}", x.requireAdmin(http.HandlerFunc(x.deleteAnnouncement)))
	x.mux.Handle("GET /v1/admin/calls", x.requireAdmin(http.HandlerFunc(x.adminCalls)))
	x.mux.Handle("GET /v1/admin/health", x.requireAdmin(http.HandlerFunc(x.health)))
	x.mux.Handle("GET /v1/admin/groups", x.requireAdmin(http.HandlerFunc(x.adminGroups)))
	x.mux.Handle("GET /v1/admin/groups/{id}", x.requireAdmin(http.HandlerFunc(x.adminGroupOverview)))
	x.mux.Handle("GET /v1/admin/groups/{id}/members", x.requireAdmin(http.HandlerFunc(x.adminGroupMembers)))
	x.mux.Handle("POST /v1/admin/groups/{id}/disband", x.requireAdmin(http.HandlerFunc(x.disbandGroup)))
	x.mux.Handle("GET /v1/admin/sensitive-words", x.requireAdmin(http.HandlerFunc(x.sensitiveWords)))
	x.mux.Handle("POST /v1/admin/sensitive-words", x.requireAdmin(http.HandlerFunc(x.addSensitiveWord)))
	x.mux.Handle("DELETE /v1/admin/sensitive-words/{id}", x.requireAdmin(http.HandlerFunc(x.deleteSensitiveWord)))
	x.mux.Handle("GET /v1/admin/settings", x.requireAdmin(http.HandlerFunc(x.settings)))
	x.mux.Handle("PUT /v1/admin/settings", x.requireAdmin(http.HandlerFunc(x.updateSettings)))
	x.mux.HandleFunc("/api/v1/admin/", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
		x.mux.ServeHTTP(w, r)
	})
}

func (x *API) callConfig(w http.ResponseWriter, _ *http.Request) {
	servers := make([]map[string]any, 0, 2)
	if len(x.cfg.RTCSTUNURLs) > 0 {
		servers = append(servers, map[string]any{"urls": x.cfg.RTCSTUNURLs})
	}
	if len(x.cfg.RTCTURNURLs) > 0 {
		servers = append(servers, map[string]any{"urls": x.cfg.RTCTURNURLs, "username": x.cfg.RTCTURNUsername, "credential": x.cfg.RTCTURNCredential})
	}
	write(w, http.StatusOK, map[string]any{"iceServers": servers, "inviteTimeoutSeconds": int(x.cfg.CallInviteTTL.Seconds())})
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
	write(w, http.StatusOK, map[string]any{"call": call, "duplicate": duplicate})
}
func (x *API) acceptCall(w http.ResponseWriter, r *http.Request) { x.callAction(w, r, "accept") }
func (x *API) rejectCall(w http.ResponseWriter, r *http.Request) { x.callAction(w, r, "reject") }
func (x *API) cancelCall(w http.ResponseWriter, r *http.Request) { x.callAction(w, r, "cancel") }
func (x *API) hangupCall(w http.ResponseWriter, r *http.Request) { x.callAction(w, r, "hangup") }
func (x *API) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if strings.HasPrefix(r.URL.Path, "/v1/auth/") || r.URL.Path == "/v1/admin/auth/login" || r.URL.Path == "/v1/ws/ticket" {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Pragma", "no-cache")
		}
		origin := r.Header.Get("Origin")
		if origin != "" && x.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Admin-Key")
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
		adminPath := strings.HasPrefix(r.URL.Path, "/v1/admin/") || strings.HasPrefix(r.URL.Path, "/api/v1/admin/")
		if !probe && !adminPath {
			if maintenance, announcement := x.app.MaintenanceStatus(); maintenance {
				if announcement == "" {
					announcement = "系统正在维护，请稍后重试"
				}
				writeError(w, http.StatusServiceUnavailable, "MAINTENANCE", announcement)
				return
			}
		}
		if !probe && !x.limits.allow("http:"+x.clientIP(r), 300, time.Minute) {
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
	// 内存模式以及 Redis 短时不可用时保留单节点保护；生产 readiness
	// 会同时报告 Redis 故障，恢复后自动回到全局原子限流。
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
		key := r.Header.Get("X-Admin-Key")
		if key == "" {
			key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		adminID, role := "", ""
		if x.cfg.AdminSharedKeyEnabled && x.cfg.AdminKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(x.cfg.AdminKey)) == 1 {
			adminID, role = "bootstrap", "platform_admin"
		} else {
			claims, err := x.auth.ParseClaims(key, "admin")
			if err != nil {
				x.app.RecordAdminAudit("unknown", "admin.authorization.failed", "admin_request", r.URL.Path, "failed", x.clientIP(r), map[string]any{"method": r.Method, "reason": "invalid credential"})
				writeError(w, 401, "UNAUTHENTICATED", "invalid admin credential")
				return
			}
			adminID, role = claims.Subject, claims.Role
		}
		if !roleAllowed(role, adminPermission(r.Method, r.URL.Path)) {
			x.app.RecordAdminAudit(adminID, "admin.authorization.denied", "admin_request", r.URL.Path, "failed", x.clientIP(r), map[string]any{"method": r.Method, "role": role})
			writeError(w, 403, "FORBIDDEN", "administrator role is not permitted")
			return
		}
		ctx := context.WithValue(context.WithValue(r.Context(), userKey, adminID), roleKey, role)
		if r.Method == http.MethodGet {
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
		x.app.RecordAdminAudit(adminID, "admin.request", "admin_request", r.URL.Path, result, x.clientIP(r), map[string]any{"method": r.Method, "role": role, "status": status})
	})
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
	switch err {
	case app.ErrInvalid:
		writeError(w, 400, "INVALID_ARGUMENT", err.Error())
	case app.ErrForbidden:
		writeError(w, 403, "FORBIDDEN", err.Error())
	case app.ErrNotFound:
		writeError(w, 404, "NOT_FOUND", err.Error())
	case app.ErrConflict:
		writeError(w, 409, "CONFLICT", err.Error())
	default:
		writeError(w, 500, "INTERNAL", "internal server error")
	}
}

func (x *API) health(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"status": "ok", "service": "linli-im", "uptimeSeconds": int(time.Since(x.started).Seconds())})
}
func (x *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := x.app.Ready(ctx); err != nil {
		writeError(w, 503, "NOT_READY", err.Error())
		return
	}
	write(w, 200, map[string]string{"status": "ready"})
}
func (x *API) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	metrics := &x.app.Metrics
	fmt.Fprintf(w, "im_http_requests_total %d\nim_http_requests_in_flight %d\nim_messages_sent_total %d\nim_websocket_connections %d\nim_websocket_dropped_events_total %d\nim_errors_total %d\nim_message_fanout_recipients_total %d\nim_runtime_retention_deleted_total %d\n",
		metrics.Requests.Load(), metrics.HTTPInFlight.Load(), metrics.Messages.Load(), metrics.WSConnections.Load(), metrics.WSDroppedEvents.Load(), metrics.Errors.Load(), metrics.FanoutRecipients.Load(), metrics.RetentionDeleted.Load())
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
	fmt.Fprintf(w, "im_redis_pool_total_connections %d\nim_redis_pool_idle_connections %d\nim_redis_pool_timeouts_total %d\nim_push_outbox_pending %d\nim_push_outbox_oldest_seconds %g\nim_event_outbox_pending %d\nim_event_outbox_oldest_seconds %g\nim_message_fanout_pending %d\nim_message_fanout_oldest_seconds %g\n",
		stats.RedisTotalConnections, stats.RedisIdleConnections, stats.RedisTimeouts, stats.PushPending, stats.OldestPushSeconds, stats.EventPending, stats.OldestEventSeconds, stats.FanoutPending, stats.OldestFanoutSeconds)
}
func (x *API) requestCode(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Phone string `json:"phone"`
	}
	if decode(r, &p) != nil || strings.TrimSpace(p.Phone) == "" {
		writeError(w, 400, "INVALID_ARGUMENT", "phone is required")
		return
	}
	if !x.allow(r.Context(), "otp-ip:"+x.clientIP(r), 5, 10*time.Minute) || !x.allow(r.Context(), "otp-phone:"+strings.TrimSpace(p.Phone), 3, 10*time.Minute) {
		w.Header().Set("Retry-After", "600")
		writeError(w, 429, "RATE_LIMITED", "verification requests are temporarily limited")
		return
	}
	if x.otp == nil {
		writeError(w, http.StatusServiceUnavailable, "SMS_NOT_CONFIGURED", "verification provider is not configured")
		return
	}
	if err := x.otp.Request(r.Context(), strings.TrimSpace(p.Phone)); err != nil {
		writeError(w, http.StatusBadGateway, "SMS_UNAVAILABLE", "verification provider is unavailable")
		return
	}
	resp := map[string]any{"expiresIn": 300}
	write(w, 202, resp)
}
func (x *API) login(w http.ResponseWriter, r *http.Request) {
	var p struct{ Phone, Code, Name string }
	if decode(r, &p) != nil || p.Phone == "" || p.Code == "" {
		writeError(w, 400, "INVALID_ARGUMENT", "phone and code are required")
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
	x.issueUserSession(w, u)
}
func (x *API) issueUserSession(w http.ResponseWriter, u *model.User) {
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
	write(w, 200, map[string]any{"user": u, "accessToken": a, "refreshToken": refresh, "expiresIn": int(x.cfg.AccessTTL.Seconds())})
}
func (x *API) register(w http.ResponseWriter, r *http.Request) {
	var p struct{ Phone, Code, Password, Name string }
	if decode(r, &p) != nil || p.Phone == "" || p.Code == "" || p.Password == "" || p.Name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "phone, code, password and name are required")
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
	x.issueUserSession(w, u)
}
func (x *API) passwordLogin(w http.ResponseWriter, r *http.Request) {
	var p struct{ Phone, Password string }
	if decode(r, &p) != nil || p.Phone == "" || p.Password == "" {
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
	x.issueUserSession(w, u)
}
func (x *API) passwordResetCode(w http.ResponseWriter, r *http.Request) {
	var p struct{ Phone string }
	if decode(r, &p) != nil || strings.TrimSpace(p.Phone) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "phone is required")
		return
	}
	p.Phone = strings.TrimSpace(p.Phone)
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
	if decode(r, &p) != nil || p.Phone == "" || p.Code == "" || p.Password == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "phone, code and password are required")
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
	var p struct{ Email, Password, TOTP string }
	if decode(r, &p) != nil {
		x.app.RecordAdminAudit("unknown", "admin.login", "admin_session", "login", "failed", x.clientIP(r), map[string]any{"reason": "invalid request"})
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(strings.TrimSpace(p.Email))), []byte(strings.ToLower(x.cfg.AdminEmail))) != 1 || !verifyAdminPassword(x.cfg.AdminPasswordHash, p.Password) || (x.cfg.AdminTOTPSecret != "" && !verifyTOTP(x.cfg.AdminTOTPSecret, p.TOTP, time.Now())) {
		x.app.RecordAdminAudit("unknown", "admin.login", "admin_session", "login", "failed", x.clientIP(r), map[string]any{"reason": "invalid credentials"})
		writeError(w, 401, "UNAUTHENTICATED", "invalid administrator credentials")
		return
	}
	token, err := x.auth.IssueAdmin(x.cfg.AdminID, x.cfg.AdminRole, 15*time.Minute)
	if err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(x.cfg.AdminID, "admin.login", "admin_session", "login", "success", x.clientIP(r), map[string]any{"role": x.cfg.AdminRole})
	write(w, 200, map[string]any{"accessToken": token, "expiresIn": 900, "displayName": x.cfg.AdminID, "role": x.cfg.AdminRole})
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
	write(w, 200, map[string]any{"accessToken": a, "refreshToken": refresh, "expiresIn": int(x.cfg.AccessTTL.Seconds())})
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
	return "/v1/avatars/" + mediaID + "?expires=" + strconv.FormatInt(expires, 10) + "&signature=" + signature
}

func avatarMediaIDFromPath(value string) string {
	const prefix = "/v1/media/"
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
	if decode(r, &p) != nil || strings.TrimSpace(p.Phone) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "phone is required")
		return
	}
	p.Phone = strings.TrimSpace(p.Phone)
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
	if decode(r, &p) != nil || strings.TrimSpace(p.Phone) == "" || p.Code == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "phone and code are required")
		return
	}
	p.Phone = strings.TrimSpace(p.Phone)
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

func (x *API) syncEventsWithDownloadURLs(ctx context.Context, userID string, events []*model.SyncEvent) ([]*model.SyncEvent, error) {
	raw, err := json.Marshal(events)
	if err != nil {
		return nil, err
	}
	cloned := []*model.SyncEvent{}
	if err = json.Unmarshal(raw, &cloned); err != nil {
		return nil, err
	}
	for _, event := range cloned {
		value, ok := event.Payload["message"]
		if !ok {
			continue
		}
		messageRaw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return nil, marshalErr
		}
		message := &model.Message{}
		if unmarshalErr := json.Unmarshal(messageRaw, message); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		message, err = x.messageWithDownloadURL(ctx, userID, message)
		if err != nil {
			return nil, err
		}
		event.Payload["message"] = message
	}
	return cloned, nil
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
	write(w, 200, map[string]any{"items": x.app.FriendRequests(uid(r))})
}
func (x *API) friends(w http.ResponseWriter, r *http.Request) {
	items := x.app.Friends(uid(r))
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
		SourceMessageIDs []string `json:"sourceMessageIds"`
		Mode             string   `json:"mode"`
		ClientBatchID    string   `json:"clientBatchId"`
	}
	if decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	messages, duplicate, err := x.app.ForwardMessages(uid(r), r.PathValue("targetId"), p.SourceMessageIDs, p.Mode, p.ClientBatchID)
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
	items, err := x.app.SearchConversationMessages(uid(r), r.PathValue("id"), r.URL.Query().Get("q"), before, limit)
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
	item, duplicate, err := x.app.SetGroupMessagePin(uid(r), r.PathValue("id"), r.PathValue("messageId"), r.Method == http.MethodPut)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": item, "duplicate": duplicate})
}
func (x *API) groupMessagePins(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := x.app.GroupMessagePins(uid(r), r.PathValue("id"), before, limit)
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
		writeError(w, 403, "FORBIDDEN", "not a conversation member")
		return
	}
	w.WriteHeader(204)
}
func (x *API) sync(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, cursor, more := x.app.SyncContext(r.Context(), uid(r), after, limit)
	var err error
	events, err = x.syncEventsWithDownloadURLs(r.Context(), uid(r), events)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]any{"events": events, "cursor": cursor, "hasMore": more})
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
func (x *API) websocketTicket(w http.ResponseWriter, r *http.Request) {
	if !x.allow(r.Context(), "ws-ticket-issue:"+uid(r), 30, time.Minute) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many websocket ticket requests")
		return
	}
	const ttl = 30 * time.Second
	ticket, err := x.auth.IssueWebSocket(uid(r), ttl)
	if err != nil {
		handleErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, http.StatusCreated, map[string]any{"ticket": ticket, "expiresIn": int(ttl.Seconds())})
}
func (x *API) websocket(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("ticket")
	claims, err := x.auth.ParseClaims(raw, "ws")
	if err != nil {
		writeError(w, 401, "UNAUTHENTICATED", err.Error())
		return
	}
	if claims.ID == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "websocket ticket id is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	allowed, consumeErr := x.app.AllowRate(ctx, "ws-ticket-consume:"+claims.ID, 1, time.Minute)
	cancel()
	if consumeErr != nil {
		if x.cfg.Environment == "production" {
			writeError(w, http.StatusServiceUnavailable, "TICKET_STORE_UNAVAILABLE", "websocket ticket verification is unavailable")
			return
		}
		allowed = x.limits.allow("ws-ticket-consume:"+claims.ID, 1, time.Minute)
	}
	if !allowed {
		writeError(w, http.StatusUnauthorized, "TICKET_USED", "websocket ticket has already been used")
		return
	}
	u, err := x.app.UserContext(r.Context(), claims.Subject)
	if err != nil || u.Banned {
		writeError(w, 403, "FORBIDDEN", "account unavailable")
		return
	}
	if claims.ExpiresAt == nil {
		writeError(w, 401, "UNAUTHENTICATED", "token expiry is required")
		return
	}
	expiresAt := time.Now().Add(x.cfg.AccessTTL)
	x.hub.Serve(w, r, claims.Subject, expiresAt)
}
func (x *API) adminStats(w http.ResponseWriter, r *http.Request) { write(w, 200, x.app.AdminStats()) }
func (x *API) adminUsers(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, total, next, err := x.app.AdminUsersPage(query.Get("q"), query.Get("status"), query.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]any{"items": items, "total": total, "nextCursor": next})
}
func (x *API) adminUserOverview(w http.ResponseWriter, r *http.Request) {
	item, err := x.app.AdminUserOverview(r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, item)
}
func (x *API) adminBan(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Reason        string
		DurationHours int `json:"durationHours"`
	}
	if decode(r, &p) != nil || strings.TrimSpace(p.Reason) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "reason is required")
		return
	}
	if err := x.app.AdminBan(uid(r), r.PathValue("id"), true, p.DurationHours, p.Reason); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, map[string]bool{"banned": true})
}
func (x *API) adminUnban(w http.ResponseWriter, r *http.Request) {
	var p struct{ Reason string }
	if decode(r, &p) != nil || strings.TrimSpace(p.Reason) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "reason is required")
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
	var p struct{ Action, Note string }
	if decode(r, &p) != nil || strings.TrimSpace(p.Note) == "" {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	status, err := x.app.ResolveReport(uid(r), r.PathValue("id"), p.Action, p.Note)
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
		handleErr(w, err)
		return
	}
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
	write(w, http.StatusOK, map[string]any{
		"current":        map[string]any{"id": uid(r), "role": r.Context().Value(roleKey)},
		"administrators": []map[string]any{{"id": x.cfg.AdminID, "role": x.cfg.AdminRole, "source": "environment", "mutable": false}},
		"roles": []map[string]any{
			{"id": "platform_admin", "permissions": []string{"read", "users.write", "groups.write", "reports.write", "rules.write", "announcements.write", "settings.write"}},
			{"id": "moderator", "permissions": []string{"read", "users.write", "reports.write", "rules.write"}},
			{"id": "support", "permissions": []string{"read"}},
		},
		"note": "管理员账号由生产环境密钥管理配置，本接口只读且不回显凭据。",
	})
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
	items := x.hub.Presence()
	write(w, http.StatusOK, map[string]any{"items": items, "totalUsers": len(items), "totalConnections": x.app.Metrics.WSConnections.Load()})
}
func announcementInput(r *http.Request) (store.AnnouncementInput, error) {
	var p struct {
		Title         string     `json:"title"`
		Content       string     `json:"content"`
		Status        string     `json:"status"`
		Pinned        bool       `json:"pinned"`
		TargetType    string     `json:"targetType"`
		TargetUserIDs []string   `json:"targetUserIds"`
		ScheduledAt   *time.Time `json:"scheduledAt"`
		PushOnPublish bool       `json:"pushOnPublish"`
	}
	err := decode(r, &p)
	return store.AnnouncementInput{Title: p.Title, Content: p.Content, Status: p.Status, Pinned: p.Pinned, TargetType: p.TargetType, TargetUserIDs: p.TargetUserIDs, ScheduledAt: p.ScheduledAt, PushOnPublish: p.PushOnPublish}, err
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
	input, err := announcementInput(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
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
	input, err := announcementInput(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
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
		EnqueuePush bool `json:"enqueuePush"`
	}
	if decode(r, &p) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
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
	item, err := x.app.WithdrawAnnouncement(uid(r), r.PathValue("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, item)
}
func (x *API) deleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Reason string `json:"reason"`
	}
	if decode(r, &p) != nil || strings.TrimSpace(p.Reason) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "reason is required")
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
func (x *API) disbandGroup(w http.ResponseWriter, r *http.Request) {
	var p struct{ Reason string }
	if decode(r, &p) != nil || strings.TrimSpace(p.Reason) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "reason is required")
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
	var p struct{ Word, Category string }
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
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
		Reason string `json:"reason"`
	}
	if decode(r, &p) != nil || strings.TrimSpace(p.Reason) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "reason is required")
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
		"database":      x.cfg.Mode == "full" && x.cfg.DatabaseURL != "",
		"redis":         x.cfg.RedisURL != "",
		"objectStorage": x.cfg.S3Endpoint != "" && x.cfg.S3AccessKey != "" && x.cfg.S3SecretKey != "",
		"otpProvider":   x.cfg.DevMode || (x.cfg.OTPWebhookURL != "" && x.cfg.OTPWebhookToken != ""),
		"pushProvider":  pushConfigured,
		"apnsVoIP":      apnsVoIPConfigured,
		"turn":          len(x.cfg.RTCTURNURLs) > 0 && x.cfg.RTCTURNUsername != "" && x.cfg.RTCTURNCredential != "",
		"adminTOTP":     x.cfg.AdminTOTPSecret != "",
	}
	values["infrastructure"] = map[string]any{
		"pushProvider": x.cfg.PushProvider, "mediaMaxSizeMB": x.cfg.MediaMaxBytes / (1 << 20),
		"apnsVoipSandbox":          x.cfg.APNSVoIPSandbox,
		"callInviteTimeoutSeconds": int64(x.cfg.CallInviteTTL / time.Second), "websocketMaxPerUser": x.cfg.WSMaxPerUser,
		"websocketMaxPerIP": x.cfg.WSMaxPerIP, "websocketMaxConnections": x.cfg.WSMaxConnections, "accessTokenMinutes": int64(x.cfg.AccessTTL / time.Minute),
		"refreshTokenHours": int64(x.cfg.RefreshTTL / time.Hour),
	}
	values["restartRequiredKeys"] = []string{"pushProvider", "mediaMaxSizeMB", "callInviteTimeoutSeconds", "websocketMaxPerUser", "websocketMaxPerIP", "websocketMaxConnections", "accessTokenMinutes", "refreshTokenHours"}
	return values
}
func (x *API) settings(w http.ResponseWriter, r *http.Request) { write(w, 200, x.settingsPayload()) }
func (x *API) updateSettings(w http.ResponseWriter, r *http.Request) {
	var p map[string]any
	if decode(r, &p) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid request")
		return
	}
	if err := x.app.UpdateSettings(uid(r), p); err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, x.settingsPayload())
}
