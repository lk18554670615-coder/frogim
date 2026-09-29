package deployment

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

// EnterpriseConfig is a PRIVATE operator artifact, not platform API input.
// Keep it across releases: rebuilding a bundle must not rotate media, database,
// auth or IM secrets. Public addressing requires a separate explicit profile;
// an absent profile always retains loopback-only preview semantics.
type EnterpriseConfig struct {
	MediaBucket        string                `json:"mediaBucket,omitempty"`
	SharedDatastores   *SharedDatastores     `json:"sharedDatastores,omitempty"`
	TenantID           string                `json:"tenantId"`
	ServerID           string                `json:"serverId"`
	ToolsImage         string                `json:"toolsImage"`
	PlatformControlURL string                `json:"platformControlUrl"`
	PlatformWebOrigin  string                `json:"platformWebOrigin"`
	Ports              EnterprisePorts       `json:"ports"`
	Secrets            EnterpriseSecrets     `json:"secrets"`
	ControlTLS         EnterpriseTLS         `json:"controlTls"`
	PublicTLS          EnterpriseTLS         `json:"publicTls"`
	Production         *EnterpriseProduction `json:"production,omitempty"`
}
type EnterpriseTLS struct {
	CA          string `json:"ca"`
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"privateKey"`
}
type EnterprisePorts struct {
	HTTP       int `json:"http"`
	Media      int `json:"media"`
	IM         int `json:"im"`
	Control    int `json:"control"`
	RTCTCP     int `json:"rtcTcp"`
	RTCUDPFrom int `json:"rtcUdpFrom"`
}
type EnterpriseSecrets struct {
	Database           string `json:"database"`
	Redis              string `json:"redis"`
	Media              string `json:"media"`
	JWT                string `json:"jwt"`
	MediaSigning       string `json:"mediaSigning"`
	LegacyMediaSigning string `json:"legacyMediaSigning,omitempty"`
	IMManager          string `json:"imManager"`
	IMToken            string `json:"imToken"`
	IMPolicy           string `json:"imPolicy"`
	LiveKit            string `json:"livekit"`
	AdminPasswordHash  string `json:"adminPasswordHash"`
	PluginPublicKey    string `json:"pluginPublicKey"`
	Gateway            string `json:"gateway,omitempty"`
}

func NewEnterpriseSecrets(password string) (EnterpriseSecrets, error) {
	var s EnterpriseSecrets
	if len(password) < 16 || len(password) > 72 {
		return s, ErrBundle
	}
	for _, target := range []*string{&s.Database, &s.Redis, &s.Media, &s.JWT, &s.MediaSigning, &s.IMManager, &s.IMToken, &s.IMPolicy, &s.LiveKit, &s.Gateway} {
		v, e := tenancy.Secret()
		if e != nil {
			return EnterpriseSecrets{}, ErrBundle
		}
		*target = v
	}
	h, e := bcrypt.GenerateFromPassword([]byte(password), 12)
	if e != nil {
		return EnterpriseSecrets{}, ErrBundle
	}
	s.AdminPasswordHash = string(h)
	pub, _, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return EnterpriseSecrets{}, ErrBundle
	}
	// Custom plugin installation is not enabled by generating a private key.
	// The baked-in policy artifact is verified independently by its SHA256.
	s.PluginPublicKey = base64.StdEncoding.EncodeToString(pub)
	return s, nil
}

func ReadEnterpriseConfig(reader io.Reader) (EnterpriseConfig, error) {
	var c EnterpriseConfig
	b, e := io.ReadAll(io.LimitReader(reader, (256<<10)+1))
	if e != nil || len(b) > 256<<10 {
		return c, ErrBundle
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Validate() != nil {
		return EnterpriseConfig{}, ErrBundle
	}
	return c, nil
}

func (c EnterpriseConfig) PublicURL() string {
	if c.Production != nil {
		return c.Production.APIOrigin
	}
	return fmt.Sprintf("https://127.0.0.1:%d", c.Ports.HTTP)
}

func (c EnterpriseConfig) Validate() error {
	if !validMediaBucket(c.mediaBucket()) {
		return ErrBundle
	}
	if c.SharedDatastores != nil && !c.SharedDatastores.valid(c.TenantID) {
		return ErrBundle
	}
	if !serviceName.MatchString(c.TenantID) || !serviceName.MatchString(c.ServerID) || !serviceName.MatchString("frogim-deploy-"+c.TenantID) || !imageReference.MatchString(c.ToolsImage) {
		return ErrBundle
	}
	control, e := url.Parse(c.PlatformControlURL)
	if e != nil || tenancy.ValidateBaseURL(c.PlatformControlURL, false) != nil {
		return ErrBundle
	}
	// Explicit development host names only. No accidentally generated public
	// authentication stack, fixed OTP or wildcard CORS.
	if !c.productionMode() && control.Hostname() != "host.docker.internal" && control.Hostname() != "platform-api" && net.ParseIP(control.Hostname()) == nil {
		return ErrBundle
	}
	if ip := net.ParseIP(control.Hostname()); !c.productionMode() && ip != nil && !ip.IsLoopback() {
		return ErrBundle
	}
	web, e := url.Parse(c.PlatformWebOrigin)
	if e != nil || tenancy.ValidateBaseURL(c.PlatformWebOrigin, false) != nil || (!c.productionMode() && web.Hostname() != "localhost" && web.Hostname() != "127.0.0.1" && web.Hostname() != "::1") {
		return ErrBundle
	}
	ports := map[int]bool{}
	for _, p := range []int{c.Ports.HTTP, c.Ports.Media, c.Ports.IM, c.Ports.Control, c.Ports.RTCTCP, c.Ports.RTCUDPFrom, c.Ports.RTCUDPFrom + 1, c.Ports.RTCUDPFrom + 2, c.Ports.RTCUDPFrom + 3} {
		if (p < 1024 && !(c.productionMode() && p == 443 && (c.Ports.HTTP == p || c.Ports.Media == p))) || p > 65535 || ports[p] {
			return ErrBundle
		}
		ports[p] = true
	}
	s := c.Secrets
	if s.LegacyMediaSigning != "" && (len(s.LegacyMediaSigning) < 16 || len(s.LegacyMediaSigning) > 4096 || strings.ContainsAny(s.LegacyMediaSigning, "\r\n") || s.LegacyMediaSigning == s.JWT) {
		return ErrBundle
	}
	seen := map[string]bool{}
	for _, secret := range []string{s.Database, s.Redis, s.Media, s.JWT, s.MediaSigning, s.IMManager, s.IMToken, s.IMPolicy, s.LiveKit} {
		decoded, e := base64.RawURLEncoding.DecodeString(secret)
		if e != nil || len(decoded) != 32 || seen[secret] {
			return ErrBundle
		}
		seen[secret] = true
	}
	cost, e := bcrypt.Cost([]byte(s.AdminPasswordHash))
	if e != nil || cost < 12 {
		return ErrBundle
	}
	pub, e := base64.StdEncoding.DecodeString(s.PluginPublicKey)
	if e != nil || len(pub) != ed25519.PublicKeySize {
		return ErrBundle
	}
	if !separateBundleKeys(c.ControlTLS, c.PublicTLS) {
		return ErrBundle
	}
	if c.productionMode() {
		decoded, e := base64.RawURLEncoding.DecodeString(s.Gateway)
		if e != nil || len(decoded) != 32 || seen[s.Gateway] {
			return ErrBundle
		}
		return c.validateProduction()
	}
	if validateBundleTLS(c.ControlTLS, tenancy.EnterpriseIdentity(c.TenantID), true) != nil || validateBundleTLS(c.PublicTLS, "", false) != nil {
		return ErrBundle
	}
	return nil
}

func validateBundleTLS(material EnterpriseTLS, identity string, client bool) error {
	return validateBundleTLSHost(material, identity, client, "127.0.0.1")
}

func separateBundleKeys(a, b EnterpriseTLS) bool {
	first, err := tls.X509KeyPair([]byte(a.Certificate), []byte(a.PrivateKey))
	if err != nil || len(first.Certificate) == 0 {
		return false
	}
	second, err := tls.X509KeyPair([]byte(b.Certificate), []byte(b.PrivateKey))
	if err != nil || len(second.Certificate) == 0 {
		return false
	}
	x, err := x509.ParseCertificate(first.Certificate[0])
	if err != nil {
		return false
	}
	y, err := x509.ParseCertificate(second.Certificate[0])
	if err != nil {
		return false
	}
	px, err := x509.MarshalPKIXPublicKey(x.PublicKey)
	if err != nil {
		return false
	}
	py, err := x509.MarshalPKIXPublicKey(y.PublicKey)
	return err == nil && !bytes.Equal(px, py)
}

func validateBundleTLSHost(material EnterpriseTLS, identity string, client bool, hostname string) error {
	pair, e := tls.X509KeyPair([]byte(material.Certificate), []byte(material.PrivateKey))
	if e != nil || len(pair.Certificate) == 0 {
		return ErrBundle
	}
	leaf, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil || leaf.IsCA || (identity != "" && (len(leaf.URIs) != 1 || leaf.URIs[0].String() != identity)) {
		return ErrBundle
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(material.CA)) {
		return ErrBundle
	}
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		c, e := x509.ParseCertificate(der)
		if e != nil {
			return ErrBundle
		}
		intermediates.AddCert(c)
	}
	options := x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: hostname, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if _, e = leaf.Verify(options); e != nil {
		return ErrBundle
	}
	if client {
		options.KeyUsages = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		if _, e = leaf.Verify(options); e != nil {
			return ErrBundle
		}
	}
	return nil
}

// BuildEnterpriseBundle constructs nine real services. Sensitive material stays
// in this private compose artifact. The public catalog contains hashes only.
func BuildEnterpriseBundle(c EnterpriseConfig, release Release) ([]byte, Release, error) {
	if c.Validate() != nil || release.Runtime != "linux/amd64" || release.SchemaVersion != 79 || !tenancy.ValidID(release.ID) {
		return nil, Release{}, ErrBundle
	}
	if (release.TenantID != "" || release.ServerID != "") && !release.MatchesTarget(c.TenantID, c.ServerID) {
		return nil, Release{}, ErrBundle
	}
	release.TenantID, release.ServerID = c.TenantID, c.ServerID
	if release.IsolationMode != "" && release.IsolationMode != c.isolationMode() {
		return nil, Release{}, ErrBundle
	}
	if c.productionMode() {
		release.IsolationMode = "dedicated_host"
	}
	if c.sharedIngress() {
		release.IngressMode = SharedIngressMode
	} else if release.IngressMode != "" {
		return nil, Release{}, ErrBundle
	}
	b := Bundle{Services: map[string]Service{}, Networks: map[string]LocalNetwork{"business": {Internal: true}, "edge": {}}, Volumes: map[string]LocalVolume{}}
	volume := func(name, target string, readOnly bool) Volume {
		b.Volumes[name] = LocalVolume{}
		return Volume{Type: "volume", Source: name, Target: target, ReadOnly: readOnly}
	}
	health := func(command ...string) *Healthcheck {
		return &Healthcheck{Test: append([]string{"CMD"}, command...), Interval: "2s", Timeout: "3s", Retries: 50, StartPeriod: "10s"}
	}
	base := func(image string) Service {
		return Service{Image: image, Platform: release.Runtime, Networks: []string{"business"}, Restart: "unless-stopped", SecurityOpt: []string{"no-new-privileges:true"}}
	}
	depends := func(names ...string) map[string]Dependency {
		out := map[string]Dependency{}
		for _, name := range names {
			out[name] = Dependency{Condition: "service_healthy"}
		}
		return out
	}
	port := func(target, published int, protocol string) Port {
		host := "127.0.0.1"
		if c.Production != nil {
			host = c.Production.PublicBindIP
			if published == c.Ports.Control || (c.sharedIngress() && (published == c.Ports.HTTP || published == c.Ports.Media)) {
				host = c.Production.ControlBindIP
			}
		}
		return Port{Target: target, Published: strconv.Itoa(published), HostIP: host, Protocol: protocol}
	}
	role := func(name string, files map[string]string) Service {
		s := base(c.ToolsImage)
		s.Entrypoint = []string{"/opt/frogim/tenant-runtime", name}
		s.User, s.ReadOnly = "10001:10001", true
		s.CapDrop, s.Tmpfs = []string{"ALL"}, []string{"/tmp:mode=1777", "/config:uid=10001,gid=10001,mode=0700", "/caddy-data:uid=10001,gid=10001,mode=0700", "/caddy-config:uid=10001,gid=10001,mode=0700"}
		s.Environment = map[string]string{}
		if files != nil {
			encoded, _ := json.Marshal(files)
			s.Environment[RuntimeFilesEnvironment] = string(encoded)
		}
		s.Healthcheck = health("/opt/frogim/tenant-runtime", "health", name)
		return s
	}
	s := base("postgres@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193")
	s.Environment = map[string]string{"POSTGRES_USER": "enterprise", "POSTGRES_DB": "enterprise", "POSTGRES_PASSWORD": c.Secrets.Database}
	s.Volumes, s.Healthcheck = []Volume{volume("postgres", "/var/lib/postgresql/data", false)}, health("pg_isready", "-U", "enterprise", "-d", "enterprise")
	b.Services["enterprise-db"] = s
	s = base("redis@sha256:978f0e01593e65eed801f2402944efcd936d43b5027e4908a7897baf88ed6241")
	s.Environment = map[string]string{"REDIS_PASSWORD": c.Secrets.Redis}
	s.Command = []string{"sh", "-c", `exec redis-server --appendonly yes --requirepass "$REDIS_PASSWORD"`}
	s.Healthcheck = health("sh", "-c", `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli ping | grep -qx PONG`)
	s.Volumes = []Volume{volume("redis", "/data", false)}
	b.Services["enterprise-redis"] = s
	s = base("minio/minio@sha256:d249d1fb6966de4d8ad26c04754b545205ff15a62e4fd19ebd0f26fa5baacbc0")
	s.Environment = map[string]string{"MINIO_ROOT_USER": "tenantmedia", "MINIO_ROOT_PASSWORD": c.Secrets.Media}
	s.Command = []string{"server", "/data", "--console-address", ":9001"}
	s.Healthcheck = health("curl", "-f", "http://127.0.0.1:9000/minio/health/live")
	s.Volumes = []Volume{volume("media", "/data", false)}
	b.Services["enterprise-minio"] = s
	s = base("minio/mc@sha256:fb8f773eac8ef9d6da0486d5dec2f42f219358bcb8de579d1623d518c9ebd4cc")
	s.User, s.ReadOnly, s.CapDrop, s.Tmpfs = "10001:10001", true, []string{"ALL"}, []string{"/tmp:mode=1777"}
	s.Environment = map[string]string{"MINIO_ROOT_USER": "tenantmedia", "MINIO_ROOT_PASSWORD": c.Secrets.Media, "HOME": "/tmp", "MC_CONFIG_DIR": "/tmp/mc", "MEDIA_BUCKET": c.mediaBucket()}
	s.Entrypoint = []string{"sh", "-c", `mc alias set local http://enterprise-minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null && mc mb --ignore-existing "local/$MEDIA_BUCKET" >/dev/null && mc anonymous set none "local/$MEDIA_BUCKET" >/dev/null && touch /tmp/media-ready || exit 1; trap 'exit 0' TERM INT; while :; do sleep 3600 & wait $!; done`}
	s.DependsOn, s.Healthcheck = depends("enterprise-minio"), health("test", "-f", "/tmp/media-ready")
	b.Services["enterprise-media-init"] = s
	s = role("plugins", nil)
	s.Volumes = []Volume{volume("plugins", "/plugins", false)}
	b.Services["enterprise-plugins"] = s
	s = role("api", map[string]string{"ca.pem": c.ControlTLS.CA, "control.pem": c.ControlTLS.Certificate, "control.key": c.ControlTLS.PrivateKey})
	for key, value := range c.apiEnvironment() {
		s.Environment[key] = value
	}
	s.Networks = []string{"business", "edge"}
	s.Volumes = []Volume{volume("plugins", "/plugins", false)}
	s.Ports = []Port{port(8444, c.Ports.Control, "tcp")}
	s.DependsOn = depends("enterprise-db", "enterprise-redis", "enterprise-media-init", "enterprise-plugins")
	b.Services["enterprise-api"] = s
	s = role("im", map[string]string{"wk.yaml": enterpriseWKConfig})
	for key, value := range map[string]string{"WK_MANAGERTOKEN": c.Secrets.IMManager, "WK_TOKENAUTHON": "true", "WK_EXTERNAL_IP": "127.0.0.1", "WK_EXTERNAL_TCPADDR": fmt.Sprintf("tcp://127.0.0.1:%d", c.Ports.IM), "WK_EXTERNAL_WSADDR": strings.Replace(c.PublicURL(), "https:", "wss:", 1) + "/im", "WK_WEBHOOK_GRPCADDR": "enterprise-api:6970", "WK_DATASOURCE_ADDR": "http://enterprise-api:8080/internal/wukong/datasource", "WK_TRACE_PROMETHEUSAPIURL": "", "IM_WUKONG_POLICY_URL": "http://enterprise-api:8080/internal/wukong/policy/send", "IM_WUKONG_POLICY_SECRET": c.Secrets.IMPolicy} {
		s.Environment[key] = value
	}
	// WuKongIM stores plugin runtime state under plugins/plugindata.
	s.Environment["WK_EXTERNAL_IP"] = c.imHost()
	s.Environment["WK_EXTERNAL_TCPADDR"] = "tcp://" + net.JoinHostPort(c.imHost(), strconv.Itoa(c.Ports.IM))
	s.Volumes = []Volume{volume("im", "/data", false), volume("im-logs", "/logs", false), volume("plugins", "/data/plugins", false)}
	s.Networks, s.Ports, s.DependsOn = []string{"business", "edge"}, []Port{port(5100, c.Ports.IM, "tcp")}, depends("enterprise-plugins")
	b.Services["enterprise-im"] = s
	s = role("livekit", map[string]string{"livekit.yaml": fmt.Sprintf(c.liveKitConfiguration(), c.Ports.RTCTCP, c.Ports.RTCUDPFrom, c.Ports.RTCUDPFrom+3)})
	s.Environment["LIVEKIT_KEYS"] = "tenant-local: " + c.Secrets.LiveKit
	s.Ports = []Port{port(c.Ports.RTCTCP, c.Ports.RTCTCP, "tcp")}
	for p := c.Ports.RTCUDPFrom; p < c.Ports.RTCUDPFrom+4; p++ {
		s.Ports = append(s.Ports, port(p, p, "udp"))
	}
	s.Networks = []string{"business", "edge"}
	b.Services["enterprise-livekit"] = s
	s = role("gateway", map[string]string{"public.pem": c.PublicTLS.Certificate, "public.key": c.PublicTLS.PrivateKey, "Caddyfile": c.gatewayConfiguration()})
	if c.productionMode() {
		s.Environment["FROGIM_GATEWAY_SECRET"] = c.Secrets.Gateway
	}
	if c.sharedIngress() {
		s.Environment["FROGIM_EDGE_SECRET"] = c.Production.SharedIngress.Secret
		s.Environment["FROGIM_INGRESS_MODE"] = SharedIngressMode
	}
	s.Ports, s.Networks = []Port{port(8444, c.Ports.HTTP, "tcp"), port(8445, c.Ports.Media, "tcp")}, []string{"business", "edge"}
	s.DependsOn = depends("enterprise-api", "enterprise-im", "enterprise-livekit")
	b.Services["enterprise-gateway"] = s
	if c.SharedDatastores != nil {
		release.DatastoreMode = tenancy.SharedDatastoreMode
		attachSharedEnterprise(&b, c)
	}
	if validateBundle(b, release) != nil {
		return nil, Release{}, ErrBundle
	}
	raw, e := json.MarshalIndent(b, "", "  ")
	if e != nil {
		return nil, Release{}, ErrBundle
	}
	digest := sha256.Sum256(raw)
	release.ComposeSHA256 = hex.EncodeToString(digest[:])
	if !release.Valid() {
		return nil, Release{}, ErrBundle
	}
	return raw, release, nil
}

