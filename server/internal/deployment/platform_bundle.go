package deployment

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/linli/im/server/internal/privatefile"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

// PlatformConfig is an operator-owned private file, never an admin API request.
// There is one platform project. It contains no business database, IM or media.
type PlatformConfig struct {
	SharedIngress     *SharedIngress    `json:"sharedIngress,omitempty"`
	SharedDatastores  bool              `json:"sharedDatastores,omitempty"`
	ToolsImage        string            `json:"toolsImage"`
	PublicURL         string            `json:"publicUrl"`
	ControlURL        string            `json:"controlUrl"`
	PublicBindIP      string            `json:"publicBindIp"`
	ControlBindIP     string            `json:"controlBindIp"`
	DatabaseSecret    string            `json:"databaseSecret"`
	RedisSecret       string            `json:"redisSecret"`
	GatewaySecret     string            `json:"gatewaySecret"`
	AdminUsername     string            `json:"adminUsername"`
	AdminPasswordHash string            `json:"adminPasswordHash"`
	PublicTLS         EnterpriseTLS     `json:"publicTls"`
	ControlTLS        EnterpriseTLS     `json:"controlTls"`
	Peers             []PlatformPeer    `json:"peers"`
	Catalog           []Release         `json:"catalog"`
	Suppliers         map[string]string `json:"suppliers,omitempty"`
	APNSPrivateKey    string            `json:"apnsPrivateKey,omitempty"`
}
type PlatformPeer struct {
	TenantID   string `json:"tenantId"`
	ServerID   string `json:"serverId"`
	ControlURL string `json:"controlUrl"`
	AgentURL   string `json:"agentUrl"`
}
type PlatformRelease struct {
	ID            string `json:"id"`
	Runtime       string `json:"runtime"`
	SchemaVersion int    `json:"schemaVersion"`
	ToolsImage    string `json:"toolsImage"`
	PublicURL     string `json:"publicUrl"`
	ComposeSHA256 string `json:"composeSha256"`
}

func ReadPlatformConfig(reader io.Reader) (PlatformConfig, error) {
	var c PlatformConfig
	b, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		return c, ErrBundle
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Validate() != nil {
		return PlatformConfig{}, ErrBundle
	}
	return c, nil
}

