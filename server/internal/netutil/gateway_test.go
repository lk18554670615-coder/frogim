package netutil

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayForwardingRequiresIndependentProof(t *testing.T) {
	secret := strings.Repeat("g", 43)
	r := httptest.NewRequest("GET", "https://app.example.test/v2/config/auth", nil)
	r.RemoteAddr = "10.40.0.3:1234"
	r.Header.Set("X-Real-IP", "203.0.113.41")
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	for _, proof := range []string{"", "invalid"} {
		r.Header.Set("X-Frogim-Gateway", proof)
		if GatewayClientIP(r, secret) != "10.40.0.3" {
			t.Fatal("spoofed forwarded IP trusted")
		}
	}
	r.Header.Set("X-Frogim-Gateway", secret)
	if GatewayClientIP(r, secret) != "203.0.113.41" {
		t.Fatal("authenticated IP ignored")
	}
	if GatewayClientIP(r, "") != "10.40.0.3" {
		t.Fatal("unconfigured gateway trusted")
	}
	for _, ip := range []string{"203.0.113.41, 198.51.100.7", "not-an-ip", "fe80::1%eth0", ""} {
		r.Header.Set("X-Real-IP", ip)
		if GatewayClientIP(r, secret) != "10.40.0.3" {
			t.Fatal("invalid source accepted")
		}
	}
	r.Header.Set("X-Real-IP", "2001:db8::5")
	if GatewayClientIP(r, secret) != "2001:db8::5" {
		t.Fatal("IPv6")
	}
	r.Header.Add("X-Frogim-Gateway", secret)
	if GatewayClientIP(r, secret) != "10.40.0.3" {
		t.Fatal("ambiguous proxy proof accepted")
	}
}
