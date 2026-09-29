package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/linli/im/server/internal/app"
	livekitcontrol "github.com/linli/im/server/internal/livekit"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/net/http/httpguts"
)

type tenantLiveKitControl interface {
	IssueTenantParticipant(string, string, string, tenancy.Identity, int64, int64) (livekitcontrol.ParticipantSession, error)
	VerifyTenantParticipant(string) (livekitcontrol.TenantParticipant, error)
}

type tenantSignalKey struct{}
type tenantSignalTransport struct {
	base      http.RoundTripper
	fence     func(context.Context, string, func() error) error
	authorize func(context.Context, livekitcontrol.TenantParticipant) error
	begin     func(context.Context, tenancy.Identity, int64, int64) (string, error)
	complete  func(context.Context, string) error
}

func (t tenantSignalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p, ok := r.Context().Value(tenantSignalKey{}).(livekitcontrol.TenantParticipant)
	if !ok {
		return nil, app.ErrForbidden
	}
	var response *http.Response
	err := t.fence(r.Context(), p.Identity.LocalUserID, func() error {
		if err := t.authorize(r.Context(), p); err != nil {
			return err
		}
		attempt := ""
		if !strings.HasSuffix(r.URL.Path, "/validate") {
			var err error
			attempt, err = t.begin(r.Context(), p.Identity, p.AuthVersion, p.RealmVersion)
			if err != nil {
				return err
			}
		}
		var err error
		// Pin startup through the upstream handshake, not the whole call. In the
		// pinned LiveKit server the participant is registered before HTTP 101.
		// Revocation shares this lock and cannot miss a successful in-flight join.
		response, err = t.base.RoundTrip(r)
		if err == nil && response != nil && attempt != "" && response.StatusCode == http.StatusSwitchingProtocols {
			// Participant creation precedes 101 in the pinned server. No silent
			// timeout cleanup for a missing/ambiguous response: block revocation.
			err = t.complete(r.Context(), attempt)
		}
		return err
	})
	if err != nil && response != nil {
		response.Body.Close()
		response = nil
	}
	return response, err
}

func (x *API) configureTenantLiveKit() {
	x.tenantMediaProxy = nil
	if x.cfg.TenantID == "" || !x.cfg.LiveKitEnabled || x.tenantStore == nil {
		return
	}
	upstream, err := url.Parse(x.cfg.LiveKitAPIURL)
	if err != nil || upstream.Host == "" || (upstream.Scheme != "http" && upstream.Scheme != "https") || upstream.User != nil || upstream.RawQuery != "" || upstream.Fragment != "" {
		return
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 20 * time.Second
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(upstream)
			p.Out.URL.Path = strings.TrimPrefix(p.In.URL.Path, "/livekit")
			p.Out.URL.RawPath = ""
			// Forward only the normal LiveKit protocol; never a caller-supplied
			// destination, administrative API path or business authorization JWT.
			p.Out.Header.Del("Cookie")
		},
		Transport: tenantSignalTransport{base: transport, fence: x.tenantStore.WithTenantSessionFence, authorize: x.authorizeTenantParticipant, begin: x.tenantStore.BeginTenantMediaAttempt, complete: x.tenantStore.CompleteTenantMediaAttempt},
		ErrorLog:  log.New(io.Discard, "", 0), // transport errors can contain token URLs
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, e error) {
			status := http.StatusServiceUnavailable
			if errors.Is(e, app.ErrForbidden) || errors.Is(e, store.ErrForbidden) {
				status = http.StatusUnauthorized
			}
			writeError(w, status, "TENANT_CALL_UNAVAILABLE", "通话身份已失效或通话服务暂不可用")
		},
		ModifyResponse: func(r *http.Response) error {
			if r.StatusCode >= 300 && r.StatusCode < 400 {
				return app.ErrUnavailable
			}
			r.Header.Set("Cache-Control", "no-store")
			return nil
		},
	}
	x.tenantMediaProxy = proxy
}