// A private control DNS name may resolve through the operator's private zone.
// Reject obvious public/preview IPs; actual routing/firewall is an external gate.
func productionControlURL(raw string) error {
	if tenancy.ValidateBaseURL(raw, false) != nil {
		return ErrBundle
	}
	u, _ := url.Parse(raw)
	if originPort(raw) < 1024 || originPort(raw) > 65535 {
		return ErrBundle
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
		if !ip.IsPrivate() {
			return ErrBundle
		}
	} else if !strings.Contains(u.Hostname(), ".") || strings.EqualFold(u.Hostname(), "host.docker.internal") || strings.HasSuffix(strings.ToLower(u.Hostname()), ".localhost") {
		return ErrBundle
	}
	return nil
}
func productionPublicBind(raw string) bool {
	ip, err := netip.ParseAddr(raw)
	return err == nil && !ip.IsLoopback() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast() && (ip.IsUnspecified() || ip.IsGlobalUnicast())
}
func (c PlatformConfig) Validate() error {
	if c.SharedIngress != nil && (!c.SharedDatastores || c.SharedIngress.valid() != nil || c.SharedIngress.HTTPSPort < 1024 || c.SharedIngress.HTTPSPort > 65535 || c.SharedIngress.HTTPSPort == originPort(c.ControlURL) || c.SharedIngress.Secret == c.GatewaySecret) {
		return ErrBundle
	}
	if !imageReference.MatchString(c.ToolsImage) || strings.Contains(c.ToolsImage, "..") || tenancy.PublicOrigin(c.PublicURL) != nil || productionControlURL(c.ControlURL) != nil || c.ControlURL == c.PublicURL || !productionPublicBind(c.PublicBindIP) {
		return ErrBundle
	}
	ip, e := netip.ParseAddr(c.ControlBindIP)
	if e != nil || !ip.IsPrivate() || originPort(c.ControlURL) == originPort(c.PublicURL) {
		return ErrBundle
	}
	if !tenancy.ValidID(c.AdminUsername) {
		return ErrBundle
	}
	cost, err := bcrypt.Cost([]byte(c.AdminPasswordHash))
	if err != nil || cost < 12 || c.DatabaseSecret == c.RedisSecret || c.DatabaseSecret == c.GatewaySecret || c.RedisSecret == c.GatewaySecret {
		return ErrBundle
	}
	for _, s := range []string{c.DatabaseSecret, c.RedisSecret, c.GatewaySecret} {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil || len(b) != 32 {
			return ErrBundle
		}
	}
	public, _ := url.Parse(c.PublicURL)
	control, _ := url.Parse(c.ControlURL)
	if !separateBundleKeys(c.PublicTLS, c.ControlTLS) || validateBundleTLSHost(c.PublicTLS, "", false, public.Hostname()) != nil || validateBundleTLSHost(c.ControlTLS, tenancy.PlatformIdentity, true, control.Hostname()) != nil {
		return ErrBundle
	}
	if len(c.Peers) > 100 || len(c.Catalog) > 100 {
		return ErrBundle
	}
	tenants, servers, addresses := map[string]bool{}, map[string]bool{}, map[string]bool{c.ControlURL: true, c.PublicURL: true}
	for _, p := range c.Peers {
		if !tenancy.ValidID(p.TenantID) || !tenancy.ValidID(p.ServerID) || tenants[p.TenantID] || servers[p.ServerID] || p.AgentURL == p.ControlURL || addresses[p.ControlURL] || addresses[p.AgentURL] || productionControlURL(p.ControlURL) != nil || productionControlURL(p.AgentURL) != nil {
			return ErrBundle
		}
		tenants[p.TenantID], servers[p.ServerID], addresses[p.ControlURL], addresses[p.AgentURL] = true, true, true, true
	}
	catalog, _ := json.Marshal(c.Catalog)
	if c.Catalog == nil {
		catalog = []byte("[]")
	}
	if len(c.Catalog) > 0 {
		if _, err = ReadCatalog(bytes.NewReader(catalog)); err != nil {
			return ErrBundle
		}
	}
	for _, r := range c.Catalog {
		if r.IsolationMode != "dedicated_host" || !tenants[r.TenantID] || !servers[r.ServerID] {
			return ErrBundle
		}
		matched := false
		for _, peer := range c.Peers {
			matched = matched || (peer.TenantID == r.TenantID && peer.ServerID == r.ServerID)
		}
		if !matched {
			return ErrBundle
		}
	}
	if c.SharedDatastores && !tenants["default"] {
		return ErrBundle
	}
	return validatePlatformSuppliers(c)
}

func (c PlatformConfig) RuntimeEnvironment() map[string]string {
	env := map[string]string{
		"PLATFORM_ENV": "production", "PLATFORM_DEPLOYMENT_MODE": "dedicated_host",
		"PLATFORM_PUBLIC_URL": c.PublicURL, "PLATFORM_WEB_ORIGIN": c.PublicURL,
		"PLATFORM_ADDR": ":8090", "PLATFORM_CONTROL_ADDR": ":8443",
		"PLATFORM_DATABASE_URL":   "postgres://platform:" + c.DatabaseSecret + "@platform-db:5432/platform?sslmode=disable",
		"PLATFORM_REDIS_URL":      "redis://:" + c.RedisSecret + "@platform-redis:6379/0",
		"PLATFORM_ADMIN_USERNAME": c.AdminUsername, "PLATFORM_ADMIN_PASSWORD_HASH": c.AdminPasswordHash,
		"PLATFORM_CA_FILE": "/config/ca.pem", "PLATFORM_CERT_FILE": "/config/control.pem", "PLATFORM_KEY_FILE": "/config/control.key",
		"PLATFORM_PEERS_FILE": "/config/peers.json", "PLATFORM_AGENTS_FILE": "/config/agents.json",
		"PLATFORM_GATEWAY_SECRET": c.GatewaySecret,
		"PLATFORM_PUSH_PROVIDER":  "disabled", "PLATFORM_WEB_PUSH_ENABLED": "false",
	}
	if len(c.Catalog) > 0 {
		env["PLATFORM_DEPLOYMENT_CATALOG_FILE"] = "/config/catalog.json"
	}
	for key, value := range c.Suppliers {
		env[key] = value
	}
	if c.APNSPrivateKey != "" {
		env["PLATFORM_APNS_VOIP_KEY_FILE"] = "/config/apns.pem"
	}
	if c.SharedDatastores {
		env["PLATFORM_DATASTORE_MODE"] = tenancy.SharedDatastoreMode
		env["PLATFORM_DATABASE_URL"] = "postgres://platform:" + c.DatabaseSecret + "@shared-postgres:5432/platform?sslmode=disable"
		env["PLATFORM_REDIS_URL"] = "redis://:" + c.RedisSecret + "@shared-redis:6379/0"
	}
	return env
}

