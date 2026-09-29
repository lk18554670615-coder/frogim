package directorybackup

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

type Request struct {
	Expected backup.Binding `json:"expected"`
	Actor    string         `json:"actor"`
	Reason   string         `json:"reason"`
}

func (r Request) valid() bool {
	return r.Expected.Valid() && r.Expected.Scope == "platform" && r.Expected.SchemaVersion >= 17 && r.Expected.SchemaVersion <= tenancy.PlatformSchemaVersion && len(strings.TrimSpace(r.Actor)) > 0 && len(r.Actor) <= 100 && len(strings.TrimSpace(r.Reason)) >= 3 && len(r.Reason) <= 500
}

// Bundle contains the exact private rendered runtime configuration and its
// public release manifest. Both are encrypted; neither is executed on restore.
type Bundle struct{ Compose, Release []byte }

func (b Bundle) validate(expected backup.Binding, key []byte) error {
	if len(b.Compose) == 0 || len(b.Compose) > 1<<20 || len(b.Release) == 0 || len(b.Release) > 1<<16 || len(key) != 32 {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(b.Release))
	d.DisallowUnknownFields()
	var release deployment.PlatformRelease
	if d.Decode(&release) != nil || d.Decode(new(any)) != io.EOF || release.ID != expected.ReleaseID || release.SchemaVersion != expected.SchemaVersion || release.ComposeSHA256 != expected.ReleaseDigest || release.Runtime != "linux/amd64" {
		return ErrInvalid
	}
	digest := sha256.Sum256(b.Compose)
	if hex.EncodeToString(digest[:]) != expected.ReleaseDigest || !json.Valid(b.Compose) {
		return ErrInvalid
	}
	// Config holds authentication/supplier credentials: the backup key must be
	// independently generated, not a runtime secret copied out of this bundle.
	for _, encoded := range []string{base64.RawURLEncoding.EncodeToString(key), base64.StdEncoding.EncodeToString(key), hex.EncodeToString(key)} {
		if bytes.Contains(b.Compose, []byte(encoded)) {
			return ErrInvalid
		}
	}
	return nil
}
func openArchive(path string, r Request, key []byte) (*backup.Reader, error) {
	if !r.valid() {
		return nil, ErrInvalid
	}
	reader, err := backup.Open(path, r.Expected, key)
	if err != nil {
		return nil, ErrInvalid
	}
	fail := func() (*backup.Reader, error) { reader.Close(); return nil, ErrInvalid }
	files := reader.Files()
	if len(files) != 3 || files[0].Name != "compose" || files[1].Name != "release" || files[2].Name != "database" {
		return fail()
	}
	compose, release := &limitedBuffer{limit: 1 << 20}, &limitedBuffer{limit: 1 << 16}
	if reader.Read("compose", compose) != nil || reader.Read("release", release) != nil || (Bundle{compose.Bytes(), release.Bytes()}).validate(r.Expected, key) != nil {
		return fail()
	}
	return reader, nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, ErrInvalid
	}
	return b.Buffer.Write(p)
}

// Verify authenticates every file but never returns configuration or account
// data. A digest of ciphertext alone would not prove decryptability.
func Verify(path string, r Request, key []byte) (backup.File, error) {
	reader, e := openArchive(path, r, key)
	if e != nil {
		return backup.File{}, e
	}
	defer reader.Close()
	return reader.ManifestProof(), nil
}
