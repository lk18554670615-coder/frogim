package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/linli/im/server/internal/store"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var (
	ErrUnavailable = errors.New("media storage is not configured")
	ErrInvalid     = errors.New("invalid media request")
	ErrForbidden   = errors.New("forbidden")
)

type clientPlatformKey struct{}

// WithClientPlatform records a validated client class for selecting a
// preconfigured development signer. It never accepts an endpoint from the
// request, and unknown values deliberately fall back to the normal public
// endpoint.
func WithClientPlatform(ctx context.Context, platform string) context.Context {
	platform = strings.ToLower(strings.TrimSpace(platform))
	switch platform {
	case "android", "ios", "web", "macos":
		return context.WithValue(ctx, clientPlatformKey{}, platform)
	default:
		return context.WithValue(ctx, clientPlatformKey{}, "")
	}
}

type Metadata interface {
	CreateMedia(store.Media) error
	CompleteMedia(string, string, int64, string) error
	GetMedia(string) (store.Media, error)
}
type CleanupMetadata interface {
	LeaseMediaCleanup(context.Context, time.Time, time.Duration, time.Duration, time.Duration, int) ([]store.MediaCleanupItem, error)
	CompleteMediaCleanup(context.Context, string, error, time.Time) error
}
type Service struct {
	client        *minio.Client
	signer        *minio.Client
	androidSigner *minio.Client
	bucket        string
	metadata      Metadata
	maxBytes      int64
	mu            sync.Mutex
	ready         bool
}
type Prepared struct {
	MediaID   string            `json:"mediaId"`
	ObjectKey string            `json:"objectKey"`
	UploadURL string            `json:"uploadUrl"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

func New(endpoint, publicEndpoint, androidPublicEndpoint, access, secret, bucket, region string, secure, publicSecure bool, maxBytes int64, m Metadata) (*Service, error) {
	s := &Service{bucket: bucket, metadata: m, maxBytes: maxBytes}
	if endpoint == "" {
		return s, nil
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: secure})
	if err != nil {
		return nil, err
	}
	s.client = client
	s.signer = client
	if publicEndpoint != "" && publicEndpoint != endpoint {
		signer, signerErr := minio.New(publicEndpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: publicSecure, Region: region})
		if signerErr != nil {
			return nil, signerErr
		}
		s.signer = signer
	}
	if androidPublicEndpoint != "" {
		signer, signerErr := minio.New(androidPublicEndpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: publicSecure, Region: region})
		if signerErr != nil {
			return nil, signerErr
		}
		s.androidSigner = signer
	}
	return s, nil
}

func (s *Service) signerFor(ctx context.Context) *minio.Client {
	platform, _ := ctx.Value(clientPlatformKey{}).(string)
	if platform == "android" && s.androidSigner != nil {
		return s.androidSigner
	}
	return s.signer
}
func token() string { var b [12]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func allowed(mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	for _, prefix := range []string{"image/", "audio/", "video/"} {
		if strings.HasPrefix(mime, prefix) {
			return true
		}
	}
	_, ok := allowedDocumentMIMEs[mime]
	return ok
}

var allowedDocumentMIMEs = map[string]struct{}{
	"application/pdf":               {},
	"application/octet-stream":      {},
	"text/plain":                    {},
	"text/csv":                      {},
	"application/rtf":               {},
	"application/zip":               {},
	"application/x-zip-compressed":  {},
	"application/msword":            {},
	"application/vnd.ms-excel":      {},
	"application/vnd.ms-powerpoint": {},
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   {},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         {},
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": {},
}

var zipDocumentMIMEs = map[string]struct{}{
	"application/zip":              {},
	"application/x-zip-compressed": {},
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   {},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         {},
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": {},
}

var opaqueDocumentMIMEs = map[string]struct{}{
	"application/msword":            {},
	"application/vnd.ms-excel":      {},
	"application/vnd.ms-powerpoint": {},
	"application/rtf":               {},
}

func (s *Service) ensure(ctx context.Context) error {
	if s.client == nil {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready {
		return nil
	}
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !exists {
		if err = s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
			return err
		}
	}
	s.ready = true
	return nil
}
func (s *Service) Prepare(ctx context.Context, uid, mime, name string, size int64) (Prepared, error) {
	if !allowed(mime) || size <= 0 || size > s.maxBytes {
		return Prepared{}, ErrInvalid
	}
	if err := s.ensure(ctx); err != nil {
		return Prepared{}, err
	}
	mid := "med_" + token()
	ext := strings.ToLower(filepath.Ext(filepath.Base(name)))
	if len(ext) > 10 {
		ext = ""
	}
	key := fmt.Sprintf("users/%s/%s/%s%s", uid, time.Now().Format("2006/01"), mid, ext)
	meta := store.Media{ID: mid, OwnerID: uid, ObjectKey: key, MIME: mime, Size: size, Status: "pending"}
	if err := s.metadata.CreateMedia(meta); err != nil {
		return Prepared{}, err
	}
	expires := 15 * time.Minute
	url, err := s.signerFor(ctx).PresignedPutObject(ctx, s.bucket, key, expires)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{MediaID: mid, ObjectKey: key, UploadURL: url.String(), Method: "PUT", Headers: map[string]string{"Content-Type": mime}, ExpiresAt: time.Now().Add(expires)}, nil
}
func (s *Service) Complete(ctx context.Context, uid, id, checksum string) (store.Media, error) {
	m, err := s.metadata.GetMedia(id)
	if err != nil {
		return m, err
	}
	if m.OwnerID != uid {
		return m, ErrForbidden
	}
	if m.Status != "pending" {
		return m, nil
	}
	if err = s.ensure(ctx); err != nil {
		return m, err
	}
	stat, err := s.client.StatObject(ctx, s.bucket, m.ObjectKey, minio.StatObjectOptions{})
	if err != nil {
		return m, err
	}
	if stat.Size <= 0 || stat.Size != m.Size {
		return m, ErrInvalid
	}
	if stat.ContentType != "" && stat.ContentType != m.MIME {
		return m, ErrInvalid
	}
	object, err := s.client.GetObject(ctx, s.bucket, m.ObjectKey, minio.GetObjectOptions{})
	if err != nil {
		return m, err
	}
	defer object.Close()
	computedChecksum, err := verifyObjectContent(object, m.MIME, m.Size, checksum)
	if err != nil {
		return m, err
	}
	if err = s.metadata.CompleteMedia(id, uid, stat.Size, computedChecksum); err != nil {
		return m, err
	}
	m.Size = stat.Size
	m.Status = "ready"
	m.Checksum = computedChecksum
	return m, nil
}

func verifyObjectContent(reader io.Reader, declaredMIME string, expectedSize int64, claimedChecksum string) (string, error) {
	claimedChecksum = strings.ToLower(strings.TrimSpace(claimedChecksum))
	decoded, err := hex.DecodeString(claimedChecksum)
	if err != nil || len(decoded) != sha256.Size || expectedSize <= 0 {
		return "", ErrInvalid
	}
	prefixSize := min(expectedSize, 512)
	prefix := make([]byte, prefixSize)
	n, readErr := io.ReadFull(reader, prefix)
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return "", readErr
	}
	prefix = prefix[:n]
	hash := sha256.New()
	_, _ = hash.Write(prefix)
	copied, err := io.Copy(hash, io.LimitReader(reader, expectedSize-int64(n)+1))
	if err != nil {
		return "", err
	}
	if int64(n)+copied != expectedSize || !mimeMatchesContent(declaredMIME, prefix) {
		return "", ErrInvalid
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(actual), []byte(claimedChecksum)) != 1 {
		return "", ErrInvalid
	}
	return actual, nil
}

func mimeMatchesContent(declared string, prefix []byte) bool {
	declared = strings.ToLower(strings.TrimSpace(strings.SplitN(declared, ";", 2)[0]))
	detected := strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(prefix), ";", 2)[0]))
	if len(prefix) >= 12 && bytes.Equal(prefix[4:8], []byte("ftyp")) {
		detected = "video/mp4"
	} else if len(prefix) >= 4 && bytes.Equal(prefix[:4], []byte("OggS")) {
		detected = "application/ogg"
	} else if len(prefix) >= 3 && bytes.Equal(prefix[:3], []byte("ID3")) {
		detected = "audio/mpeg"
	} else if len(prefix) >= 12 && bytes.Equal(prefix[:4], []byte("RIFF")) && bytes.Equal(prefix[8:12], []byte("WAVE")) {
		detected = "audio/wav"
	} else if len(prefix) >= 4 && bytes.Equal(prefix[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) {
		detected = "video/webm"
	}
	switch {
	case strings.HasPrefix(declared, "image/"):
		return detected == declared || (declared == "image/jpg" && detected == "image/jpeg")
	case strings.HasPrefix(declared, "audio/"):
		return strings.HasPrefix(detected, "audio/") || (declared == "audio/ogg" && detected == "application/ogg") || (declared == "audio/mp4" && detected == "video/mp4")
	case strings.HasPrefix(declared, "video/"):
		return strings.HasPrefix(detected, "video/") || (declared == "video/ogg" && detected == "application/ogg")
	case declared == "application/pdf":
		return detected == "application/pdf"
	case declared == "text/plain" || declared == "text/csv":
		return detected == "text/plain"
	case func() bool { _, ok := zipDocumentMIMEs[declared]; return ok }():
		return detected == "application/zip" || detected == "application/x-zip-compressed"
	case func() bool { _, ok := opaqueDocumentMIMEs[declared]; return ok }():
		return detected == "application/octet-stream" || detected == "text/plain"
	case declared == "application/octet-stream":
		return detected != "text/html" && detected != "text/xml"
	default:
		return false
	}
}

func (s *Service) DownloadURL(ctx context.Context, id string) (string, error) {
	m, err := s.metadata.GetMedia(id)
	if err != nil {
		return "", err
	}
	if m.Status != "ready" {
		return "", ErrForbidden
	}
	if err = s.ensure(ctx); err != nil {
		return "", err
	}
	url, err := s.signerFor(ctx).PresignedGetObject(ctx, s.bucket, m.ObjectKey, 15*time.Minute, nil)
	if err != nil {
		return "", err
	}
	return url.String(), nil
}

// CleanupOnce removes abandoned uploads and ready objects that have remained
// unreferenced long enough to avoid racing a normal compose/send flow. The DB
// lease makes this safe across multiple server instances; object deletion is
// idempotent so a crash before metadata deletion is recoverable.
func (s *Service) CleanupOnce(ctx context.Context) (int, error) {
	metadata, ok := s.metadata.(CleanupMetadata)
	if !ok || s.client == nil {
		return 0, nil
	}
	items, err := metadata.LeaseMediaCleanup(ctx, time.Now(), time.Hour, 24*time.Hour, 10*time.Minute, 25)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, item := range items {
		removeErr := s.client.RemoveObject(ctx, s.bucket, item.ObjectKey, minio.RemoveObjectOptions{})
		if err = metadata.CompleteMediaCleanup(ctx, item.ID, removeErr, time.Now()); err != nil {
			return completed, err
		}
		if removeErr == nil {
			completed++
		}
	}
	return completed, nil
}
