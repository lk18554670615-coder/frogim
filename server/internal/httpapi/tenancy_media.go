package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/linli/im/server/internal/media"
)

const enterpriseAdminMediaCookie = "im_enterprise_admin_media"

// Enterprise reads stay behind the revocable API session. Do not hand out
// object-store GET capabilities that survive a change of enterprise.
func (x *API) downloadMediaURL(ctx context.Context, id string) (string, error) {
	if x.enterprise == nil {
		return x.media.DownloadURL(ctx, id)
	}
	m, err := x.app.GetMedia(id)
	if err != nil {
		return "", err
	}
	if m.Status != "ready" {
		return "", media.ErrForbidden
	}
	return strings.TrimRight(x.enterprise.PublicAPIBase(), "/") + "/v2/media/" + url.PathEscape(id) + "/content", nil
}

func scopedMediaCookiePath(base, route string) string {
	u, err := url.Parse(base)
	if err != nil {
		return route
	}
	return strings.TrimRight(u.Path, "/") + route
}

func (x *API) setEnterpriseAdminMediaCookie(w http.ResponseWriter, r *http.Request, token string) {
	if x.enterprise == nil {
		return
	}
	secure := r.TLS != nil || (x.cfg.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
	http.SetCookie(w, &http.Cookie{Name: enterpriseAdminMediaCookie, Value: token, Path: scopedMediaCookiePath(x.enterprise.PublicAPIBase(), "/v2/media-public/"), HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 900})
}
