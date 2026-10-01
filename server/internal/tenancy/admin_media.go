package tenancy

import (
	"github.com/golang-jwt/jwt/v5"
	"io"
	"net/http"
	"time"
)

func (p *Platform) adminLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "lp_admin", Path: "/platform/", MaxAge: -1, HttpOnly: true, Secure: !p.cfg.DevMode, SameSite: http.SameSiteStrictMode})
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

// This is an administrator-only profile preview, never a client business/media gateway.
func (p *Platform) adminAvatar(w http.ResponseWriter, r *http.Request) {
	uid, tid := r.PathValue("id"), r.PathValue("tenant")
	var media string
	if p.DB.QueryRow(r.Context(), `SELECT profile->>'avatarMediaId' FROM lp_memberships WHERE user_id=$1 AND tenant_id=$2`, uid, tid).Scan(&media) != nil || media == "" {
		fail(w, 404, "AVATAR_UNAVAILABLE")
		return
	}
	t, err := p.tenant(r.Context(), tid)
	if err != nil {
		fail(w, 404, "NOT_FOUND")
		return
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{"typ": "avatar-copy", "sub": uid, "source": tid, "aud": "platform", "media": media, "exp": time.Now().Add(time.Minute).Unix()}).SignedString(p.signingKey())
	if err != nil {
		fail(w, 503, "PREVIEW_UNAVAILABLE")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), "GET", t.ControlURL+"/internal/directory/avatar/"+uid, nil)
	if err != nil {
		fail(w, 503, "PREVIEW_UNAVAILABLE")
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.client.Do(req)
	if err != nil {
		fail(w, 503, "ENTERPRISE_UNAVAILABLE")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fail(w, 503, "PREVIEW_UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 10<<20))
}
