package netutil

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// The dedicated gateway replaces this credential and the single client IP;
// raw incoming Forwarded/XFF never become a trusted chain. The API port remains
// unpublished. Missing/invalid proof falls back to the actual peer address.
func GatewayAuthenticated(r *http.Request, secret string) bool {
	values := r.Header.Values("X-Frogim-Gateway")
	return len(secret) >= 32 && len(values) == 1 && subtle.ConstantTimeCompare([]byte(values[0]), []byte(secret)) == 1
}
func GatewayClientIP(r *http.Request, secret string) string {
	if GatewayAuthenticated(r, secret) && len(r.Header.Values("X-Real-IP")) == 1 {
		if ip := NormalizeIP(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
	}
	return ClientIP(r, false)
}

// NormalizeIP accepts addresses, not CIDRs, hostnames, ports or zone IDs.
func NormalizeIP(raw string) string {
	ip, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil || ip.Zone() != "" {
		return ""
	}
	return ip.Unmap().String()
}

// ClientIP returns the direct peer unless the deployment explicitly trusts its
// reverse proxy. The trusted proxy is responsible for replacing/sanitizing the
// incoming X-Forwarded-For header before it reaches this service.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		for _, forwarded := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
			if ip := net.ParseIP(strings.TrimSpace(forwarded)); ip != nil {
				return ip.String()
			}
		}
	}
	host := r.RemoteAddr
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	return strings.Trim(host, "[]")
}
