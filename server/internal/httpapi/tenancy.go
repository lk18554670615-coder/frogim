package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	livekitcontrol "github.com/linli/im/server/internal/livekit"
	"github.com/linli/im/server/internal/tenancy"
	"github.com/linli/im/server/internal/wukong"
	"net/http"
	"strings"
	"time"
)

func (x *API) setupTenancy() {
	o := tenancy.LoadOptions()
	if o.Mode != "enterprise" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	x.enterprise, x.tenancyErr = tenancy.NewEnterprise(ctx, x.cfg, o)
	if x.tenancyErr != nil {
		return
	}
	x.enterprise.Session = func(w http.ResponseWriter, r *http.Request, uid string) {
		u, e := x.app.UserContext(r.Context(), uid)
		if e != nil {
			handleErr(w, e)
			return
		}
		x.issueUserSession(w, r, u)
	}
	x.enterprise.Offline = x.offlineEnterpriseUser
	x.controlServer, x.tenancyErr = tenancy.StartControl(o, x.enterprise.ControlHandler())
	if x.tenancyErr == nil {
		x.mux.HandleFunc("POST /v2/auth/enterprise-session", x.requireClientPlatform(x.enterprise.Exchange))
		x.mux.HandleFunc("GET /livekit/", x.enterpriseCallSignal)
	}
}
func (x *API) RunTenancySync(ctx context.Context) {
	if x.enterprise != nil {
		x.enterprise.RunSync(ctx)
	}
}
func (x *API) enterpriseHandler(next http.Handler) http.Handler {
	if x.enterprise == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/v2/avatars/") {
			writeError(w, 403, "ENTERPRISE_MEDIA_SESSION_REQUIRED", "use enterprise media session")
			return
		}
		if strings.HasPrefix(p, "/v2/auth/") && p != "/v2/auth/enterprise-session" && p != "/v2/auth/logout" && p != "/v2/auth/im-session" && p != "/v2/auth/media-session" {
			writeError(w, 409, "PLATFORM_AUTH_REQUIRED", "当前客户端需要升级，请先使用网页版："+strings.TrimRight(x.enterprise.PublicAPIBase(), "/")+"/app/")
			return
		}
		// Credentials and global identity cannot be edited in the enterprise console.
		if p == "/v2/users/me/phone" || p == "/v2/users/me/password" || (r.Method != "GET" && strings.HasPrefix(p, "/v2/admin/users") && (strings.Contains(p, "password") || strings.Contains(p, "phone") || (p == "/v2/admin/users" || p == "/v2/admin/users/batch"))) {
			writeError(w, 409, "PLATFORM_IDENTITY_REQUIRED", "change identity through the platform")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (x *API) offlineEnterpriseUser(ctx context.Context, uid string) error {
	if x.wukongClient == nil {
		return errors.New("IM unavailable")
	}
	for _, flag := range []int{wukong.DeviceApp, wukong.DeviceWeb, wukong.DeviceDesktop} {
		b := make([]byte, 32)
		if _, e := rand.Read(b); e != nil {
			return e
		}
		if e := x.wukongClient.ProvisionUser(ctx, wukong.UserTokenRequest{UID: uid, Token: hex.EncodeToString(b), DeviceFlag: flag, DeviceLevel: wukong.DeviceLevelMaster}); e != nil {
			return e
		}
		if e := x.wukongClient.QuitDevice(ctx, uid, flag); e != nil {
			return e
		}
		if e := x.app.InvalidateWukongCredential(ctx, uid, flag); e != nil {
			return e
		}
	}
	rows, e := x.enterprise.DB.Query(ctx, `SELECT id FROM im_call_sessions WHERE status IN ('invited','accepted') AND (caller_id=$1 OR callee_id=$1 OR $1=ANY(participant_ids))`, uid)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if x.livekit == nil {
			return errors.New("call control unavailable")
		}
		if e = x.livekit.RemoveParticipant(ctx, livekitcontrol.CallRoomName(id), uid); e != nil {
			return e
		}
		call, _, transitionErr := x.app.TransitionCall(uid, id, "end", "enterprise_switch")
		if transitionErr != nil {
			// Expiring an invitation can finish the call before the transition.
			call, e = x.app.GetCall(uid, id)
			if e != nil || !isTerminalCallStatus(call.Status) {
				return transitionErr
			}
		}
		if isTerminalCallStatus(call.Status) {
			e = x.livekit.DeleteCallRoom(ctx, id)
		}
		if e != nil {
			return e
		}
	}
	return nil
}
