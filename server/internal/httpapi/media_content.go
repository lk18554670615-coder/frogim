package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/linli/im/server/internal/auth"
	"github.com/linli/im/server/internal/media"
	"github.com/linli/im/server/internal/store"
)

const mediaCookieName = "im_media_session"

// Web video elements cannot attach Authorization headers. This HttpOnly cookie
// is accepted ONLY by the media content route, never by ordinary business APIs.
func (x *API) addMediaSession(w http.ResponseWriter, r *http.Request, response map[string]any, claims *auth.Claims) {
	token, err := x.auth.IssueMediaSession(claims.Subject, claims.SessionID, claims.DeviceKind)
	if err != nil {
		return
	}
	response["mediaAccessToken"] = token
	if r.Header.Get("X-Client-Platform") == "web" {
		x.setMediaCookie(w, r, token, int(x.cfg.RefreshTTL.Seconds()))
	}
}

func (x *API) setMediaCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	secure := r.TLS != nil || (x.cfg.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteNoneMode
	}
	http.SetCookie(w, &http.Cookie{Name: mediaCookieName, Value: token, Path: "/v2/media/", HttpOnly: true, Secure: secure, SameSite: sameSite, MaxAge: maxAge})
}

func (x *API) mediaSession(w http.ResponseWriter, r *http.Request) {
	claims, err := x.parseRequestClaims(r)
	if err != nil {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	response := map[string]any{}
	x.addMediaSession(w, r, response, claims)
	w.Header().Set("Cache-Control", "no-store")
	write(w, 200, response)
}

func (x *API) mediaContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-cache, must-revalidate")
	w.Header().Add("Vary", "Cookie")
	w.Header().Add("Vary", "Authorization")
	// Do not fall back to cookies after an invalid explicit credential.
	var claims *auth.Claims
	var err error
	if raw := r.Header.Get("Authorization"); strings.HasPrefix(raw, "Media ") {
		claims, err = x.auth.ParseClaims(strings.TrimPrefix(raw, "Media "), "media")
	} else if raw != "" {
		claims, err = x.parseRequestClaims(r)
	} else if cookie, e := r.Cookie(mediaCookieName); e == nil {
		if origin := r.Header.Get("Origin"); origin != "" && !x.originAllowed(origin) {
			writeError(w, 403, "FORBIDDEN_ORIGIN", "origin is not allowed")
			return
		}
		claims, err = x.auth.ParseClaims(cookie.Value, "media")
	}
	if err != nil || claims == nil {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	// A stale cookie from a different account must never render that account's
	// media in the current page, including a late response during account switch.
	if viewer := r.URL.Query().Get("viewer"); viewer != "" && viewer != claims.Subject {
		writeError(w, 401, "UNAUTHENTICATED", "media session changed")
		return
	}
	user, err := x.app.UserContext(r.Context(), claims.Subject)
	if err != nil || user.Banned || user.DeletedAt != nil {
		writeError(w, 403, "FORBIDDEN", "account unavailable")
		return
	}
	active, err := x.deviceSessionActive(claims)
	if err != nil {
		handleErr(w, err)
		return
	}
	if !active {
		writeError(w, 401, "SESSION_REPLACED", "login session expired")
		return
	}
	allowed, err := x.app.CanAccessMedia(claims.Subject, r.PathValue("id"))
	if err != nil || !allowed {
		writeError(w, 404, "NOT_FOUND", "media is unavailable")
		return
	}
	mediaID := r.PathValue("id")
	if strings.HasSuffix(r.URL.Path, "/cover") {
		parent, e := x.app.GetMedia(mediaID)
		if e != nil || parent.CoverMediaID == "" {
			writeError(w, 404, "NOT_FOUND", "cover is unavailable")
			return
		}
		mediaID = parent.CoverMediaID
	}
	service, ok := x.media.(interface {
		OpenContent(context.Context, string) (media.Content, error)
	})
	if !ok {
		writeError(w, 503, "MEDIA_UNAVAILABLE", "media storage unavailable")
		return
	}
	content, err := service.OpenContent(r.Context(), mediaID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, media.ErrForbidden) {
			writeError(w, 404, "NOT_FOUND", "media is unavailable")
			return
		}
		writeError(w, 503, "MEDIA_UNAVAILABLE", "media content unavailable")
		return
	}
	defer content.Reader.Close()
	w.Header().Set("Content-Type", content.MIME)
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	disposition := "attachment"
	if strings.HasPrefix(content.MIME, "video/") || strings.HasPrefix(content.MIME, "audio/") ||
		content.MIME == "image/jpeg" || content.MIME == "image/png" || content.MIME == "image/gif" || content.MIME == "image/webp" {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", disposition)
	if content.ETag != "" {
		w.Header().Set("ETag", `"`+strings.ReplaceAll(content.ETag, `"`, "")+`"`)
	}
	// ServeContent supports HEAD, byte ranges (206/416), If-Range and conditional
	// requests without buffering the entire video in Go memory.
	stream := &mediaStreamWriter{ResponseWriter: w}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute))
	http.ServeContent(stream, r, "", content.Modified.Truncate(time.Second), content.Reader)
}

// The ordinary API has a 30-second total write timeout. Large video downloads
// instead get a rolling idle timeout: active transfers are not cut off at 30s,
// and a client which stops reading cannot retain a stream forever.
type mediaStreamWriter struct{ http.ResponseWriter }

func (w *mediaStreamWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *mediaStreamWriter) Write(p []byte) (int, error) {
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(time.Minute))
	return w.ResponseWriter.Write(p)
}
