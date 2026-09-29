package deployment

import (
	"encoding/base64"
	"net/netip"
	"regexp"
	"strings"
)

const SharedIngressMode = "shared_edge"

// Private operator input. The edge proof is separate from the API gateway
// proof, and is never forwarded to the API, browser or object store.
type SharedIngress struct {
	Secret    string `json:"secret"`
	HTTPSPort int    `json:"httpsPort,omitempty"`
}

func (s SharedIngress) valid() error {
	b, e := base64.RawURLEncoding.DecodeString(s.Secret)
	if e != nil || len(b) != 32 {
		return ErrBundle
	}
	return nil
}
func (c EnterpriseConfig) sharedIngress() bool {
	return c.Production != nil && c.Production.SharedIngress != nil
}
func (c EnterpriseConfig) mediaBucket() string {
	if c.MediaBucket == "" {
		return "enterprise-media"
	}
	return c.MediaBucket
}
func validMediaBucket(s string) bool {
	// Deliberately narrower than S3: no dots, IP literals or routing escapes.
	return regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`).MatchString(s)
}

func protectSharedGateway(text string, ports []string) string {
	guard := `
    @untrusted not header X-Frogim-Edge {$FROGIM_EDGE_SECRET}
    respond @untrusted 403
`
	for _, p := range ports {
		start := strings.Index(text, "https://:"+p+" {")
		if start < 0 {
			continue
		}
		open := start + strings.Index(text[start:], "{")
		depth, end := 1, open+1
		for ; end < len(text) && depth > 0; end++ {
			if text[end] == '{' {
				depth++
			}
			if text[end] == '}' {
				depth--
			}
		}
		body := strings.Replace(text[open+1:end-1], "  tls /config/public.pem /config/public.key", "", 1)
		text = text[:open+1] + "\n  tls /config/public.pem /config/public.key\n  route {\n" + guard + body + "\n  }\n" + text[end-1:]
	}
	text = strings.ReplaceAll(text, "header_up X-Forwarded-For {remote_host}", "header_up X-Forwarded-For {http.request.header.X-Frogim-Client-IP}")
	text = strings.ReplaceAll(text, "header_up X-Real-IP {remote_host}", "header_up X-Real-IP {http.request.header.X-Frogim-Client-IP}")
	// Proof headers are stripped at each reverse proxy, including IM and S3.
	text = strings.ReplaceAll(text, "header_up -Forwarded", "header_up -Forwarded\n      header_up -X-Frogim-Edge\n      header_up -X-Frogim-Client-IP")
	for _, upstream := range []string{"enterprise-im:5200", "enterprise-minio:9000"} {
		text = strings.ReplaceAll(text, "reverse_proxy "+upstream+"\n", "reverse_proxy "+upstream+" {\n      header_up -X-Frogim-Edge\n      header_up -X-Frogim-Client-IP\n    }\n")
	}
	return text
}
func (c PlatformConfig) AuthURL() string {
	if c.SharedIngress != nil {
		return c.PublicURL + "/platform"
	}
	return c.PublicURL
}
func (c PlatformConfig) gatewayConfiguration() string {
	if c.SharedIngress == nil {
		return platformCaddyConfig
	}
	text := strings.Replace(platformCaddyConfig, "  handle {\n    root * /srv/platform", "  handle_path /platform/* {\n    root * /srv/platform", 1)
	return protectSharedGateway(text, []string{"8443"})
}
func validateIngressBundle(b Bundle, r Release) error {
	g := b.Services["enterprise-gateway"]
	if g.Environment["FROGIM_INGRESS_MODE"] != r.IngressMode {
		return ErrBundle
	}
	if r.IngressMode == "" {
		return nil
	}
	if r.IngressMode != SharedIngressMode || r.TenantID != "default" || !sharedBundle(b) || (SharedIngress{Secret: g.Environment["FROGIM_EDGE_SECRET"]}).valid() != nil || g.Environment["FROGIM_EDGE_SECRET"] == g.Environment["FROGIM_GATEWAY_SECRET"] {
		return ErrBundle
	}
	for _, p := range g.Ports {
		ip, err := netip.ParseAddr(p.HostIP)
		if err != nil || !ip.IsPrivate() {
			return ErrBundle
		}
	}
	return nil
}
