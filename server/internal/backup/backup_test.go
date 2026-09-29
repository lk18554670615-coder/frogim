package backup

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealedStreamIntegrity(t *testing.T) {
	key := bytes.Repeat([]byte{19}, 32)
	for _, size := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 29, 3 * chunkSize} {
		t.Run(stringSize(size), func(t *testing.T) {
			plain := bytes.Repeat([]byte{0xA9}, size)
			var sealed, other, result bytes.Buffer
			if Encrypt(&sealed, bytes.NewReader(plain), key) != nil || Encrypt(&other, bytes.NewReader(plain), key) != nil || bytes.Equal(sealed.Bytes(), other.Bytes()) {
				t.Fatal("encryption must use a fresh file salt")
			}
			if Decrypt(&result, bytes.NewReader(sealed.Bytes()), key) != nil || !bytes.Equal(result.Bytes(), plain) {
				t.Fatal("round trip")
			}
			for _, offset := range []int{0, 10, len(magic) + 32, len(sealed.Bytes()) - 1} {
				tampered := bytes.Clone(sealed.Bytes())
				tampered[offset] ^= 1
				if Decrypt(io.Discard, bytes.NewReader(tampered), key) == nil {
					t.Fatal("tamper accepted", offset)
				}
			}
			for _, length := range []int{0, len(magic) + 32, sealed.Len() - 20, sealed.Len() - 1} {
				if length >= 0 && Decrypt(io.Discard, bytes.NewReader(sealed.Bytes()[:length]), key) == nil {
					t.Fatal("truncation accepted", length)
				}
			}
			if Decrypt(io.Discard, bytes.NewReader(append(bytes.Clone(sealed.Bytes()), 0)), key) == nil {
				t.Fatal("trailing data")
			}
			if Decrypt(io.Discard, bytes.NewReader(sealed.Bytes()), bytes.Repeat([]byte{20}, 32)) == nil {
				t.Fatal("wrong key")
			}
		})
	}
	var b bytes.Buffer
	if Encrypt(&b, bytes.NewReader(make([]byte, 2*chunkSize)), key) != nil {
		t.Fatal("encrypt")
	}
	v := b.Bytes()
	start := len(magic) + 32
	n := 4 + int(binary.BigEndian.Uint32(v[start:]))
	second := start + n
	copyOne := bytes.Clone(v[start:second])
	copy(v[start:second], v[second:second+n])
	copy(v[second:second+n], copyOne)
	if Decrypt(io.Discard, bytes.NewReader(v), key) == nil {
		t.Fatal("reordered chunks")
	}
	if Encrypt(shortWriter{}, strings.NewReader("test"), key) == nil {
		t.Fatal("short write")
	}
}
func stringSize(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	v := ""
	for n > 0 {
		v = string(digits[n%10]) + v
		n /= 10
	}
	return v
}

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }

