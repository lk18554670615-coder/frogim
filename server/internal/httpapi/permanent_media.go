package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	"github.com/linli/im/server/internal/model"
)

// permanentMediaURL is a stable, directly accessible capability URL. It has no
// expiry and reveals no object-store location or credential. Anyone holding the
// complete URL can read the media, while the HMAC prevents ID enumeration.
func (x *API) permanentMediaURL(mediaID string, cover bool) string {
	mediaID = strings.TrimSpace(mediaID)
	if mediaID == "" {
		return ""
	}
	kind := "content"
	if cover {
		kind = "cover"
	}
	signature := x.permanentMediaSignature(mediaID, kind)
	return "/v2/media-public/" + url.PathEscape(mediaID) + "/" + signature + "/" + kind
}

func (x *API) permanentMediaSignature(mediaID, kind string) string {
	mac := hmac.New(sha256.New, []byte(x.cfg.JWTSecret))
	_, _ = mac.Write([]byte("media-public:v1:" + mediaID + ":" + kind))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (x *API) setAdminAvatarURL(user *model.User) {
	if user == nil {
		return
	}
	mediaID := strings.TrimSpace(user.AvatarMediaID)
	if mediaID == "" {
		mediaID = avatarMediaIDFromPath(user.AvatarURL)
	}
	if mediaID != "" {
		user.AvatarURL = x.permanentMediaURL(mediaID, false)
	}
}

// permanentLocalMediaValue preserves external URLs while converting media
// references owned by this service into stable, directly accessible URLs.
func (x *API) permanentLocalMediaValue(value string) string {
	if mediaID := avatarMediaIDFromPath(strings.TrimSpace(value)); mediaID != "" {
		return x.permanentMediaURL(mediaID, false)
	}
	return value
}

func (x *API) permanentMediaContent(w http.ResponseWriter, r *http.Request) {
	mediaID := strings.TrimSpace(r.PathValue("id"))
	kind := strings.TrimSpace(r.PathValue("kind"))
	if mediaID == "" || (kind != "content" && kind != "cover") {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "media is unavailable")
		return
	}
	expected := []byte(x.permanentMediaSignature(mediaID, kind))
	provided := []byte(strings.TrimSpace(r.PathValue("signature")))
	if len(expected) != len(provided) || subtle.ConstantTimeCompare(expected, provided) != 1 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "media is unavailable")
		return
	}
	if kind == "cover" {
		parent, err := x.app.GetMedia(mediaID)
		if err != nil || parent.CoverMediaID == "" {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "cover is unavailable")
			return
		}
		mediaID = parent.CoverMediaID
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	x.serveMediaContent(w, r, mediaID)
}
