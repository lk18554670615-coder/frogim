package webpushpolicy

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

func keys(t *testing.T) (string, string) {
	t.Helper()
	k, e := ecdh.P256().GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(k.Bytes())
}

func TestConfiguration(t *testing.T) {
	pub, priv := keys(t)
	other, _ := keys(t)
	for _, subject := range []string{"mailto:ops@example.com", "https://example.com/push"} {
		p, e := New(pub, priv, subject, "PUSH.example.com, other.example.com")
		if e != nil || !p.Hosts["push.example.com"] || p.PublicKey != pub {
			t.Fatal("valid configuration rejected", e)
		}
	}
	for _, c := range [][4]string{
		{other, priv, "mailto:ops@example.com", "push.example.com"},
		{pub, "bad", "mailto:ops@example.com", "push.example.com"},
		{pub, priv, "http://example.com", "push.example.com"},
		{pub, priv, "https://user:pass@example.com", "push.example.com"},
		{pub, priv, "mailto:invalid", "push.example.com"},
	} {
		if _, e := New(c[0], c[1], c[2], c[3]); e != ErrInvalid {
			t.Fatal("invalid configuration accepted")
		}
	}
	for _, hosts := range []string{"", "*", "*.example.com", "localhost", "127.0.0.1", "[::1]", "example.com:443", "example.com.", "example..com", "-bad.example", "example.com/path", "example.com,", "a." + strings.Repeat("b", 64)} {
		if _, e := New(pub, priv, "mailto:ops@example.com", hosts); e != ErrInvalid {
			t.Errorf("invalid host configuration accepted: %q", hosts)
		}
	}
}

func TestSubscriptionCanonicalAndValidation(t *testing.T) {
	pub, priv := keys(t)
	p, _ := New(pub, priv, "mailto:ops@example.com", "push.example.com")
	token := func(endpoint, point, auth string) string {
		v, _ := json.Marshal(map[string]any{"endpoint": endpoint, "expirationTime": 123, "ignored": "drop", "keys": map[string]string{"p256dh": point, "auth": auth}})
		return string(v)
	}
	auth := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))
	one, subscription, e := p.Subscription(token("https://PUSH.example.com:443/sub?secret=example", pub+"=", auth+"=="))
	if e != nil {
		t.Fatal(e)
	}
	two, _, e := p.Subscription(token("https://push.example.com/sub?secret=example", pub, auth))
	if e != nil || one != two || strings.Contains(one, "ignored") || subscription.Endpoint != "https://push.example.com/sub?secret=example" {
		t.Fatal("unstable canonical subscription")
	}
	for _, endpoint := range []string{"http://push.example.com/x", "https://push.example.com:8443/x", "https://push.example.com.evil.test/x", "https://user@push.example.com/x", "https://push.example.com/x#fragment", "https://127.0.0.1/x", "https://[::1]/x", "https://push.example.com./x", "https://push.example.com/" + strings.Repeat("x", 4096)} {
		if _, _, e := p.Subscription(token(endpoint, pub, auth)); e != ErrInvalid {
			t.Fatal("unsafe endpoint accepted")
		}
	}
	for _, raw := range []string{"null", "{}", "[1]", strings.Repeat("x", 8193), token("https://push.example.com/x", "invalid", auth), token("https://push.example.com/x", pub, "bad"), token("https://push.example.com/x", base64.RawURLEncoding.EncodeToString(make([]byte, 65)), auth)} {
		if _, _, e := p.Subscription(raw); e != ErrInvalid {
			t.Fatal("invalid subscription accepted")
		}
	}
}

func TestPublicIPs(t *testing.T) {
	for _, raw := range []string{"", "0.0.0.0", "127.0.0.1", "10.1.2.3", "172.16.1.1", "192.168.1.1", "169.254.169.254", "100.64.1.1", "192.0.2.3", "198.51.100.1", "203.0.113.1", "198.18.0.1", "224.0.0.1", "255.255.255.255", "::", "::1", "::ffff:127.0.0.1", "fe80::1", "fc00::1", "ff02::1", "2001:db8::1", "2002:7f00:1::", "64:ff9b::7f00:1"} {
		if PublicIP(net.ParseIP(raw)) {
			t.Errorf("non-public address accepted: %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "::ffff:8.8.8.8"} {
		if !PublicIP(net.ParseIP(raw)) {
			t.Errorf("public address rejected: %s", raw)
		}
	}
}
