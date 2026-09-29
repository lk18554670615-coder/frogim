package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memoryObjects struct {
	data               map[string][]byte
	writes             int
	loseACK, interrupt bool
}

func (s *memoryObjects) get(_ context.Context, k string) (io.ReadCloser, error) {
	b, ok := s.data[k]
	if !ok {
		return nil, errObjectAbsent
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (s *memoryObjects) putNew(_ context.Context, k string, b []byte) error {
	if s.interrupt {
		return ErrOffsite
	}
	if _, ok := s.data[k]; ok {
		return errObjectExists
	}
	s.data[k] = bytes.Clone(b)
	s.writes++
	if s.loseACK {
		return ErrOffsite
	}
	return nil
}
func offsiteFixture(t *testing.T) (OffsiteConfig, Binding, string, []byte) {
	t.Helper()
	b := Binding{TenantID: "alpha", ServerID: "host-alpha", ReleaseID: "release-one", ReleaseDigest: strings.Repeat("b", 64), Generation: 2, AccessVersion: 3, SchemaVersion: 79}
	c := OffsiteConfig{ID: "alpha-offsite", Scope: "enterprise", TenantID: "alpha", ServerID: "host-alpha", Endpoint: "https://backup.example.test", Bucket: "isolated-backups", Prefix: "frogim", Region: "us-east-1", AccessKey: "fixture-access-key", SecretKey: strings.Repeat("s", 43)}
	key := bytes.Repeat([]byte{29}, 32)
	path := filepath.Join(t.TempDir(), "archive")
	w, e := Create(path, b, key)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	if w.Add("database", func(out io.Writer) error {
		_, e := io.WriteString(out, "private-data-not-for-object-storage")
		return e
	}) != nil || w.Finalize() != nil {
		t.Fatal("fixture archive")
	}
	return c, b, path, key
}
func TestOffsiteScopeTLSAndLeastPrivilegePolicy(t *testing.T) {
	c, b, _, _ := offsiteFixture(t)
	if !c.matches(b) {
		t.Fatal("matching scope")
	}
	for _, change := range []func(*Binding){func(b *Binding) { b.TenantID = "beta" }, func(b *Binding) { b.ServerID = "other" }, func(b *Binding) { b.Scope = "platform" }} {
		wrong := b
		change(&wrong)
		if c.matches(wrong) {
			t.Fatal("scope accepted")
		}
	}
	for _, change := range []func(*OffsiteConfig){func(c *OffsiteConfig) { c.Endpoint = "http://backup.example.test" }, func(c *OffsiteConfig) { c.Endpoint += "/path" }, func(c *OffsiteConfig) { c.Endpoint += "?redirect=other" }, func(c *OffsiteConfig) { c.Prefix = "../beta" }, func(c *OffsiteConfig) { c.SecretKey = "short" }, func(c *OffsiteConfig) { c.DirectoryID = "9a161c47-a687-415d-94ab-908cbd52b7a3" }} {
		wrong := c
		change(&wrong)
		if wrong.valid() {
			t.Fatal("unsafe config")
		}
	}
	p, e := c.IAMPolicy()
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(p), "DeleteObject") || strings.Contains(string(p), "ListBucket") || strings.Contains(string(p), c.SecretKey) || !strings.Contains(string(p), "frogim/enterprise/alpha/host-alpha/*") {
		t.Fatal("overbroad policy")
	}
	c.Scope = "platform"
	c.TenantID = ""
	c.ServerID = ""
	c.DirectoryID = "9a161c47-a687-415d-94ab-908cbd52b7a3"
	if !c.valid() {
		t.Fatal("platform config")
	}
	if c.matches(b) {
		t.Fatal("enterprise reached platform store")
	}
	request, _ := http.NewRequest("GET", "https://other.example.test/object", nil)
	if _, e := (fixedOriginTransport{origin: "backup.example.test"}).RoundTrip(request); !errors.Is(e, ErrOffsite) {
		t.Fatal("credentials could follow foreign origin")
	}
}
func TestOffsiteRoundTripAndLostPutAcknowledgment(t *testing.T) {
	c, b, path, key := offsiteFixture(t)
	objects := &memoryObjects{data: map[string][]byte{}, loseACK: true}
	o := &Offsite{config: c, objects: objects}
	d, e := o.Deliver(t.Context(), b, path, key)
	if e != nil {
		t.Fatal(e)
	}
	writes := objects.writes
	repeated, e := o.Deliver(t.Context(), b, path, key)
	if e != nil || repeated != d || objects.writes != writes {
		t.Fatal("repeat upload changed objects")
	}
	for _, data := range objects.data {
		if bytes.Contains(data, []byte("private-data-not-for-object-storage")) || bytes.Contains(data, key) {
			t.Fatal("plaintext/key sent")
		}
	}
	destination := filepath.Join(t.TempDir(), "restored")
	if e = o.Retrieve(t.Context(), b, d, destination, key); e != nil {
		t.Fatal(e)
	}
	if e = o.Retrieve(t.Context(), b, d, destination, key); e != nil {
		t.Fatal("repeat download", e)
	}
	r, e := Open(destination, b, key)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	var plain bytes.Buffer
	if r.Read("database", &plain) != nil || plain.String() != "private-data-not-for-object-storage" {
		t.Fatal("restored content")
	}
	d.Binding.TenantID = "beta"
	if e = o.Retrieve(t.Context(), b, d, filepath.Join(t.TempDir(), "foreign"), key); e == nil {
		t.Fatal("receipt chose another tenant")
	}
}
func TestOffsiteIncompleteCorruptOrWrongKeyNeverCompletes(t *testing.T) {
	c, b, path, key := offsiteFixture(t)
	objects := &memoryObjects{data: map[string][]byte{}, interrupt: true}
	o := &Offsite{config: c, objects: objects}
	if _, e := o.Deliver(t.Context(), b, path, key); e == nil {
		t.Fatal("failed upload completed")
	}
	for k := range objects.data {
		if strings.HasSuffix(k, "/manifest.sealed") {
			t.Fatal("early completion marker")
		}
	}
	objects.interrupt = false
	d, e := o.Deliver(t.Context(), b, path, key)
	if e != nil {
		t.Fatal(e)
	}
	if e = o.Retrieve(t.Context(), b, d, filepath.Join(t.TempDir(), "wrong-key"), bytes.Repeat([]byte{30}, 32)); e == nil {
		t.Fatal("wrong key")
	}
	chunk := chunkKey(o.root(d), "database", 0)
	original := bytes.Clone(objects.data[chunk])
	objects.data[chunk][0] ^= 1
	writes := objects.writes
	if _, e = o.Deliver(t.Context(), b, path, key); e == nil || objects.writes != writes {
		t.Fatal("corrupt remote replaced")
	}
	dest := filepath.Join(t.TempDir(), "partial")
	if e = o.Retrieve(t.Context(), b, d, dest, key); e == nil {
		t.Fatal("corrupt download accepted")
	}
	if _, e = os.Stat(filepath.Join(dest, "manifest.sealed")); !os.IsNotExist(e) {
		t.Fatal("download published early")
	}
	objects.data[chunk] = original
	if e = o.Retrieve(t.Context(), b, d, dest, key); e == nil {
		t.Fatal("partial local set overwritten")
	}
	if e = o.Retrieve(t.Context(), b, d, filepath.Join(t.TempDir(), "new-attempt"), key); e != nil {
		t.Fatal(e)
	}
}
func TestOffsiteSegmentsLargeCiphertext(t *testing.T) {
	c, b, path, key := offsiteFixture(t)
	// A separate source, not an overwrite of the preceding completed fixture.
	path = filepath.Join(t.TempDir(), "large")
	w, e := Create(path, b, key)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	data := bytes.Repeat([]byte{17}, offsiteChunkSize+63)
	if w.Add("database", func(out io.Writer) error { _, e := out.Write(data); return e }) != nil || w.Finalize() != nil {
		t.Fatal("large fixture")
	}
	objects := &memoryObjects{data: map[string][]byte{}}
	o := &Offsite{config: c, objects: objects}
	d, e := o.Deliver(t.Context(), b, path, key)
	if e != nil {
		t.Fatal(e)
	}
	if len(objects.data) != 3 || len(objects.data[chunkKey(o.root(d), "database", 0)]) != offsiteChunkSize {
		t.Fatal("unbounded object")
	}
	if e = o.Retrieve(t.Context(), b, d, filepath.Join(t.TempDir(), "restored"), key); e != nil {
		t.Fatal(e)
	}
}
