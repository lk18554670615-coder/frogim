package backup

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestDailyReceiptDiscoveryImmutableAndPrivate(t *testing.T) {
	c, _, _, key := offsiteFixture(t)
	c.Scope, c.TenantID, c.ServerID, c.DirectoryID = "platform", "", "", "9a161c47-a687-415d-94ab-908cbd52b7a3"
	b := Binding{Scope: "platform", DirectoryID: c.DirectoryID, BackupID: "daily-test", ReleaseID: "release-one", ReleaseDigest: strings.Repeat("b", 64), SchemaVersion: 17}
	path := filepath.Join(t.TempDir(), "platform")
	w, e := Create(path, b, key)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	if w.Add("database", func(out io.Writer) error { _, e := io.WriteString(out, "private account data"); return e }) != nil || w.Finalize() != nil {
		t.Fatal("archive")
	}
	s := &memoryObjects{data: map[string][]byte{}, loseACK: true}
	o := &Offsite{config: c, objects: s}
	d, e := o.Deliver(t.Context(), b, path, key)
	if e != nil {
		t.Fatal(e)
	}
	date := "2026-09-29"
	if e = o.PublishDailyReceipt(t.Context(), date, d, key); e != nil {
		t.Fatal(e)
	}
	writes := s.writes
	if e = o.PublishDailyReceipt(t.Context(), date, d, key); e != nil || s.writes != writes {
		t.Fatal("lost ACK duplicated index", e)
	}
	r, e := o.ReadDailyReceipt(t.Context(), date, key)
	if e != nil || r.Delivery != d {
		t.Fatal("discovery", e)
	}
	index, _ := o.dailyKey(date)
	if bytes.Contains(s.data[index], []byte(b.BackupID)) || bytes.Contains(s.data[index], []byte(d.Manifest.SHA256)) {
		t.Fatal("index not encrypted")
	}
	if _, e = o.ReadDailyReceipt(t.Context(), date, bytes.Repeat([]byte{30}, 32)); e == nil {
		t.Fatal("wrong key")
	}
	if _, e = o.ReadDailyReceipt(t.Context(), "2026-02-30", key); e == nil {
		t.Fatal("invalid date")
	}
	s.data[index][10] ^= 1
	if e = o.PublishDailyReceipt(t.Context(), date, d, key); e == nil || s.writes != writes {
		t.Fatal("tampered index replaced")
	}
	s.data[index][10] ^= 1
	otherDate := "2026-09-28"
	otherPath, _ := o.dailyKey(otherDate)
	s.data[otherPath] = bytes.Clone(s.data[index])
	if _, e = o.ReadDailyReceipt(t.Context(), otherDate, key); e == nil {
		t.Fatal("cross-date replay")
	}
	wrong := *o
	wrong.config.DirectoryID = "8a161c47-a687-415d-94ab-908cbd52b7a3"
	foreign, _ := wrong.dailyKey(date)
	s.data[foreign] = bytes.Clone(s.data[index])
	if _, e = wrong.ReadDailyReceipt(t.Context(), date, key); e == nil {
		t.Fatal("cross-directory replay")
	}
	c2 := c
	c2.SecretKey = strings.Repeat("z", 43)
	c2.CAFile = filepath.Join(t.TempDir(), "ca.pem")
	if (&Offsite{config: c2}).DestinationFingerprint() != o.DestinationFingerprint() {
		t.Fatal("credential rotation changes destination")
	}
	c2.Prefix = "other"
	if (&Offsite{config: c2}).DestinationFingerprint() == o.DestinationFingerprint() {
		t.Fatal("destination mismatch")
	}
}