func BuildPlatformBundle(c PlatformConfig, id string) ([]byte, PlatformRelease, error) {
	if c.Validate() != nil || !tenancy.ValidID(id) {
		return nil, PlatformRelease{}, ErrBundle
	}
	health := func(args ...string) *Healthcheck {
		return &Healthcheck{Test: append([]string{"CMD"}, args...), Interval: "3s", Timeout: "3s", Retries: 40, StartPeriod: "10s"}
	}
	base := func(image string) Service {
		return Service{Image: image, Platform: "linux/amd64", Networks: []string{"platform"}, Restart: "unless-stopped", SecurityOpt: []string{"no-new-privileges:true"}}
	}
	b := Bundle{Services: map[string]Service{}, Networks: map[string]LocalNetwork{"platform": {Internal: true}, "edge": {}}, Volumes: map[string]LocalVolume{"postgres": {}, "redis": {}}}
	s := base("postgres@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193")
	s.Environment = map[string]string{"POSTGRES_USER": "platform", "POSTGRES_DB": "platform", "POSTGRES_PASSWORD": c.DatabaseSecret}
	s.Volumes, s.Healthcheck = []Volume{{Type: "volume", Source: "postgres", Target: "/var/lib/postgresql/data"}}, health("pg_isready", "-U", "platform", "-d", "platform")
	b.Services["platform-db"] = s
	s = base("redis@sha256:978f0e01593e65eed801f2402944efcd936d43b5027e4908a7897baf88ed6241")
	s.Environment = map[string]string{"REDIS_PASSWORD": c.RedisSecret}
	s.Command = []string{"sh", "-c", `exec redis-server --appendonly yes --requirepass "$REDIS_PASSWORD"`}
	s.Volumes, s.Healthcheck = []Volume{{Type: "volume", Source: "redis", Target: "/data"}}, health("sh", "-c", `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli ping | grep -qx PONG`)
	b.Services["platform-redis"] = s
	role := func(name string, files map[string]string) Service {
		s := base(c.ToolsImage)
		s.Entrypoint, s.User, s.ReadOnly = []string{"/opt/frogim/tenant-runtime", name}, "10001:10001", true
		s.CapDrop, s.Tmpfs = []string{"ALL"}, []string{"/tmp:mode=1777", "/config:uid=10001,gid=10001,mode=0700", "/caddy-data:uid=10001,gid=10001,mode=0700", "/caddy-config:uid=10001,gid=10001,mode=0700"}
		encoded, _ := json.Marshal(files)
		s.Environment = map[string]string{RuntimeFilesEnvironment: string(encoded)}
		s.Networks, s.Healthcheck = []string{"platform", "edge"}, health("/opt/frogim/tenant-runtime", "health", name)
		return s
	}
	peers := []map[string]string{}
	agents := []PeerConfig{}
	for _, p := range c.Peers {
		peers = append(peers, map[string]string{"tenantId": p.TenantID, "controlUrl": p.ControlURL})
		agents = append(agents, PeerConfig{ServerID: p.ServerID, ControlURL: p.AgentURL, CAFile: "/config/ca.pem", CertFile: "/config/control.pem", KeyFile: "/config/control.key"})
	}
	encode := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	catalog := c.Catalog
	if catalog == nil {
		catalog = []Release{}
	}
	key := c.APNSPrivateKey
	if key == "" {
		key = "provider-disabled\n"
	}
	s = role("platform", map[string]string{"ca.pem": c.ControlTLS.CA, "control.pem": c.ControlTLS.Certificate, "control.key": c.ControlTLS.PrivateKey, "peers.json": encode(peers), "agents.json": encode(agents), "catalog.json": encode(catalog), "apns.pem": key})
	for k, v := range c.RuntimeEnvironment() {
		s.Environment[k] = v
	}
	s.Ports = []Port{{Target: 8443, Published: strconv.Itoa(originPort(c.ControlURL)), HostIP: c.ControlBindIP, Protocol: "tcp"}}
	s.DependsOn = map[string]Dependency{"platform-db": {Condition: "service_healthy"}, "platform-redis": {Condition: "service_healthy"}}
	b.Services["platform-api"] = s
	s = role("platform-gateway", map[string]string{"public.pem": c.PublicTLS.Certificate, "public.key": c.PublicTLS.PrivateKey, "Caddyfile": c.gatewayConfiguration()})
	s.Environment["FROGIM_PLATFORM_PUBLIC_URL"] = c.AuthURL()
	s.Environment["FROGIM_GATEWAY_SECRET"] = c.GatewaySecret
	s.Ports = []Port{{Target: 8443, Published: strconv.Itoa(originPort(c.PublicURL)), HostIP: c.PublicBindIP, Protocol: "tcp"}}
	if c.SharedIngress != nil {
		s.Ports[0].HostIP = c.ControlBindIP
		s.Ports[0].Published = strconv.Itoa(c.SharedIngress.HTTPSPort)
		s.Environment["FROGIM_EDGE_SECRET"] = c.SharedIngress.Secret
	}
	s.DependsOn = map[string]Dependency{"platform-api": {Condition: "service_healthy"}}
	b.Services["platform-gateway"] = s
	if c.SharedDatastores {
		delete(b.Services, "platform-db")
		delete(b.Services, "platform-redis")
		delete(b.Volumes, "postgres")
		delete(b.Volumes, "redis")
		b.Networks["shared-data"] = LocalNetwork{External: true, Name: tenancy.SharedDataNetwork}
		s = b.Services["platform-api"]
		s.Networks = append(s.Networks, "shared-data")
		s.DependsOn = nil
		b.Services["platform-api"] = s
	}
	// This operator artifact is directly consumable by Compose; protect every
	// dollar from host .env interpolation (bcrypt and container-side commands).
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, PlatformRelease{}, ErrBundle
	}
	raw = bytes.ReplaceAll(raw, []byte("$"), []byte("$$"))
	digest := sha256.Sum256(raw)
	r := PlatformRelease{ID: id, Runtime: "linux/amd64", SchemaVersion: tenancy.PlatformSchemaVersion, ToolsImage: c.ToolsImage, PublicURL: c.PublicURL, ComposeSHA256: hex.EncodeToString(digest[:])}
	return raw, r, nil
}