func (x *API) authorizeTenantParticipant(ctx context.Context, p livekitcontrol.TenantParticipant) error {
	if p.Identity.TenantID != x.cfg.TenantID || x.tenantStore == nil {
		return app.ErrForbidden
	}
	i, v, err := x.tenantStore.TenantAuthIdentity(ctx, x.cfg.TenantID, p.Identity.LocalUserID)
	if err != nil {
		return err
	}
	if i != p.Identity || v != p.AuthVersion {
		return app.ErrForbidden
	}
	realm, err := x.tenantStore.TenantRealmVersion(ctx, x.cfg.TenantID)
	if err != nil {
		return err
	}
	if realm != p.RealmVersion {
		return app.ErrForbidden
	}
	call, err := x.app.GetCall(p.Identity.LocalUserID, p.CallID)
	if err != nil {
		return err
	}
	if call.Status != "accepted" || call.ConversationID != p.ConversationID || !callUserCanJoin(call, p.Identity.LocalUserID) {
		return app.ErrForbidden
	}
	return nil
}

func (x *API) tenantLiveKitSignal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if x.cfg.TenantID == "" {
		http.NotFound(w, r)
		return
	}
	switch r.URL.Path {
	case "/livekit/rtc", "/livekit/rtc/validate", "/livekit/rtc/v1", "/livekit/rtc/v1/validate":
	default:
		http.NotFound(w, r)
		return
	}
	key, keyErr := base64.StdEncoding.DecodeString(r.Header.Get("Sec-WebSocket-Key"))
	if !strings.HasSuffix(r.URL.Path, "/validate") && (!httpguts.HeaderValuesContainsToken(r.Header.Values("Connection"), "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || r.Header.Get("Sec-WebSocket-Version") != "13" || keyErr != nil || len(key) != 16) {
		writeError(w, 400, "INVALID_CALL_HANDSHAKE", "invalid WebSocket handshake")
		return
	}
	control, ok := x.livekit.(tenantLiveKitControl)
	if !ok || x.tenantMediaProxy == nil || x.livekitSetupErr != nil {
		writeError(w, 503, "LIVEKIT_UNAVAILABLE", "通话服务暂不可用")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !x.originAllowed(origin) {
		writeError(w, 403, "FORBIDDEN_ORIGIN", "origin is not allowed")
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["access_token"]) > 1 || query.Get("publish") != "" {
		writeError(w, 401, "TENANT_CALL_REJECTED", "通话凭据不可用")
		return
	}
	token := query.Get("access_token")
	if header := r.Header.Get("Authorization"); header != "" {
		if !strings.HasPrefix(header, "Bearer ") || (token != "" && header != "Bearer "+token) {
			writeError(w, 401, "TENANT_CALL_REJECTED", "通话凭据不可用")
			return
		}
		token = strings.TrimPrefix(header, "Bearer ")
	}
	p, err := control.VerifyTenantParticipant(token)
	if err != nil || p.Identity.TenantID != x.cfg.TenantID {
		writeError(w, 401, "TENANT_CALL_REJECTED", "通话凭据不可用")
		return
	}
	x.tenantMediaProxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tenantSignalKey{}, p)))
}

// Called after the local identity has been frozen, under the same user lock as
// token issuance and signal handshakes. Only this user's media is removed.
// Every control failure keeps the durable platform operation pending.
func (x *API) revokeTenantMedia(ctx context.Context, user string) error {
	if err := x.tenantStore.CheckTenantMediaSettled(ctx, user); err != nil {
		return err
	}
	if !x.cfg.LiveKitEnabled {
		return nil
	}
	control, ok := x.livekit.(livekitAdminControl)
	if !ok || x.livekitSetupErr != nil || x.tenantMediaProxy == nil {
		return app.ErrUnavailable
	}
	rooms, err := control.ListRooms(ctx)
	if err != nil {
		return err
	}
	for _, room := range rooms {
		participants, err := control.ListParticipants(ctx, room.Name)
		if err != nil {
			return err
		}
		for _, participant := range participants {
			if participant.Identity == user {
				if err = control.RemoveParticipant(ctx, room.Name, user); err != nil {
					return err
				}
			}
		}
		remaining, err := control.ListParticipants(ctx, room.Name)
		if err != nil {
			return err
		}
		for _, participant := range remaining {
			if participant.Identity == user {
				return errors.New("media disconnect not confirmed")
			}
		}
	}
	return nil
}
