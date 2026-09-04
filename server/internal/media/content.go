package media

import (
	"context"
	"io"
	"time"

	"github.com/linli/im/server/internal/store"
	"github.com/minio/minio-go/v7"
)

// Content is streamed from the private bucket; no public or signed storage URL
// is returned. Closing Reader also releases the outstanding storage request.
type Content struct {
	Reader     io.ReadSeekCloser
	MIME, ETag string
	Modified   time.Time
}

func (s *Service) OpenContent(ctx context.Context, id string) (Content, error) {
	m, err := s.metadata.GetMedia(id)
	if err != nil {
		return Content{}, err
	}
	if m.Status != "ready" {
		return Content{}, ErrForbidden
	}
	if err = s.ensure(ctx); err != nil {
		return Content{}, err
	}
	object, err := s.client.GetObject(ctx, s.bucket, m.ObjectKey, minio.GetObjectOptions{})
	if err != nil {
		return Content{}, err
	}
	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		if code := minio.ToErrorResponse(err).Code; code == "NoSuchKey" || code == "NoSuchObject" {
			return Content{}, store.ErrNotFound
		}
		return Content{}, err
	}
	return Content{Reader: object, MIME: m.MIME, ETag: info.ETag, Modified: info.LastModified}, nil
}
