package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/linli/im/server/internal/privatefile"
)

var ErrOffsite = errors.New("OFFSITE_BACKUP_UNCONFIRMED")
var errObjectAbsent = errors.New("offsite object absent")
var errObjectExists = errors.New("offsite object exists")

// Conditional single-object writes stay below all supported S3 multipart
// boundaries. Large sealed files use deterministic numbered 32 MiB segments;
// the original authenticated file hash/size covers their concatenation.
const offsiteChunkSize = 32 << 20

type objectStore interface {
	get(context.Context, string) (io.ReadCloser, error)
	putNew(context.Context, string, []byte) error
}
type Offsite struct {
	config  OffsiteConfig
	objects objectStore
}
type Delivery struct {
	Version  int     `json:"version"`
	TargetID string  `json:"targetId"`
	Binding  Binding `json:"binding"`
	Manifest File    `json:"manifest"`
}

func (o *Offsite) receipt(b Binding, p File) (Delivery, error) {
	if o == nil || o.objects == nil || !o.config.matches(b) || p.Name != "manifest" || p.Size < 1 || p.Size > 1<<20 || !digest.MatchString(p.SHA256) {
		return Delivery{}, ErrInvalid
	}
	return Delivery{Version: 1, TargetID: o.config.ID, Binding: b, Manifest: p}, nil
}
func (o *Offsite) root(d Delivery) string { return o.config.objectRoot() + "/" + d.Manifest.SHA256 }
func chunkKey(root, name string, n int) string {
	return fmt.Sprintf("%s/%s/%06d.sealed", root, name, n)
}
func readObject(ctx context.Context, s objectStore, key string, size int64) ([]byte, error) {
	r, e := s.get(ctx, key)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	b, e := io.ReadAll(io.LimitReader(r, size+1))
	if e != nil || int64(len(b)) != size {
		return nil, ErrOffsite
	}
	return b, nil
}

// A missing or lost PUT acknowledgment is resolved by reading exact bytes. An
// existing object is NEVER replaced; a mismatching object requires operator
// investigation, not deletion/re-upload. Remote IAM has no DeleteObject grant.
func putVerified(ctx context.Context, s objectStore, key string, data []byte) error {
	existing, e := readObject(ctx, s, key, int64(len(data)))
	if e == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return ErrOffsite
	}
	if !errors.Is(e, errObjectAbsent) {
		return ErrOffsite
	}
	_ = s.putNew(ctx, key, data)
	if ctx.Err() != nil {
		return ErrOffsite
	}
	// Also resolves conflicts and uncertain network acknowledgments. Never
	// blindly retry an unconditional write after a timeout.
	existing, verifyErr := readObject(ctx, s, key, int64(len(data)))
	if verifyErr != nil || !bytes.Equal(existing, data) {
		return ErrOffsite
	}
	return nil
}

// Deliver authenticates the local archive before any upload and publishes the
// encrypted manifest LAST. Repeating after interruption resumes by verifying
// existing immutable segments; no plaintext, key or runtime configuration is
// exposed to the object store or receipt.
func (o *Offsite) Deliver(ctx context.Context, b Binding, path string, key []byte) (Delivery, error) {
	if o == nil || !o.config.matches(b) || !o.independentKey(key) {
		return Delivery{}, ErrInvalid
	}
	r, e := Open(path, b, key)
	if e != nil {
		return Delivery{}, e
	}
	defer r.Close()
	d, e := o.receipt(b, r.ManifestProof())
	if e != nil {
		return Delivery{}, e
	}
	root := o.root(d)
	for _, f := range r.Files() {
		writer := &segmentWriter{ctx: ctx, objects: o.objects, root: root, name: f.Name}
		if r.CopyCiphertext(f.Name, writer) != nil || writer.finish() != nil {
			return Delivery{}, ErrOffsite
		}
	}
	var manifest bytes.Buffer
	if r.CopyCiphertext("manifest", &manifest) != nil || putVerified(ctx, o.objects, root+"/manifest.sealed", manifest.Bytes()) != nil {
		return Delivery{}, ErrOffsite
	}
	return d, nil
}

type segmentWriter struct {
	ctx        context.Context
	objects    objectStore
	root, name string
	index      int
	data       []byte
}