func TestBackupSetBindingAndCompletion(t *testing.T) {
	key := bytes.Repeat([]byte{21}, 32)
	binding := Binding{TenantID: "alpha", ServerID: "host-alpha", ReleaseID: "one", ReleaseDigest: strings.Repeat("a", 64), Generation: 1, AccessVersion: 2, SchemaVersion: 79}
	path := filepath.Join(t.TempDir(), "backup")
	w, e := Create(path, binding, key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Create(path, binding, key); e == nil {
		t.Fatal("overwrote existing set")
	}
	if w.Add("secret", func(out io.Writer) error { _, e := io.WriteString(out, "private business data"); return e }) != nil {
		t.Fatal("write")
	}
	if _, e = Open(path, binding, key); e == nil {
		t.Fatal("unfinished set")
	}
	if w.Finalize() != nil {
		t.Fatal("finalize")
	}
	r, e := Open(path, binding, key)
	if e != nil {
		t.Fatal(e)
	}
	var actual bytes.Buffer
	if r.Read("secret", &actual) != nil || actual.String() != "private business data" {
		t.Fatal("read")
	}
	r.Close()
	wrong := binding
	wrong.TenantID = "other"
	if _, e = Open(path, wrong, key); e == nil {
		t.Fatal("foreign archive")
	}
	wrong = binding
	wrong.Generation++
	if _, e = Open(path, wrong, key); e == nil {
		t.Fatal("generation rollback")
	}
	if w.Finalize() == nil || w.Add("next", func(io.Writer) error { return nil }) == nil {
		t.Fatal("finalized writer mutable")
	}
	file := filepath.Join(path, "secret.sealed")
	data, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	data[len(data)-1] ^= 1
	if os.WriteFile(file, data, 0600) != nil {
		t.Fatal("tamper fixture")
	}
	if _, e = Open(path, binding, key); e == nil {
		t.Fatal("file tag failure accepted")
	}
	failedPath := filepath.Join(t.TempDir(), "failed")
	failed, e := Create(failedPath, binding, key)
	if e != nil {
		t.Fatal(e)
	}
	if failed.Add("database", func(out io.Writer) error { _, _ = out.Write([]byte("partial")); return errors.New("producer failed") }) == nil || failed.Finalize() == nil {
		t.Fatal("failed producer confirmed")
	}
}

func TestPlatformArchiveCannotMasqueradeAsEnterprise(t *testing.T) {
	b := Binding{Scope: "platform", DirectoryID: "9a161c47-a687-415d-94ab-908cbd52b7a3", BackupID: "nightly-one", ReleaseID: "platform-one", ReleaseDigest: strings.Repeat("a", 64), SchemaVersion: 17}
	if !b.Valid() {
		t.Fatal("valid platform binding rejected")
	}
	key := bytes.Repeat([]byte{23}, 32)
	path := filepath.Join(t.TempDir(), "set")
	w, e := Create(path, b, key)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	if w.Add("database", func(out io.Writer) error { _, e := out.Write([]byte("fixture")); return e }) != nil || w.Finalize() != nil {
		t.Fatal("write")
	}
	for _, change := range []func(*Binding){
		func(b *Binding) { b.Scope = "" }, func(b *Binding) { b.TenantID = "platform" }, func(b *Binding) { b.ServerID = "platform" },
		func(b *Binding) { b.AccessVersion = 1 }, func(b *Binding) { b.Generation = 1 }, func(b *Binding) { b.DirectoryID = "../../other" },
		func(b *Binding) { b.BackupID = "../../other" }, func(b *Binding) { b.SchemaVersion = 0 },
	} {
		wrong := b
		change(&wrong)
		if wrong.Valid() {
			t.Fatal("ambiguous binding", wrong)
		}
		if reader, e := Open(path, wrong, key); e == nil {
			reader.Close()
			t.Fatal("cross-scope open")
		}
	}
	wrong := b
	wrong.DirectoryID = "8a161c47-a687-415d-94ab-908cbd52b7a3"
	if reader, e := Open(path, wrong, key); e == nil {
		reader.Close()
		t.Fatal("other platform archive accepted")
	}
}
func tarFixture(t *testing.T, headers ...tar.Header) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, h := range headers {
		if e := w.WriteHeader(&h); e != nil {
			t.Fatal(e)
		}
		if h.Size > 0 {
			_, _ = w.Write(make([]byte, h.Size))
		}
	}
	if w.Close() != nil {
		t.Fatal("tar close")
	}
	return b.Bytes()
}
func TestVolumeArchiveRejectsAmbiguousOrUnsafeEntries(t *testing.T) {
	root := tar.Header{Name: ".", Typeflag: tar.TypeDir, Mode: 0700}
	valid := tarFixture(t, root, tar.Header{Name: "data", Typeflag: tar.TypeDir, Mode: 0700}, tar.Header{Name: "data/file", Typeflag: tar.TypeReg, Mode: 0600, Size: 37, Uid: 10001, Gid: 10001})
	if ReadVolume(bytes.NewReader(valid), "") != nil {
		t.Fatal("valid archive rejected")
	}
	for _, h := range []tar.Header{
		{Name: "../escape", Typeflag: tar.TypeReg}, {Name: "/escape", Typeflag: tar.TypeReg}, {Name: `dir\escape`, Typeflag: tar.TypeReg}, {Name: "C:escape", Typeflag: tar.TypeReg},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/outside"}, {Name: "link", Typeflag: tar.TypeLink, Linkname: "file"}, {Name: "fifo", Typeflag: tar.TypeFifo},
		{Name: "setid", Typeflag: tar.TypeReg, Mode: 04755}, {Name: "missing/file", Typeflag: tar.TypeReg}, {Name: ".", Typeflag: tar.TypeDir}, {Name: "xattrs", Typeflag: tar.TypeReg, Xattrs: map[string]string{"security.capability": "unsafe"}},
	} {
		t.Run(h.Name, func(t *testing.T) {
			if ReadVolume(bytes.NewReader(tarFixture(t, root, h)), "") == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	badParent := tarFixture(t, root, tar.Header{Name: "file", Typeflag: tar.TypeReg}, tar.Header{Name: "file/child", Typeflag: tar.TypeReg})
	if ReadVolume(bytes.NewReader(badParent), "") == nil {
		t.Fatal("non-directory parent")
	}
	for _, n := range []int{512, 1024, len(valid) - 1024, len(valid) - 1} {
		if ReadVolume(bytes.NewReader(valid[:n]), "") == nil {
			t.Fatal("missing footer accepted", n)
		}
	}
	if ReadVolume(bytes.NewReader(append(bytes.Clone(valid), 0)), "") == nil {
		t.Fatal("trailing archive accepted")
	}
	dest := t.TempDir()
	if os.WriteFile(filepath.Join(dest, "existing"), []byte("keep"), 0600) != nil {
		t.Fatal("fixture")
	}
	if ReadVolume(bytes.NewReader(valid), dest) == nil {
		t.Fatal("nonempty destination accepted")
	}
}
func TestVolumeExportIsReadable(t *testing.T) {
	root := t.TempDir()
	if os.Mkdir(filepath.Join(root, "dir"), 0700) != nil || os.WriteFile(filepath.Join(root, "dir", "data"), []byte("binary\x00data"), 0600) != nil {
		t.Fatal("fixture")
	}
	var b bytes.Buffer
	if ExportVolume(root, &b) != nil || ReadVolume(&b, "") != nil {
		t.Fatal("export validation")
	}
}
