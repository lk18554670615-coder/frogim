package deployment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

// Release metadata is operator-owned, immutable configuration on BOTH sides.
// Public requests select an ID + digest; they cannot supply a compose document.
type Release struct {
	IngressMode   string   `json:"ingressMode,omitempty"`
	DatastoreMode string   `json:"datastoreMode,omitempty"`
	ID            string   `json:"id"`
	Sequence      int64    `json:"sequence"`
	Runtime       string   `json:"runtime"`
	ComposeSHA256 string   `json:"composeSha256"`
	SchemaVersion int      `json:"schemaVersion"`
	RollbackTo    []string `json:"rollbackTo"`
	TenantID      string   `json:"tenantId,omitempty"`
	ServerID      string   `json:"serverId,omitempty"`
	IsolationMode string   `json:"isolationMode,omitempty"`
}

func (r Release) Valid() bool {
	if r.IngressMode != "" && (r.IngressMode != SharedIngressMode || r.IsolationMode != "dedicated_host" || r.DatastoreMode != tenancy.SharedDatastoreMode || r.TenantID != "default") {
		return false
	}
	if r.DatastoreMode != "" && (r.DatastoreMode != tenancy.SharedDatastoreMode || r.TenantID != "default") {
		return false
	}
	if (r.IsolationMode != "" && r.IsolationMode != "local_preview" && r.IsolationMode != "dedicated_host") || (r.IsolationMode == "dedicated_host" && (r.TenantID == "" || r.ServerID == "")) {
		return false
	}
	if !tenancy.ValidID(r.ID) || r.Sequence < 1 || (r.Runtime != "linux/amd64" && r.Runtime != "linux/arm64") || !fingerprint.MatchString(r.ComposeSHA256) || r.SchemaVersion < 73 || len(r.RollbackTo) > 128 {
		return false
	}
	if (r.TenantID != "" || r.ServerID != "") && (!tenancy.ValidID(r.TenantID) || !tenancy.ValidID(r.ServerID)) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range r.RollbackTo {
		if !tenancy.ValidID(id) || id == r.ID || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// Generic unbound bundles remain useful for isolated executor fixtures, but
// the platform never accepts them as an enterprise deployment target.
func (r Release) MatchesTarget(tenant, server string) bool {
	return r.TenantID == tenant && r.ServerID == server && r.TenantID != "" && r.ServerID != ""
}

func (r Release) Digest() string {
	copy := r
	copy.RollbackTo = append([]string{}, r.RollbackTo...)
	sort.Strings(copy.RollbackTo)
	b, _ := json.Marshal(copy)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func ReadCatalog(r io.Reader) (map[string]Release, error) {
	b, e := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if e != nil || len(b) > 1<<20 {
		return nil, tenancy.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var list []Release
	if d.Decode(&list) != nil || d.Decode(new(any)) != io.EOF || len(list) == 0 || len(list) > 1000 {
		return nil, tenancy.ErrInvalid
	}
	result := map[string]Release{}
	sequences := map[string]bool{}
	for _, release := range list {
		sequenceKey := release.Runtime + ":" + release.TenantID + ":" + release.ServerID + ":" + strconv.FormatInt(release.Sequence, 10)
		if !release.Valid() || result[release.ID].ID != "" || sequences[sequenceKey] {
			return nil, tenancy.ErrInvalid
		}
		result[release.ID] = release
		sequences[sequenceKey] = true
	}
	for _, release := range list {
		for _, id := range release.RollbackTo {
			from, ok := result[id]
			// No implicit database downgrade, even if the old image still exists.
			if !ok || from.Runtime != release.Runtime || from.SchemaVersion != release.SchemaVersion || from.Sequence >= release.Sequence || from.TenantID != release.TenantID || from.ServerID != release.ServerID || from.isolationMode() != release.isolationMode() || from.DatastoreMode != release.DatastoreMode || from.IngressMode != release.IngressMode {
				return nil, tenancy.ErrInvalid
			}
		}
	}
	return result, nil
}

var imageReference = regexp.MustCompile(`^[a-z0-9][a-z0-9./_-]*@sha256:[a-f0-9]{64}$`)
var serviceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// A bundle is a self-contained Compose JSON document. Network/volume names are
// project-scoped; only the default enterprise shared-data network may be
// external. Includes, builds and host namespaces are deliberately unavailable. Commands *inside* its operator-authored containers
// are part of the pinned artifact, never request input.
type Bundle struct {
	Services map[string]Service      `json:"services"`
	Networks map[string]LocalNetwork `json:"networks,omitempty"`
	Volumes  map[string]LocalVolume  `json:"volumes,omitempty"`
}
type LocalNetwork struct {
	External bool   `json:"external,omitempty"`
	Name     string `json:"name,omitempty"`
	Internal bool   `json:"internal,omitempty"`
}
type LocalVolume struct{}
type Service struct {
	Image       string                `json:"image"`
	Platform    string                `json:"platform"`
	Entrypoint  []string              `json:"entrypoint,omitempty"`
	Command     []string              `json:"command,omitempty"`
	Environment map[string]string     `json:"environment,omitempty"`
	Networks    []string              `json:"networks,omitempty"`
	Ports       []Port                `json:"ports,omitempty"`
	Volumes     []Volume              `json:"volumes,omitempty"`
	Restart     string                `json:"restart,omitempty"`
	ReadOnly    bool                  `json:"read_only,omitempty"`
	User        string                `json:"user,omitempty"`
	CapDrop     []string              `json:"cap_drop,omitempty"`
	SecurityOpt []string              `json:"security_opt,omitempty"`
	Tmpfs       []string              `json:"tmpfs,omitempty"`
	Healthcheck *Healthcheck          `json:"healthcheck"`
	DependsOn   map[string]Dependency `json:"depends_on,omitempty"`
}
type Port struct {
	Target    int    `json:"target"`
	Published string `json:"published"`
	HostIP    string `json:"host_ip"`
	Protocol  string `json:"protocol"`
}
type Volume struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
}
type Healthcheck struct {
	Test        []string `json:"test"`
	Interval    string   `json:"interval"`
	Timeout     string   `json:"timeout"`
	Retries     int      `json:"retries"`
	StartPeriod string   `json:"start_period,omitempty"`
}
type Dependency struct {
	Condition string `json:"condition"`
}

func readBundle(root, release string, digest string) ([]byte, Bundle, error) {
	var bundle Bundle
	if !filepath.IsAbs(root) || !tenancy.ValidID(release) || !fingerprint.MatchString(digest) {
		return nil, bundle, tenancy.ErrInvalid
	}
	// No symlink/junction escape from the operator's immutable bundle tree.
	bounded, e := os.OpenRoot(root)
	if e != nil {
		return nil, bundle, ErrBundle
	}
	defer bounded.Close()
	info, e := bounded.Lstat(release)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, bundle, ErrBundle
	}
	path := filepath.Join(release, "compose.json")
	info, e = bounded.Lstat(path)
	if e != nil || !info.Mode().IsRegular() {
		return nil, bundle, ErrBundle
	}
	f, e := bounded.Open(path)
	if e != nil {
		return nil, bundle, ErrBundle
	}
	defer f.Close()
	info, e = f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return nil, bundle, ErrBundle
	}
	b, e := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if e != nil || len(b) > 2<<20 {
		return nil, bundle, ErrBundle
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != digest {
		return nil, bundle, ErrBundle
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&bundle) != nil || d.Decode(new(any)) != io.EOF || len(bundle.Services) == 0 || len(bundle.Services) > 20 {
		return nil, bundle, ErrBundle
	}
	return b, bundle, nil
}

func validateBundle(b Bundle, r Release) error {
	if r.isolationMode() == "dedicated_host" && validateProductionBundle(b, r) != nil {
		return ErrBundle
	}
	if sharedBundle(b) && validateSharedBundle(b, r) != nil {
		return ErrBundle
	}
	if sharedBundle(b) != (r.DatastoreMode == tenancy.SharedDatastoreMode) {
		return ErrBundle
	}
	for name, network := range b.Networks {
		if network.External || network.Name != "" {
			if !sharedBundle(b) || r.TenantID != "default" || name != "shared-data" || !network.External || network.Internal || network.Name != tenancy.SharedDataNetwork {
				return ErrBundle
			}
		}
		if !serviceName.MatchString(name) {
			return ErrBundle
		}
	}
	for name := range b.Volumes {
		if !serviceName.MatchString(name) {
			return ErrBundle
		}
	}
	for name, s := range b.Services {
		if !serviceName.MatchString(name) || !imageReference.MatchString(s.Image) || strings.Contains(s.Image, "..") || s.Platform != r.Runtime || s.Healthcheck == nil || len(s.Healthcheck.Test) < 2 || (s.Healthcheck.Test[0] != "CMD" && s.Healthcheck.Test[0] != "CMD-SHELL") || s.Healthcheck.Retries < 1 || s.Healthcheck.Retries > 60 || len(s.Networks) == 0 {
			return ErrBundle
		}
		for _, duration := range []string{s.Healthcheck.Interval, s.Healthcheck.Timeout} {
			parsed, e := time.ParseDuration(duration)
			if e != nil || parsed < time.Millisecond || parsed > time.Minute {
				return ErrBundle
			}
		}
		if s.Healthcheck.StartPeriod != "" {
			parsed, e := time.ParseDuration(s.Healthcheck.StartPeriod)
			if e != nil || parsed < 0 || parsed > 2*time.Minute {
				return ErrBundle
			}
		}
		for _, network := range s.Networks {
			if _, ok := b.Networks[network]; !ok {
				return ErrBundle
			}
		}
		for _, p := range s.Ports {
			published, portErr := strconv.Atoi(p.Published)
			bindingAllowed := p.HostIP == "127.0.0.1" || p.HostIP == "::1"
			if r.isolationMode() == "dedicated_host" {
				bindingAllowed = productionPort(name, p)
			}
			if p.Target < 1 || p.Target > 65535 || portErr != nil || published < 1 || published > 65535 || !bindingAllowed || !regexp.MustCompile(`^[0-9]{1,5}$`).MatchString(p.Published) || (p.Protocol != "tcp" && p.Protocol != "udp") {
				return ErrBundle
			}
		}
		for _, v := range s.Volumes {
			// Host bind mounts (including the Docker socket), devices and remote
			// volume drivers are not accepted through this release format.
			if v.Type != "volume" || !strings.HasPrefix(v.Target, "/") || strings.Contains(v.Target, "..") {
				return ErrBundle
			}
			if _, ok := b.Volumes[v.Source]; !ok {
				return ErrBundle
			}
		}
		for dependency, d := range s.DependsOn {
			if _, ok := b.Services[dependency]; !ok || dependency == name || d.Condition != "service_healthy" {
				return ErrBundle
			}
		}
	}
	return nil
}
