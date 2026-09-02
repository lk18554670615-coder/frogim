package netutil

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPTrustBoundary(t *testing.T) {
	r := httptest.NewRequest("GET", "http://service/v2/users/me", nil)
	r.RemoteAddr = "10.0.0.8:43120"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.2")
	if got := ClientIP(r, false); got != "10.0.0.8" {
		t.Fatalf("direct bind trusted spoofed header: %q", got)
	}
	if got := ClientIP(r, true); got != "203.0.113.9" {
		t.Fatalf("trusted proxy address=%q", got)
	}
}

func TestNormalizeIP(t *testing.T) {
	for raw, want := range map[string]string{" 192.0.2.1 ": "192.0.2.1", "::ffff:192.0.2.1": "192.0.2.1", "2001:0db8:0000::1": "2001:db8::1", "192.0.2.1/24": "", "1.1.1.1:80": "", "example.com": "", "fe80::1%eth0": "", "garbage": ""} {
		if got := NormalizeIP(raw); got != want {
			t.Fatalf("%q got %q want %q", raw, got, want)
		}
	}
}