func (c EnterpriseConfig) apiEnvironment() map[string]string {
	s := c.Secrets
	result := map[string]string{
		"IM_ENV": "development", "IM_TENANCY_PREVIEW": "true", "IM_TENANT_ID": c.TenantID,
		"IM_TENANT_PUBLIC_URL": c.PublicURL(), "IM_PLATFORM_CONTROL_URL": c.PlatformControlURL,
		"IM_TENANT_CONTROL_ADDR": ":8444", "IM_TENANT_CA_FILE": "/config/ca.pem", "IM_TENANT_CERT_FILE": "/config/control.pem", "IM_TENANT_KEY_FILE": "/config/control.key",
		"IM_DATABASE_URL": "postgres://enterprise:" + s.Database + "@enterprise-db:5432/enterprise?sslmode=disable", "IM_REDIS_URL": "redis://:" + s.Redis + "@enterprise-redis:6379/0",
		"IM_JWT_SECRET": s.JWT, "IM_MEDIA_SIGNING_SECRET": s.MediaSigning,
		"IM_LEGACY_MEDIA_SIGNING_SECRET": s.LegacyMediaSigning,
		"IM_ADMIN_USERNAME":              "enterprise-admin", "IM_ADMIN_PASSWORD_HASH": s.AdminPasswordHash, "IM_ADMIN_ID": "enterprise-admin",
		"IM_PUSH_PROVIDER": "noop", "IM_ALLOWED_ORIGINS": c.PlatformWebOrigin + "," + c.PublicURL(),
		"IM_S3_ENDPOINT": "enterprise-minio:9000", "IM_S3_PUBLIC_ENDPOINT": fmt.Sprintf("127.0.0.1:%d", c.Ports.Media), "IM_S3_PUBLIC_SECURE": "true", "IM_S3_ACCESS_KEY": "tenantmedia", "IM_S3_SECRET_KEY": s.Media, "IM_S3_BUCKET": c.mediaBucket(),
		"IM_WUKONG_ENABLED": "true", "IM_WUKONG_API_URL": "http://enterprise-im:5001", "IM_WUKONG_MANAGER_URL": "http://enterprise-im:5300", "IM_WUKONG_MANAGER_TOKEN": s.IMManager, "IM_WUKONG_TOKEN_SECRET": s.IMToken, "IM_WUKONG_POLICY_SECRET": s.IMPolicy,
		"IM_WUKONG_TCP_URL": fmt.Sprintf("tcp://127.0.0.1:%d", c.Ports.IM), "IM_WUKONG_WS_URL": strings.Replace(c.PublicURL(), "https:", "wss:", 1) + "/im",
		"IM_WUKONG_PLUGIN_DIR": "/plugins", "IM_WUKONG_PLUGIN_TRUSTED_KEYS": "bundle-build:" + s.PluginPublicKey, "IM_WUKONG_PLUGIN_ALLOWLIST": "im-policy",
		"IM_LIVEKIT_ENABLED": "true", "IM_LIVEKIT_URL": strings.Replace(c.PublicURL(), "https:", "wss:", 1) + "/livekit", "IM_LIVEKIT_API_URL": "http://enterprise-livekit:7880", "IM_LIVEKIT_API_KEY": "tenant-local", "IM_LIVEKIT_API_SECRET": s.LiveKit,
		"IM_IP_REGION_DIR": "/opt/ip2region",
	}
	result["IM_S3_PUBLIC_ENDPOINT"] = c.mediaHost()
	result["IM_WUKONG_TCP_URL"] = "tcp://" + net.JoinHostPort(c.imHost(), strconv.Itoa(c.Ports.IM))
	if c.productionMode() {
		result["IM_ENV"], result["IM_TENANCY_PREVIEW"], result["IM_PUSH_PROVIDER"] = "production", "false", "platform"
		result["IM_TENANT_DEPLOYMENT_MODE"] = "dedicated_host"
		result["IM_GATEWAY_SECRET"] = c.Secrets.Gateway
	}
	if c.SharedDatastores != nil {
		result["IM_DATASTORE_MODE"] = tenancy.SharedDatastoreMode
		result["IM_DATABASE_URL"] = "postgres://enterprise:" + s.Database + "@shared-postgres:5432/" + c.SharedDatastores.Database + "?sslmode=disable"
		result["IM_REDIS_URL"] = "redis://:" + s.Redis + "@shared-redis:6379/" + strconv.Itoa(c.SharedDatastores.RedisDB)
	}
	return result
}
