package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/linli/im/server/internal/privatefile"
)

// Binding is checked against independently selected operator state. An archive
// must never choose its own destination enterprise or restore authority.
type Binding struct {
	// Empty Scope preserves the original enterprise format. Platform archives
	// cannot be accepted as enterprise archives (or vice versa).
	Scope         string `json:"scope,omitempty"`
	DirectoryID   string `json:"directoryId,omitempty"`
	BackupID      string `json:"backupId,omitempty"`
	TenantID      string `json:"tenantId"`
	ServerID      string `json:"serverId"`
	ReleaseID     string `json:"releaseId"`
	ReleaseDigest string `json:"releaseDigest"`
	Generation    int64  `json:"generation"`
	AccessVersion int64  `json:"accessVersion"`
	SchemaVersion int    `json:"schemaVersion"`
}
type File struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Manifest struct {
	Version   int       `json:"version"`
	Binding   Binding   `json:"binding"`
	CreatedAt time.Time `json:"createdAt"`
	Files     []File    `json:"files"`
}

var safeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var directoryID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func (b Binding) valid() bool {
	if b.Scope == "platform" {
		return directoryID.MatchString(b.DirectoryID) && safeName.MatchString(b.BackupID) && safeName.MatchString(b.ReleaseID) && digest.MatchString(b.ReleaseDigest) && b.SchemaVersion > 0 && b.TenantID == "" && b.ServerID == "" && b.Generation == 0 && b.AccessVersion == 0
	}
	if b.Scope != "" || b.DirectoryID != "" || b.BackupID != "" {
		return false
	}
	return safeName.MatchString(b.TenantID) && safeName.MatchString(b.ServerID) && safeName.MatchString(b.ReleaseID) && digest.MatchString(b.ReleaseDigest) && b.Generation > 0 && b.AccessVersion > 0 && b.SchemaVersion >= 73
}

func (b Binding) Valid() bool { return b.valid() }

// Create reserves a NEW directory. Incomplete attempts stay incomplete and are
// never reused/overwritten; only Finalize writes the authenticated manifest.
type Writer struct {
	root         string
	key          []byte
	manifest     Manifest
	failed, done bool
}

func (w *Writer) Close() { w.failed = true; clear(w.key) }

func Create(path string, binding Binding, key []byte) (*Writer, error) {
	if !filepath.IsAbs(path) || !binding.valid() || len(key) != 32 {
		return nil, ErrInvalid
	}
	if e := os.Mkdir(path, 0700); e != nil {
		return nil, ErrInvalid
	}
	return &Writer{root: path, key: bytes.Clone(key), manifest: Manifest{Version: 1, Binding: binding, CreatedAt: time.Now().UTC()}}, nil
}
func (w *Writer) Add(name string, produce func(io.Writer) error) error {
	if w.failed || w.done || !safeName.MatchString(name) || name == "manifest" || len(w.manifest.Files) >= 16 {
		return ErrInvalid
	}
	for _, f := range w.manifest.Files {
		if f.Name == name {
			return ErrInvalid
		}
	}
	w.failed = true // any failure poisons this writer, including a producer failure
	f, e := privatefile.Create(filepath.Join(w.root, name+".sealed"))
	if e != nil {
		return ErrInvalid
	}
	defer f.Close()
	h := sha256.New()
	count := &countWriter{Writer: io.MultiWriter(f, h)}
	r, p := io.Pipe()
	finished := make(chan error, 1)
	go func() { e := produce(p); _ = p.CloseWithError(e); finished <- e }()
	e = Encrypt(count, r, w.key)
	_ = r.CloseWithError(e)
	pe := <-finished
	if e != nil || pe != nil || f.Sync() != nil {
		return ErrInvalid
	}
	w.manifest.Files = append(w.manifest.Files, File{name, hex.EncodeToString(h.Sum(nil)), count.n})
	w.failed = false
	return nil
}
func (w *Writer) Finalize() error {
	if w.failed || w.done || len(w.manifest.Files) == 0 {
		return ErrInvalid
	}
	w.failed = true
	raw, e := json.Marshal(w.manifest)
	if e != nil {
		return ErrInvalid
	}
	f, e := privatefile.Create(filepath.Join(w.root, "manifest.sealed"))
	if e != nil {
		return ErrInvalid
	}
	defer f.Close()
	if Encrypt(f, bytes.NewReader(raw), w.key) != nil || f.Sync() != nil {
		return ErrInvalid
	}
	w.done = true
	clear(w.key)
	return nil
}

