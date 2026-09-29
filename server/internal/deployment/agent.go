// Package deployment owns the restricted host-agent protocol. It does not expose
// an arbitrary process runner, filesystem path, Docker socket or shell command.
package deployment

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/linli/im/server/internal/tenancy"
)

const Protocol = 1

func AgentIdentity(id string) string { return "spiffe://frogim/agent/" + id }

var fingerprint = regexp.MustCompile(`^[a-f0-9]{64}$`)
var noncePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{43}$`)

type Inspection struct {
	Nonce           string   `json:"nonce"`
	Protocol        int      `json:"protocol"`
	ServerID        string   `json:"serverId"`
	TenantID        string   `json:"tenantId"`
	HTTPBaseURL     string   `json:"httpBaseUrl"`
	HostFingerprint string   `json:"hostFingerprint"`
	IsolationMode   string   `json:"isolationMode"`
	Runtime         string   `json:"runtime"`
	Capabilities    []string `json:"capabilities"`
}

func (r Inspection) Valid(nonce, server, tenant, address string) bool {
	return noncePattern.MatchString(nonce) && r.Nonce == nonce && r.Protocol == Protocol && tenancy.ValidID(server) && r.ServerID == server &&
		r.TenantID == tenant && r.HTTPBaseURL == address && fingerprint.MatchString(r.HostFingerprint) &&
		(r.IsolationMode == "local_preview" || r.IsolationMode == "dedicated_host") &&
		(r.Runtime == "linux/amd64" || r.Runtime == "linux/arm64") &&
		(slices.Equal(r.Capabilities, []string{"inspect"}) || slices.Equal(r.Capabilities, []string{"inspect", "deploy"}) || slices.Equal(r.Capabilities, []string{"inspect", "deploy", "backup"}))
}

// A host fingerprint detects accidental reuse; it is not cloud attestation.
// local_preview IDs explicitly do not claim physical host isolation.
func New(server, tenant, address, mode string, hostID []byte) (*Agent, error) {
	if !tenancy.ValidID(server) || !tenancy.ValidID(tenant) || tenancy.ValidateBaseURL(address, false) != nil ||
		(mode != "local_preview" && mode != "dedicated_host") {
		return nil, tenancy.ErrInvalid
	}
	id := strings.TrimSpace(string(hostID))
	if len(id) < 32 || len(id) > 128 || !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(id) {
		return nil, tenancy.ErrInvalid
	}
	if mode == "dedicated_host" && !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(id) {
		return nil, tenancy.ErrInvalid
	}
	digest := sha256.Sum256([]byte("frogim-host-v1:" + id))
	return &Agent{report: Inspection{Protocol: Protocol, ServerID: server, TenantID: tenant, HTTPBaseURL: address, HostFingerprint: hex.EncodeToString(digest[:]), IsolationMode: mode, Runtime: runtime.GOOS + "/" + runtime.GOARCH, Capabilities: []string{"inspect"}}}, nil
}

type Agent struct {
	report   Inspection
	executor *Executor
}

func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	if a.executor != nil {
		a.deploymentRoutes(mux)
		if a.executor.backupIO != nil {
			a.backupRoutes(mux)
		}
	}
	mux.HandleFunc("POST /internal/agent/inspect", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Nonce string `json:"nonce"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || !noncePattern.MatchString(in.Nonce) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		report := a.report
		report.Nonce = in.Nonce
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(report)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if tenancy.PeerIdentity(r) != tenancy.PlatformIdentity {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

type PeerConfig struct {
	ServerID   string `json:"serverId"`
	ControlURL string `json:"controlUrl"`
	CAFile     string `json:"caFile"`
	CertFile   string `json:"certFile"`
	KeyFile    string `json:"keyFile"`
}

// Only local operator-owned configuration can supply control endpoints and
// client keys. Browser requests choose a preconfigured ID, never a URL.
func ReadPeers(reader io.Reader) (map[string]*tenancy.RPC, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, tenancy.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var entries []PeerConfig
	if d.Decode(&entries) != nil || d.Decode(new(any)) != io.EOF || len(entries) > 1000 {
		return nil, tenancy.ErrInvalid
	}
	peers := map[string]*tenancy.RPC{}
	addresses := map[string]bool{}
	for _, e := range entries {
		if !tenancy.ValidID(e.ServerID) || peers[e.ServerID] != nil || addresses[e.ControlURL] {
			return nil, tenancy.ErrInvalid
		}
		cfg, err := tenancy.TLSConfig(e.CAFile, e.CertFile, e.KeyFile)
		if err != nil {
			return nil, errors.New("agent certificate configuration unavailable")
		}
		if !LocalIdentity(cfg, tenancy.PlatformIdentity) {
			return nil, errors.New("agent client certificate identity mismatch")
		}
		rpc, err := tenancy.NewRPC(e.ControlURL, cfg, AgentIdentity(e.ServerID))
		if err != nil {
			return nil, tenancy.ErrInvalid
		}
		peers[e.ServerID] = rpc
		addresses[e.ControlURL] = true
	}
	return peers, nil
}

// Validate local credentials too: a misplaced enterprise certificate must not
// turn into an agent or platform certificate merely through configuration.
func LocalIdentity(cfg *tls.Config, expected string) bool {
	if cfg == nil || len(cfg.Certificates) != 1 || len(cfg.Certificates[0].Certificate) == 0 {
		return false
	}
	leaf, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
	return err == nil && len(leaf.URIs) == 1 && leaf.URIs[0].String() == expected
}