// No Docker invocation or deployment. Immutable output includes no live data;
// protect the Compose file like a credential file, not a public release manifest.
func WritePlatformRelease(root string, c PlatformConfig, id string) (PlatformRelease, error) {
	raw, release, err := BuildPlatformBundle(c, id)
	if err != nil || !filepath.IsAbs(root) {
		return PlatformRelease{}, ErrBundle
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return PlatformRelease{}, ErrBundle
	}
	bounded, err := os.OpenRoot(root)
	if err != nil {
		return PlatformRelease{}, ErrBundle
	}
	defer bounded.Close()
	manifest, _ := json.MarshalIndent(release, "", "  ")
	if existing, err := bounded.Lstat(id); err == nil {
		if !existing.IsDir() || existing.Mode()&os.ModeSymlink != 0 {
			return PlatformRelease{}, ErrBundle
		}
		for name, expected := range map[string][]byte{"compose.json": raw, "release.json": manifest} {
			info, e := bounded.Lstat(filepath.Join(id, name))
			if e != nil || !info.Mode().IsRegular() {
				return PlatformRelease{}, ErrBundle
			}
			actual, e := bounded.ReadFile(filepath.Join(id, name))
			if e != nil || !bytes.Equal(actual, expected) {
				return PlatformRelease{}, ErrBundle
			}
		}
		return release, nil
	} else if !os.IsNotExist(err) {
		return PlatformRelease{}, ErrBundle
	}
	if bounded.Mkdir(id, 0700) != nil {
		return PlatformRelease{}, ErrBundle
	}
	for _, file := range []struct {
		name    string
		content []byte
	}{{"compose.json", raw}, {"release.json", manifest}} {
		f, e := privatefile.Create(filepath.Join(root, id, file.name))
		if e != nil {
			return PlatformRelease{}, ErrBundle
		}
		_, e = f.Write(file.content)
		syncErr, closeErr := f.Sync(), f.Close()
		if e != nil || syncErr != nil || closeErr != nil {
			return PlatformRelease{}, ErrBundle
		}
	}
	return release, nil
}

// Build metadata is baked next to the compiled Flutter app. A wrong platform
// address cannot silently serve a client which authenticates against another
// directory. The image itself is pinned by digest in the private artifact.
func ValidatePlatformWebMetadata(raw []byte, publicURL string) error {
	var m struct {
		PlatformURL string `json:"platformUrl"`
		Environment string `json:"environment"`
		BaseHref    string `json:"baseHref"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF || m.PlatformURL != publicURL || tenancy.PublicPlatformURL(publicURL) != nil || m.Environment != "production" || m.BaseHref != "/app/" {
		return ErrBundle
	}
	return nil
}
