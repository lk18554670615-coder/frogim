// Package webpushpolicy validates platform-owned browser subscriptions without
// importing enterprise storage. Errors deliberately omit endpoints and keys.
package webpushpolicy

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
)

var ErrInvalid = errors.New("invalid Web Push configuration or subscription")

type Policy struct {
	PublicKey string
	Hosts     map[string]bool
}

func New(public, private, subject, allowedHosts string) (*Policy, error) {
	pub, e := base64.RawURLEncoding.DecodeString(public)
	if e != nil {
		return nil, ErrInvalid
	}
	secret, e := base64.RawURLEncoding.DecodeString(private)
	if e != nil {
		return nil, ErrInvalid
	}
	key, e := ecdh.P256().NewPrivateKey(secret)
	if e != nil || !bytes.Equal(key.PublicKey().Bytes(), pub) {
		return nil, ErrInvalid
	}
	u, e := url.Parse(subject)
	if e != nil || u.User != nil || u.Fragment != "" ||
		!(u.Scheme == "https" && u.Hostname() != "" || u.Scheme == "mailto" && strings.Contains(u.Opaque, "@")) {
		return nil, ErrInvalid
	}
	hosts := map[string]bool{}
	for _, raw := range strings.Split(allowedHosts, ",") {
		host := strings.ToLower(strings.TrimSpace(raw))
		if len(host) > 253 || !strings.Contains(host, ".") || net.ParseIP(host) != nil {
			return nil, ErrInvalid
		}
		for _, part := range strings.Split(host, ".") {
			if part == "" || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
				return nil, ErrInvalid
			}
			for _, c := range part {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return nil, ErrInvalid
				}
			}
		}
		hosts[host] = true
	}
	if len(hosts) == 0 || len(hosts) > 32 {
		return nil, ErrInvalid
	}
	return &Policy{PublicKey: public, Hosts: hosts}, nil
}

func (p *Policy) Endpoint(raw string) bool {
	u, e := url.Parse(raw)
	return p != nil && e == nil && len(raw) <= 4096 && u.Scheme == "https" && u.User == nil &&
		u.Fragment == "" && u.Opaque == "" && (u.Port() == "" || u.Port() == "443") &&
		p.Hosts[strings.ToLower(u.Hostname())] && net.ParseIP(u.Hostname()) == nil
}

func (p *Policy) Subscription(raw string) (string, *webpush.Subscription, error) {
	if len(raw) == 0 || len(raw) > 8192 {
		return "", nil, ErrInvalid
	}
	var s webpush.Subscription
	if json.Unmarshal([]byte(raw), &s) != nil || !p.Endpoint(s.Endpoint) {
		return "", nil, ErrInvalid
	}
	pub, e := base64.RawURLEncoding.DecodeString(strings.TrimRight(s.Keys.P256dh, "="))
	if e != nil {
		return "", nil, ErrInvalid
	}
	if _, e = ecdh.P256().NewPublicKey(pub); e != nil {
		return "", nil, ErrInvalid
	}
	auth, e := base64.RawURLEncoding.DecodeString(strings.TrimRight(s.Keys.Auth, "="))
	if e != nil || len(auth) != 16 {
		return "", nil, ErrInvalid
	}
	u, _ := url.Parse(s.Endpoint)
	u.Host = strings.ToLower(u.Hostname()) // Only standard HTTPS port is permitted.
	s.Endpoint = u.String()
	s.Keys.P256dh = base64.RawURLEncoding.EncodeToString(pub)
	s.Keys.Auth = base64.RawURLEncoding.EncodeToString(auth)
	// Drop expirationTime and unknown browser fields for stable, canonical identity.
	canonical, e := json.Marshal(s)
	if e != nil {
		return "", nil, ErrInvalid
	}
	return string(canonical), &s, nil
}

// Reject special-use addresses as well as RFC1918. DNS is resolved once and the
// checked numeric IP is dialled directly, while TLS still verifies the hostname.
func PublicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96"} {
		if netip.MustParsePrefix(block).Contains(a) {
			return false
		}
	}
	return true
}
