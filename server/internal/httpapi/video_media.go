package httpapi

import (
	"context"
	"time"
)

// Call only after authorizing the parent media (or the admin history read).
// Never trust a cover ID or URL supplied in the message payload.
func (x *API) enrichVideoCover(ctx context.Context, mediaID string, body map[string]any) {
	delete(body, "cover")
	delete(body, "coverMediaId")
	delete(body, "coverLocalPath")
	m, err := x.app.GetMedia(mediaID)
	if err != nil || m.CoverMediaID == "" {
		return
	}
	body["coverMediaId"] = m.CoverMediaID
	if url, err := x.media.DownloadURL(ctx, m.CoverMediaID); err == nil {
		body["cover"] = url
	}
}

func (x *API) mediaURLResult(ctx context.Context, mediaID, url string) map[string]any {
	body := map[string]any{"mediaId": mediaID, "url": url, "expiresAt": time.Now().Add(15 * time.Minute).UTC()}
	x.enrichVideoCover(ctx, mediaID, body)
	return body
}