type countWriter struct {
	io.Writer
	n int64
}

func (w *countWriter) Write(b []byte) (int, error) {
	n, e := w.Writer.Write(b)
	w.n += int64(n)
	return n, e
}

type Reader struct {
	root          *os.Root
	key           []byte
	manifest      Manifest
	manifestProof File
}

func Open(path string, expected Binding, key []byte) (*Reader, error) {
	if !filepath.IsAbs(path) || !expected.valid() || len(key) != 32 {
		return nil, ErrInvalid
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return nil, ErrInvalid
	}
	r := &Reader{root: root, key: bytes.Clone(key)}
	ok := false
	defer func() {
		if !ok {
			r.Close()
		}
	}()
	f, e := r.openFile("manifest", 1<<20)
	if e != nil {
		return nil, ErrInvalid
	}
	var data bytes.Buffer
	h := sha256.New()
	c := &countReader{Reader: io.TeeReader(io.LimitReader(f, (1<<20)+1), h)}
	e = Decrypt(&data, c, key)
	f.Close()
	if e != nil {
		return nil, ErrInvalid
	}
	r.manifestProof = File{Name: "manifest", SHA256: hex.EncodeToString(h.Sum(nil)), Size: c.n}
	d := json.NewDecoder(&data)
	d.DisallowUnknownFields()
	if d.Decode(&r.manifest) != nil || d.Decode(new(any)) != io.EOF || r.manifest.Version != 1 || r.manifest.Binding != expected || r.manifest.CreatedAt.IsZero() || len(r.manifest.Files) == 0 || len(r.manifest.Files) > 16 {
		return nil, ErrInvalid
	}
	names := map[string]bool{}
	for _, f := range r.manifest.Files {
		if !safeName.MatchString(f.Name) || f.Name == "manifest" || names[f.Name] || !digest.MatchString(f.SHA256) || f.Size <= 0 || f.Size > MaxFileSize+(2<<20) {
			return nil, ErrInvalid
		}
		names[f.Name] = true
		if r.Read(f.Name, io.Discard) != nil {
			return nil, ErrInvalid
		}
	}
	ok = true
	return r, nil
}

// CopyCiphertext exports only an authenticated file, never decrypted data. The
// destination must not publish its completion marker until this returns nil;
// a changed source may have emitted earlier ciphertext before being rejected.
func (r *Reader) CopyCiphertext(name string, dst io.Writer) error {
	entry := r.manifestProof
	if name != "manifest" {
		entry = File{}
		for _, f := range r.manifest.Files {
			if f.Name == name {
				entry = f
				break
			}
		}
	}
	if entry.Name != name || entry.Size <= 0 {
		return ErrInvalid
	}
	f, e := r.openFile(name, entry.Size)
	if e != nil {
		return ErrInvalid
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(dst, h), io.LimitReader(f, entry.Size+1))
	if e != nil || n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
		return ErrInvalid
	}
	return nil
}
func (r *Reader) Close() error  { clear(r.key); return r.root.Close() }
func (r *Reader) Files() []File { return append([]File(nil), r.manifest.Files...) }

// ManifestProof describes the exact ciphertext authenticated by Open, not a
// second filesystem read that could refer to a replaced manifest.
func (r *Reader) ManifestProof() File { return r.manifestProof }
func (r *Reader) openFile(name string, max int64) (*os.File, error) {
	info, e := r.root.Lstat(name + ".sealed")
	if e != nil || !info.Mode().IsRegular() || info.Size() > max {
		return nil, ErrInvalid
	}
	f, e := r.root.Open(name + ".sealed")
	if e != nil {
		return nil, ErrInvalid
	}
	actual, e := f.Stat()
	if e != nil || !os.SameFile(info, actual) {
		f.Close()
		return nil, ErrInvalid
	}
	return f, nil
}

// Read re-authenticates on every use. Callers write only to unadopted staging
// destinations: earlier chunks can have been emitted when a later tag fails.
func (r *Reader) Read(name string, dst io.Writer) error {
	var entry File
	for _, f := range r.manifest.Files {
		if f.Name == name {
			entry = f
			break
		}
	}
	if entry.Name == "" {
		return ErrInvalid
	}
	f, e := r.openFile(name, entry.Size)
	if e != nil {
		return ErrInvalid
	}
	defer f.Close()
	h := sha256.New()
	c := &countReader{Reader: io.TeeReader(io.LimitReader(f, entry.Size+1), h)}
	if Decrypt(dst, c, r.key) != nil || c.n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
		return ErrInvalid
	}
	return nil
}
