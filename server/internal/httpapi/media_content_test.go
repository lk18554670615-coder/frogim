package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/media"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
)

type closeableMediaReader struct{ *strings.Reader }

func (closeableMediaReader) Close() error { return nil }

type contentMediaService struct{ signedMediaService }

func (contentMediaService) OpenContent(_ context.Context, id string) (media.Content, error) {
	return media.Content{Reader: closeableMediaReader{strings.NewReader("0123456789")}, MIME: "video/mp4", ETag: "media-etag", Modified: time.Unix(1700000000, 0)}, nil
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}
func TestMediaStreamUsesRollingDeadlineThroughMiddleware(t *testing.T) {
	recorder := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	stream := &mediaStreamWriter{ResponseWriter: &responseCapture{ResponseWriter: recorder}}
	_, _ = stream.Write([]byte("first"))
	_, _ = stream.Write([]byte("second"))
	if len(recorder.deadlines) != 2 || recorder.deadlines[1].Before(recorder.deadlines[0]) || time.Until(recorder.deadlines[1]) < 50*time.Second {
		t.Fatalf("stream did not renew its write deadline: %v", recorder.deadlines)
	}
	if recorder.Body.String() != "firstsecond" {
		t.Fatal("stream lost bytes")
	}
}

func TestFixedMediaContentSessionRangeAndPermissions(t *testing.T) {
	a, _ := app.New(t.Context(), teststore.Memory{})
	_ = a.SeedDemo()
	for _, m := range []store.Media{
		{ID: "med_cover", OwnerID: "usr_alice", MIME: "image/jpeg", Size: 10, Status: "ready"},
		{ID: "med_video", OwnerID: "usr_alice", MIME: "video/mp4", Size: 10, Status: "pending"},
	} {
		if err := a.CreateMedia(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.CompleteMediaWithCover("med_video", "usr_alice", 10, "sum", "med_cover"); err != nil {
		t.Fatal(err)
	}
	if err := a.BindMediaChannel(store.MediaChannelBinding{MediaID: "med_video", SenderID: "usr_alice", ChannelID: "usr_bob", ChannelType: 1}); err != nil {
		t.Fatal(err)
	}
	x := New(config.Config{JWTSecret: "test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour, AllowedOrigins: []string{"https://app.example.com"}}, a)
	x.media = contentMediaService{}
	ts := httptest.NewServer(x.Handler())
	defer ts.Close()
	alice := loginToken(t, ts.URL, "13800000001")
	bob := loginToken(t, ts.URL, "13800000002")
	outsider := loginToken(t, ts.URL, "13800000000")
	req, _ := http.NewRequest("POST", ts.URL+"/v2/media/session", nil)
	req.Header.Set("Authorization", "Bearer "+alice)
	req.Header.Set("X-Client-Platform", "web")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	_ = json.NewDecoder(res.Body).Decode(&response)
	res.Body.Close()
	if nested, ok := response["data"].(map[string]any); ok {
		response = nested
	}
	credential, _ := response["mediaAccessToken"].(string)
	if credential == "" {
		t.Fatalf("missing media session: %v", response)
	}
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == mediaCookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.Path != "/v2/media/" {
		t.Fatal("missing scoped HttpOnly cookie")
	}
	if _, err := x.auth.ParseClaims(credential, "access"); err == nil {
		t.Fatal("media credential accepted as access JWT")
	}
	if _, err := x.auth.ParseClaims(credential, "refresh"); err == nil {
		t.Fatal("media credential accepted as refresh JWT")
	}

	check := func(name, method, path, authHeader, rangeHeader string, c *http.Cookie, want int, body string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest(method, ts.URL+path, nil)
		if authHeader != "" {
			r.Header.Set("Authorization", authHeader)
		}
		if rangeHeader != "" {
			r.Header.Set("Range", rangeHeader)
		}
		if c != nil {
			r.AddCookie(c)
		}
		got, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		bytes, _ := io.ReadAll(got.Body)
		got.Body.Close()
		if got.StatusCode != want || (body != "" && string(bytes) != body) {
			t.Fatalf("%s: status=%d body=%q", name, got.StatusCode, bytes)
		}
		if got.Header.Get("Location") != "" {
			t.Fatalf("%s: must not redirect to signed URL", name)
		}
		return got
	}
	path := "/v2/media/med_video/content?viewer=usr_alice"
	full := check("full", "GET", path, "Media "+credential, "", nil, 200, "0123456789")
	if full.Header.Get("Accept-Ranges") != "bytes" || full.Header.Get("Content-Type") != "video/mp4" || full.Header.Get("Content-Disposition") != "inline" {
		t.Fatal(full.Header)
	}
	part := check("range", "GET", path, "Media "+credential, "bytes=2-5", nil, 206, "2345")
	if part.Header.Get("Content-Range") != "bytes 2-5/10" {
		t.Fatal(part.Header)
	}
	check("suffix", "GET", path, "Media "+credential, "bytes=-3", nil, 206, "789")
	check("invalid range", "GET", path, "Media "+credential, "bytes=20-30", nil, 416, "")
	check("head", "HEAD", path, "Media "+credential, "", nil, 200, "")
	check("web cookie", "GET", path, "", "", cookie, 200, "0123456789")
	check("no credential", "GET", path, "", "", nil, 401, "")
	check("wrong viewer", "GET", path+"-wrong", "", "", cookie, 401, "")
	check("no invalid bearer fallback", "GET", path, "Bearer invalid", "", cookie, 401, "")
	check("outsider", "GET", "/v2/media/med_video/content", "Bearer "+outsider, "", nil, 404, "")
	check("recipient", "GET", "/v2/media/med_video/content", "Bearer "+bob, "", nil, 200, "0123456789")
	check("cover recipient", "GET", "/v2/media/med_video/cover", "Bearer "+bob, "", nil, 200, "0123456789")
	check("cover outsider", "GET", "/v2/media/med_video/cover", "Bearer "+outsider, "", nil, 404, "")
	check("not a business credential", "GET", "/v2/users/me", "Bearer "+credential, "", nil, 401, "")
	check("business ignores cookie", "GET", "/v2/users/me", "", "", cookie, 401, "")
	claims, _ := x.auth.ParseClaims(credential, "media")
	if err := a.RevokeDeviceRefreshSessions(claims.Subject, claims.DeviceKind); err != nil {
		t.Fatal(err)
	}
	check("revoked media session", "GET", path, "Media "+credential, "", nil, 401, "")
	check("revoked cookie", "GET", path, "", "", cookie, 401, "")
}
