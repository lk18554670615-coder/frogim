package deployment

import (
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/linli/im/server/internal/tenancy"
)

// Operator-owned, never accepted from deployment HTTP. Public TLS must cover
// both origins; the mTLS certificate covers ControlURL. Its listener is bound
// only to the selected private host address, not the public interface.
type EnterpriseProduction struct {
	SharedIngress *SharedIngress `json:"sharedIngress,omitempty"`
	APIOrigin     string         `json:"apiOrigin"`
	MediaOrigin   string         `json:"mediaOrigin"`
	ControlURL    string         `json:"controlUrl"`
	PublicBindIP  string         `json:"publicBindIp"`
	ControlBindIP string         `json:"controlBindIp"`
	IMHost        string         `json:"imHost"`
	RTCNodeIP     string         `json:"rtcNodeIp"`
}

func (c EnterpriseConfig) productionMode() bool { return c.Production != nil }
func (c EnterpriseConfig) isolationMode() string {
	if c.productionMode() {
		return "dedicated_host"
	}
	return "local_preview"
}
func originPort(raw string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	if u.Port() == "" {
		return 443
	}
	p, _ := strconv.Atoi(u.Port())
	return p
}
func (c EnterpriseConfig) validateProduction() error {
	p := c.Production
	if p == nil {
		return ErrBundle
	}
	if tenancy.PublicOrigin(p.APIOrigin) != nil || tenancy.PublicOrigin(p.MediaOrigin) != nil || tenancy.PublicOrigin(c.PlatformWebOrigin) != nil || productionControlURL(p.ControlURL) != nil || productionControlURL(c.PlatformControlURL) != nil {
		return ErrBundle
	}
	if (!c.sharedIngress() && (originPort(p.APIOrigin) != c.Ports.HTTP || originPort(p.MediaOrigin) != c.Ports.Media)) || originPort(p.ControlURL) != c.Ports.Control {
		return ErrBundle
	}
	if c.Ports.RTCTCP == 7880 || (c.Ports.RTCUDPFrom <= 7880 && c.Ports.RTCUDPFrom+3 >= 7880) {
		return ErrBundle
	}
	if (!c.sharedIngress() && (p.APIOrigin == p.MediaOrigin || p.APIOrigin == c.PlatformWebOrigin)) || p.APIOrigin == c.PlatformControlURL || p.ControlURL == p.APIOrigin || p.ControlURL == p.MediaOrigin {
		return ErrBundle
	}
	if c.sharedIngress() && (c.TenantID != "default" || c.SharedDatastores == nil || p.SharedIngress.valid() != nil || p.SharedIngress.Secret == c.Secrets.Gateway || p.APIOrigin != p.MediaOrigin || p.APIOrigin != c.PlatformWebOrigin || c.Ports.HTTP < 1024 || c.Ports.Media < 1024) {
		return ErrBundle
	}
	bind, err := netip.ParseAddr(p.PublicBindIP)
	if err != nil || bind.IsLoopback() || bind.IsLinkLocalUnicast() || bind.IsMulticast() || (!bind.IsUnspecified() && !bind.IsGlobalUnicast()) {
		return ErrBundle
	}
	control, err := netip.ParseAddr(p.ControlBindIP)
	if err != nil || !control.IsPrivate() || tenancy.PrivateListen(net.JoinHostPort(p.ControlBindIP, strconv.Itoa(c.Ports.Control))) != nil {
		return ErrBundle
	}
	// Provisioned ICE address, not a preview/wildcard candidate. Actual NAT and
	// firewall reachability remains an infrastructure acceptance check.
	node, err := netip.ParseAddr(p.RTCNodeIP)
	if err != nil || !node.IsGlobalUnicast() || node.IsLoopback() || node.IsLinkLocalUnicast() {
		return ErrBundle
	}
	if tenancy.PublicOrigin("https://"+strings.TrimSuffix(net.JoinHostPort(p.IMHost, "443"), ":443")) != nil || strings.ContainsAny(p.IMHost, "/?#@") {
		return ErrBundle
	}
	for _, raw := range []string{p.APIOrigin, p.MediaOrigin} {
		u, _ := url.Parse(raw)
		if validateBundleTLSHost(c.PublicTLS, "", false, u.Hostname()) != nil {
			return ErrBundle
		}
	}
	u, _ := url.Parse(p.ControlURL)
	return validateBundleTLSHost(c.ControlTLS, tenancy.EnterpriseIdentity(c.TenantID), true, u.Hostname())
}

func (c EnterpriseConfig) mediaHost() string {
	if c.Production != nil {
		u, _ := url.Parse(c.Production.MediaOrigin)
		return u.Host
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(c.Ports.Media))
}
func (c EnterpriseConfig) imHost() string {
	if c.Production != nil {
		return c.Production.IMHost
	}
	return "127.0.0.1"
}
func (c EnterpriseConfig) liveKitConfiguration() string {
	node := "127.0.0.1"
	if c.Production != nil {
		node = c.Production.RTCNodeIP
	}
	return strings.ReplaceAll(enterpriseLiveKitConfig, "node_ip: 127.0.0.1", "node_ip: "+node)
}

