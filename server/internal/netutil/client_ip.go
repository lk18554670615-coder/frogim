package netutil

import (
	"net"
	"net/http"
	"strings"
)

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
