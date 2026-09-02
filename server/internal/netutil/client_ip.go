package netutil

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

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