func (c EnterpriseConfig) gatewayConfiguration() string {
	if !c.productionMode() {
		return enterpriseCaddyConfig
	}
	// The public listener never trusts client forwarding headers. Only its own
	// independently generated proof enables source-address handling at the API.
	proxy := `reverse_proxy enterprise-api:8080 {
      header_up X-Forwarded-For {remote_host}
      header_up X-Real-IP {remote_host}
      header_up X-Frogim-Gateway {$FROGIM_GATEWAY_SECRET}
      header_up -Forwarded
    }`
	text := strings.ReplaceAll(enterpriseCaddyConfig, "reverse_proxy enterprise-api:8080", proxy)
	security := `  header {
    -Server
    X-Content-Type-Options nosniff
    X-Frame-Options DENY
    Referrer-Policy no-referrer
    Strict-Transport-Security "max-age=15552000"
    Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data: blob:; connect-src 'self' https: blob:; font-src 'self' data:; media-src 'self' https: blob:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"
  }
`
	text = strings.Replace(text, "https://:8444 {\n", "https://:8444 {\n"+security, 1)
	if c.sharedIngress() {
		text = protectSharedGateway(text, []string{"8444", "8445"})
	}
	return text
}

func (r Release) isolationMode() string {
	if r.IsolationMode == "" {
		return "local_preview"
	}
	return r.IsolationMode
}

// DB, cache, storage console, IM manager, raw LiveKit/Twirp and health/metrics
// endpoints are not publicly exposed by a production deployment artifact.
func productionPort(service string, p Port) bool {
	bind, err := netip.ParseAddr(p.HostIP)
	if err != nil || bind.IsLoopback() || bind.IsMulticast() || bind.IsLinkLocalUnicast() || (!bind.IsUnspecified() && !bind.IsGlobalUnicast()) {
		return false
	}
	switch service {
	case "enterprise-api":
		return p.Target == 8444 && p.Protocol == "tcp" && bind.IsPrivate()
	case "enterprise-gateway":
		return (p.Target == 8444 || p.Target == 8445) && p.Protocol == "tcp"
	case "enterprise-im":
		return p.Target == 5100 && p.Protocol == "tcp"
	case "enterprise-livekit":
		return p.Target >= 1024 && p.Target != 7880 && (p.Protocol == "tcp" || p.Protocol == "udp") && strconv.Itoa(p.Target) == p.Published
	}
	return false
}

func validateProductionBundle(b Bundle, r Release) error {
	if validateIngressBundle(b, r) != nil {
		return ErrBundle
	}
	_, edge := b.Networks["edge"]
	serviceCount, networkCount := 9, 2
	if sharedBundle(b) {
		serviceCount, networkCount = 7, 3
	}
	if !r.MatchesTarget(r.TenantID, r.ServerID) || len(b.Services) != serviceCount || len(b.Networks) != networkCount || !edge || !b.Networks["business"].Internal || b.Networks["edge"].Internal {
		return ErrBundle
	}
	for _, name := range []string{"enterprise-db", "enterprise-redis", "enterprise-minio", "enterprise-media-init", "enterprise-plugins", "enterprise-api", "enterprise-im", "enterprise-livekit", "enterprise-gateway"} {
		if sharedBundle(b) && (name == "enterprise-db" || name == "enterprise-redis") {
			continue
		}
		s, ok := b.Services[name]
		if !ok {
			return ErrBundle
		}
		if name == "enterprise-db" || name == "enterprise-redis" || name == "enterprise-minio" {
			if len(s.Ports) != 0 || len(s.Networks) != 1 || s.Networks[0] != "business" {
				return ErrBundle
			}
		}
	}
	api := b.Services["enterprise-api"].Environment
	if api["IM_ENV"] != "production" || api["IM_TENANCY_PREVIEW"] != "false" || api["IM_TENANT_ID"] != r.TenantID || api["IM_TENANT_DEPLOYMENT_MODE"] != "dedicated_host" || api["IM_PUSH_PROVIDER"] != "platform" || tenancy.PublicOrigin(api["IM_TENANT_PUBLIC_URL"]) != nil {
		return ErrBundle
	}
	for _, key := range []string{"IM_DEV_MODE", "IM_SEED_DEMO", "IM_DEV_ALLOW_CONTAINER_BIND", "IM_IP_TEST_ONLY"} {
		if api[key] != "" && api[key] != "false" {
			return ErrBundle
		}
	}
	if api["IM_DEV_OTP_CODE"] != "" || api["IM_OTP_WEBHOOK_URL"] != "" || tenancy.DeploymentDatastores(api["IM_DATABASE_URL"], api["IM_REDIS_URL"], "enterprise", r.TenantID, api["IM_DATASTORE_MODE"]) != nil {
		return ErrBundle
	}
	return nil
}
