package directorybackup

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

func TestRecoveryReviewPostgresRealOperatorCLI(t *testing.T) {
	db, request, inventory := stagedReviewFixture(t)
	binary := os.Getenv("TENANCY_PLATFORM_BACKUP_CLI")
	if !filepath.IsAbs(binary) {
		t.Skip("explicit compiled helper required")
	}
	root := t.TempDir()
	write := func(name string, raw []byte) string {
		t.Helper()
		path := filepath.Join(root, name)
		if os.WriteFile(path, raw, 0600) != nil {
			t.Fatal("private fixture write")
		}
		return path
	}
	caPub, caKey, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "disposable recovery fixture"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, caPub, caKey)
	if e != nil {
		t.Fatal(e)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	issue := func(role string, n int64) ([]byte, []byte) {
		t.Helper()
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		uri, _ := url.Parse(role)
		leaf := &x509.Certificate{SerialNumber: big.NewInt(n), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}, URIs: []*url.URL{uri}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
		der, e := x509.CreateCertificate(rand.Reader, leaf, ca, pub, caKey)
		if e != nil {
			t.Fatal(e)
		}
		keyDER, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			t.Fatal(e)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	}
	cert, key := issue(tenancy.PlatformIdentity, 2)
	tenantCert, tenantKey := issue(tenancy.EnterpriseIdentity("default"), 3)
	pair, e := tls.X509KeyPair(tenantCert, tenantKey)
	if e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	var rejectAuthority atomic.Bool
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tenancy.PeerIdentity(r) != tenancy.PlatformIdentity || r.Method != "POST" {
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/internal/tenancy/recovery/authority" {
			if rejectAuthority.Load() {
				w.WriteHeader(409)
				return
			}
			h := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
			_ = json.NewEncoder(w).Encode(tenancy.RecoveryAuthority{TenantID: "default", PlatformControlURL: "https://new-platform.example.test", AuthoritiesPEM: string(caPEM), ClientCertificateSHA256: hex.EncodeToString(h[:])})
			return
		}
		if r.URL.Path != "/internal/tenancy/recovery/inventory" {
			w.WriteHeader(404)
			return
		}
		var query tenancy.RecoveryInventoryRequest
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&query) != nil {
			w.WriteHeader(400)
			return
		}
		calls.Add(1)
		page, e := inventory.Page(query)
		if e != nil {
			w.WriteHeader(409)
			return
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}
	server.StartTLS()
	defer server.Close()
	request.Peers[0].ControlURL = server.URL
	raw, _ := json.Marshal(request)
	config := map[string]any{}
	_ = json.Unmarshal(raw, &config)
	config["databaseUrl"] = db.DSN
	config["caFile"] = write("ca.pem", caPEM)
	config["certificateFile"] = write("platform.pem", cert)
	config["privateKeyFile"] = write("platform-key.pem", key)
	raw, _ = json.Marshal(config)
	file := write("review.json", raw)
	run := func(mode string) ReviewResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "-mode", mode, "-config", file, "-confirmed")
		command.Stderr = io.Discard
		out, e := command.Output()
		if e != nil {
			t.Fatal("compiled recovery operator failed", mode)
		}
		for _, secret := range []string{db.DSN, root, "19911112222", "PRIVATE KEY", "fixture-hash-not-a-real-password"} {
			if bytes.Contains(out, []byte(secret)) {
				t.Fatal("CLI disclosed private configuration/evidence")
			}
		}
		var result ReviewResult
		if json.Unmarshal(out, &result) != nil || result.State != "completed" || result.ActivationAllowed {
			t.Fatal("invalid result")
		}
		return result
	}
	first := run("recovery-review")
	second := run("recovery-review")
	status := run("recovery-review-status")
	if first.EvidenceDigest != second.EvidenceDigest || first.EvidenceDigest != status.EvidenceDigest || calls.Load() != 4 {
		t.Fatal("CLI did not use fresh mTLS inventory or idempotent persisted report", calls.Load())
	}
	// A forged wrong platform identity does not get to a peer request, and
	// neither stderr nor stdout is allowed to print the private config.
	config["certificateFile"] = write("wrong.pem", tenantCert)
	config["privateKeyFile"] = write("wrong-key.pem", tenantKey)
	raw, _ = json.Marshal(config)
	bad := write("wrong.json", raw)
	command := exec.CommandContext(t.Context(), binary, "-mode", "recovery-review", "-config", bad, "-confirmed")
	out, e := command.CombinedOutput()
	if e == nil || strings.Contains(string(out), db.DSN) || calls.Load() != 4 {
		t.Fatal("wrong mTLS identity accepted/disclosed")
	}
	// Exercise the compiled activation path, including a fresh authenticated
	// authority failure after prepare. No real account credentials are enabled.
	oldPub, oldKey, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	oldDER, e := x509.CreateCertificate(rand.Reader, ca, ca, oldPub, oldKey)
	if e != nil {
		t.Fatal(e)
	}
	oldPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: oldDER})
	oldDigest := sha256.Sum256(oldPEM)
	platformPair, e := tls.X509KeyPair(cert, key)
	if e != nil {
		t.Fatal(e)
	}
	hash, e := bcrypt.GenerateFromPassword([]byte("new-recovery-cli-fixture-password"), 10)
	if e != nil {
		t.Fatal(e)
	}
	activation := ActivationRequest{ReviewRequest: request, ActivationID: "cli-activation", EvidenceDigest: first.EvidenceDigest, AuthoritySHA256: tenancy.CertificateFingerprint(&tls.Config{Certificates: []tls.Certificate{platformPair}}), OldAuthoritiesSHA256: hex.EncodeToString(oldDigest[:]), PlatformControlURL: "https://new-platform.example.test", MaintenanceEvidenceSHA256: strings.Repeat("c", 64), AdminUsername: "cli-operator", AdminCredentialSHA256: reviewHash(string(hash)), Accounts: []RecoveryAccount{}}
	raw, _ = json.Marshal(activation)
	config = map[string]any{}
	_ = json.Unmarshal(raw, &config)
	config["databaseUrl"] = db.DSN
	config["caFile"] = write("ca.pem", caPEM)
	config["certificateFile"] = write("platform.pem", cert)
	config["privateKeyFile"] = write("platform-key.pem", key)
	config["oldCaFile"] = write("old-ca.pem", oldPEM)
	config["adminPasswordHashFile"] = write("fresh.hash", hash)
	config["accountPasswordHashFiles"] = map[string]string{}
	raw, _ = json.Marshal(config)
	activationFile := write("activation.json", raw)
	activate := func(mode string, success bool) ActivationResult {
		t.Helper()
		command := exec.CommandContext(t.Context(), binary, "-mode", mode, "-config", activationFile, "-confirmed")
		out, e := command.CombinedOutput()
		if (e == nil) != success {
			t.Fatalf("activation CLI %s success=%v", mode, success)
		}
		for _, secret := range []string{db.DSN, string(hash), root} {
			if bytes.Contains(out, []byte(secret)) {
				t.Fatal("activation disclosed secret")
			}
		}
		var result ActivationResult
		if success && json.Unmarshal(out, &result) != nil {
			t.Fatal("invalid activation receipt")
		}
		return result
	}
	if activate("recovery-prepare", true).State != "prepared" {
		t.Fatal("prepare")
	}
	rejectAuthority.Store(true)
	activate("recovery-activate", false)
	if _, e = platform.OpenWithAuthority(t.Context(), db.DSN, activation.AuthoritySHA256); e == nil {
		t.Fatal("failed CLI proof reopened directory")
	}
	rejectAuthority.Store(false)
	if activate("recovery-activate", true).State != "activated" || activate("recovery-status", true).HeldAccounts != 1 {
		t.Fatal("activation status")
	}
	activate("recovery-activate", true)
}
