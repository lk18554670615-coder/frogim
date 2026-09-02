package ipregion

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRegionUnavailableAndSpecialAddresses(t *testing.T) {
	var r *Resolver
	for ip, status := range map[string]string{"": "unknown", "bad": "unknown", "::ffff:127.0.0.1": "loopback", "192.168.1.1": "private", "fc00::1": "private", "fe80::1": "reserved", "1.1.1.1": "unavailable"} {
		if v := r.Lookup(ip); v.Status != status {
			t.Fatalf("%s: %+v", ip, v)
		}
	}
	r = New(t.TempDir())
	if r.Lookup("1.1.1.1").Status != "unavailable" {
		t.Fatal("missing data must degrade")
	}
}
func TestRegionExactFiveFieldFormat(t *testing.T) {
	r := &Resolver{lookup: func(string) (string, error) { return "中国|广东省|深圳市|联通|CN", nil }}
	v := r.Lookup("1.1.1.1")
	if v.Status != "ok" || v.ISP != "联通" || v.City != "深圳市" {
		t.Fatalf("%+v", v)
	}
	r.lookup = func(string) (string, error) { return "Australia|0|0|0|AU", nil }
	v = r.Lookup("2001:4860:4860::8888")
	if v.City != "" || v.ISP != "" {
		t.Fatal("invented missing region")
	}
	r.lookup = func(string) (string, error) { return "", errors.New("read failed") }
	if r.Lookup("1.1.1.1").Status != "unavailable" {
		t.Fatal("lookup error")
	}
}
func TestPinnedDataLookup(t *testing.T) {
	dir := os.Getenv("IM_TEST_IP_REGION_DIR")
	if dir == "" {
		t.Skip("IM_TEST_IP_REGION_DIR not set")
	}
	r := New(dir)
	defer r.Close()
	for _, ip := range []string{"1.2.3.4", "240e:3b7:3272:d8d0:db09:c067:8d59:539e"} {
		v := r.Lookup(ip)
		if v.Status != "ok" || v.Country == "" {
			t.Fatalf("%s %+v", ip, v)
		}
	}
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "ip2region_v4.xdb"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	b := New(bad)
	defer b.Close()
	if b.Lookup("1.2.3.4").Status != "unavailable" {
		t.Fatal("corrupt data used")
	}
}
