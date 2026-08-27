package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/store"
)

type fakeMetadata struct{}

func (fakeMetadata) CreateMedia(store.Media) error                     { return nil }
func (fakeMetadata) CompleteMedia(string, string, int64, string) error { return nil }
func (fakeMetadata) GetMedia(string) (store.Media, error) {
	return store.Media{}, errors.New("missing")
}

func TestPrepareValidatesBeforeStorage(t *testing.T) {
	s, err := New("", "", "", "", "", "bucket", "us-east-1", false, false, 100<<20, fakeMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Prepare(context.Background(), "u", "text/html", "x.html", 10); err != ErrInvalid {
		t.Fatalf("mime err=%v", err)
	}
	if _, err = s.Prepare(context.Background(), "u", "image/png", "x.png", 101<<20); err != ErrInvalid {
		t.Fatalf("size err=%v", err)
	}
	if _, err = s.Prepare(context.Background(), "u", "image/png", "x.png", 10); err != ErrUnavailable {
		t.Fatalf("storage err=%v", err)
	}
}

func TestSignerSelectionUsesAndroidDevelopmentEndpointOnlyForAndroid(t *testing.T) {
	s, err := New(
		"minio:9000", "127.0.0.1:9000", "10.0.2.2:9000",
		"access", "secret", "bucket", "us-east-1", false, false,
		100<<20, fakeMetadata{},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		platform string
		host     string
	}{
		{platform: "android", host: "10.0.2.2:9000"},
		{platform: "web", host: "127.0.0.1:9000"},
		{platform: "macos", host: "127.0.0.1:9000"},
		{platform: "unknown", host: "127.0.0.1:9000"},
	} {
		got := s.signerFor(WithClientPlatform(context.Background(), test.platform)).EndpointURL().Host
		if got != test.host {
			t.Fatalf("platform=%s host=%s want=%s", test.platform, got, test.host)
		}
	}
}

func TestVerifyObjectContentChecksHashSizeAndMagic(t *testing.T) {
	png := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{0}, 64)...)
	sum := sha256.Sum256(png)
	checksum := hex.EncodeToString(sum[:])
	actual, err := verifyObjectContent(bytes.NewReader(png), "image/png", int64(len(png)), checksum)
	if err != nil || actual != checksum {
		t.Fatalf("valid image checksum=%q err=%v", actual, err)
	}
	if _, err = verifyObjectContent(bytes.NewReader(png), "image/jpeg", int64(len(png)), checksum); err != ErrInvalid {
		t.Fatalf("mismatched magic err=%v", err)
	}
	if _, err = verifyObjectContent(bytes.NewReader(png), "image/png", int64(len(png)), strings.Repeat("0", 64)); err != ErrInvalid {
		t.Fatalf("mismatched checksum err=%v", err)
	}
	html := []byte("<!doctype html><script>alert(1)</script>")
	htmlSum := sha256.Sum256(html)
	if _, err = verifyObjectContent(bytes.NewReader(html), "application/octet-stream", int64(len(html)), hex.EncodeToString(htmlSum[:])); err != ErrInvalid {
		t.Fatalf("active octet-stream content err=%v", err)
	}
}