func (w *segmentWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := min(len(p), offsiteChunkSize-len(w.data))
		w.data = append(w.data, p[:n]...)
		p = p[n:]
		written += n
		if len(w.data) == offsiteChunkSize {
			if e := w.finish(); e != nil {
				return written, e
			}
		}
	}
	return written, nil
}
func (w *segmentWriter) finish() error {
	if len(w.data) == 0 {
		return nil
	}
	if putVerified(w.ctx, w.objects, chunkKey(w.root, w.name, w.index), w.data) != nil {
		return ErrOffsite
	}
	w.data = w.data[:0]
	w.index++
	return nil
}

// Retrieve requires an independently selected binding and a saved delivery
// receipt. It never lists a bucket or lets a remote prefix select the tenant.
// Destination must be NEW (or the exact already complete authenticated set).
// Interrupted downloads remain incomplete and must use a new directory.
func (o *Offsite) Retrieve(ctx context.Context, b Binding, d Delivery, path string, key []byte) error {
	canonical, e := o.receipt(b, d.Manifest)
	if e != nil || d != canonical || !filepath.IsAbs(path) || !o.independentKey(key) {
		return ErrInvalid
	}
	if _, e = os.Lstat(path); e == nil {
		r, e := Open(path, b, key)
		if e != nil {
			return ErrInvalid
		}
		defer r.Close()
		if r.ManifestProof() != d.Manifest {
			return ErrInvalid
		}
		return nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return ErrInvalid
	}
	root := o.root(d)
	cipher, e := readObject(ctx, o.objects, root+"/manifest.sealed", d.Manifest.Size)
	if e != nil {
		return ErrOffsite
	}
	h := sha256.Sum256(cipher)
	if hex.EncodeToString(h[:]) != d.Manifest.SHA256 {
		return ErrInvalid
	}
	var plain bytes.Buffer
	if Decrypt(&plain, bytes.NewReader(cipher), key) != nil {
		return ErrInvalid
	}
	var m Manifest
	decoder := json.NewDecoder(&plain)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil || decoder.Decode(new(any)) != io.EOF || m.Version != 1 || m.Binding != b || m.CreatedAt.IsZero() || len(m.Files) == 0 || len(m.Files) > 16 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if !safeName.MatchString(f.Name) || f.Name == "manifest" || seen[f.Name] || !digest.MatchString(f.SHA256) || f.Size <= 0 || f.Size > MaxFileSize+(2<<20) {
			return ErrInvalid
		}
		seen[f.Name] = true
	}
	if os.Mkdir(path, 0700) != nil {
		return ErrInvalid
	}
	local, e := os.OpenRoot(path)
	if e != nil {
		return ErrInvalid
	}
	defer local.Close()
	r := &Reader{root: local, key: bytes.Clone(key), manifest: m, manifestProof: d.Manifest}
	defer clear(r.key)
	for _, f := range m.Files {
		if e := o.retrieveFile(ctx, root, path, f); e != nil {
			return e
		}
		if r.Read(f.Name, io.Discard) != nil {
			return ErrInvalid
		}
	}
	// All files are authenticated before the final manifest exists locally.
	file, e := privatefile.Create(filepath.Join(path, "manifest.sealed"))
	if e != nil {
		return ErrInvalid
	}
	defer file.Close()
	if n, e := file.Write(cipher); e != nil || n != len(cipher) || file.Sync() != nil {
		return ErrOffsite
	}
	return nil
}
func (o *Offsite) retrieveFile(ctx context.Context, root, path string, f File) error {
	out, e := privatefile.Create(filepath.Join(path, f.Name+".sealed"))
	if e != nil {
		return ErrInvalid
	}
	defer out.Close()
	h := sha256.New()
	left := f.Size
	for n := 0; left > 0; n++ {
		size := min(left, int64(offsiteChunkSize))
		data, e := readObject(ctx, o.objects, chunkKey(root, f.Name, n), size)
		if e != nil {
			return ErrOffsite
		}
		if n, e := io.MultiWriter(out, h).Write(data); e != nil || n != len(data) {
			return ErrOffsite
		}
		left -= size
	}
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 || out.Sync() != nil {
		return ErrInvalid
	}
	return nil
}
