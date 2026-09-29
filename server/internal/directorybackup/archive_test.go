package directorybackup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

func fixture() (Request, Bundle, []byte) {
	compose := []byte(`{"services":{},"private":"fixture-never-a-real-secret"}`)
	hash := sha256.Sum256(compose)
	r := Request{Expected: backup.Binding{Scope: "platform", DirectoryID: "9a161c47-a687-415d-94ab-908cbd52b7a3", BackupID: "backup-one", ReleaseID: "platform-one", ReleaseDigest: hex.EncodeToString(hash[:]), SchemaVersion: tenancy.PlatformSchemaVersion}, Actor: "test-operator", Reason: "isolated recovery test"}
	release, _ := json.Marshal(deployment.PlatformRelease{ID: r.Expected.ReleaseID, Runtime: "linux/amd64", SchemaVersion: r.Expected.SchemaVersion, ComposeSHA256: r.Expected.ReleaseDigest})
	return r, Bundle{compose, release}, bytes.Repeat([]byte{31}, 32)
}
func TestConnectionConfigRejectsHiddenOverrides(t *testing.T) {
	base := "postgres://fixture:test-password@127.0.0.1:15473/fixture?sslmode=disable"
	if _, _, e := connectionConfig(base); e != nil {
		t.Fatal(e)
	}
	for _, dsn := range []string{
		base + "&search_path=foreign", base + "&host=other", base + "&service=other", base + "&options=ignored", base + "&sslmode=disable",
		strings.Replace(base, "sslmode=disable", "sslmode=prefer", 1), strings.Replace(base, "sslmode=disable", "sslmode=verify-full", 1),
		strings.Replace(base, "/fixture?", "/dbname%3Dother?", 1), strings.Replace(base, "/fixture?", "/foo/bar?", 1), strings.Replace(base, "test-password@", "@", 1),
	} {
		if _, _, e := connectionConfig(dsn); e == nil {
			t.Fatal("unsafe DSN accepted")
		}
	}
}
func TestArchiveRequiresAllComponentsAndExactRelease(t *testing.T) {
	r, b, key := fixture()
	if b.validate(r.Expected, key) != nil {
		t.Fatal("bundle")
	}
	for _, mutation := range []func(*Request){func(r *Request) { r.Expected.SchemaVersion++ }, func(r *Request) { r.Reason = "" }, func(r *Request) { r.Expected.Scope = "" }} {
		copy := r
		mutation(&copy)
		if copy.valid() {
			t.Fatal("invalid request accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "archive")
	w, e := backup.Create(path, r.Expected, key)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	for _, file := range []struct {
		name string
		data []byte
	}{{"compose", b.Compose}, {"release", b.Release}, {"database", []byte("fixture")}} {
		if e := w.Add(file.name, func(out io.Writer) error { _, e := out.Write(file.data); return e }); e != nil {
			t.Fatal(e)
		}
	}
	if w.Finalize() != nil {
		t.Fatal("finalize")
	}
	if proof, e := Verify(path, r, key); e != nil || proof.Size == 0 {
		t.Fatal("verify", e)
	}
	wrong := r
	wrong.Expected.ReleaseDigest = strings.Repeat("f", 64)
	if _, e := Verify(path, wrong, key); e == nil {
		t.Fatal("wrong release")
	}
	if _, e := Verify(path, r, bytes.Repeat([]byte{32}, 32)); e == nil {
		t.Fatal("wrong key")
	}
	b.Compose = append(b.Compose, ' ')
	if b.validate(r.Expected, key) == nil {
		t.Fatal("config changed without matching digest")
	}
}

func TestPreviousPlatformSchemaArchiveRemainsVerifiable(t *testing.T) {
	r, b, key := fixture()
	r.Expected.SchemaVersion = 17
	b.Release, _ = json.Marshal(deployment.PlatformRelease{ID: r.Expected.ReleaseID, Runtime: "linux/amd64", SchemaVersion: 17, ComposeSHA256: r.Expected.ReleaseDigest})
	path := filepath.Join(t.TempDir(), "old-platform")
	w, e := backup.Create(path, r.Expected, key)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	for _, file := range []struct {
		name string
		data []byte
	}{{"compose", b.Compose}, {"release", b.Release}, {"database", []byte("old fixture")}} {
		if e = w.Add(file.name, func(out io.Writer) error { _, e := out.Write(file.data); return e }); e != nil {
			t.Fatal(e)
		}
	}
	if e = w.Finalize(); e != nil {
		t.Fatal(e)
	}
	if _, e = Verify(path, r, key); e != nil {
		t.Fatal("schema 17 archive stranded by upgrade", e)
	}
}
