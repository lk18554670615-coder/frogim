package deployment

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

func bundleTestPKI(t *testing.T, extraNames ...string) func(string) EnterpriseTLS {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated bundle test CA"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour)}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, key.Public(), key)
	if e != nil {
		t.Fatal(e)
	}
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return func(identity string) EnterpriseTLS {
		t.Helper()
		leafKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
		if e != nil {
			t.Fatal(e)
		}
		leaf := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "isolated bundle service"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"host.docker.internal", "enterprise-api"}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		if identity != "" {
			u, _ := url.Parse(identity)
			leaf.URIs = []*url.URL{u}
		}
		leaf.DNSNames = append(leaf.DNSNames, extraNames...)
		der, e := x509.CreateCertificate(rand.Reader, leaf, ca, leafKey.Public(), key)
		if e != nil {
			t.Fatal(e)
		}
		private, e := x509.MarshalPKCS8PrivateKey(leafKey)
		if e != nil {
			t.Fatal(e)
		}
		return EnterpriseTLS{CA: caPEM, Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))}
	}
}
func bundleTestConfig(t *testing.T) EnterpriseConfig {
	t.Helper()
	s, e := NewEnterpriseSecrets("synthetic-bundle-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	issue := bundleTestPKI(t)
	return EnterpriseConfig{TenantID: "fixture-a", ServerID: "server-a", ToolsImage: "frogim/enterprise-bundle@sha256:" + strings.Repeat("a", 64), PlatformControlURL: "https://host.docker.internal:19999", PlatformWebOrigin: "https://127.0.0.1:18443", Ports: EnterprisePorts{HTTP: 21444, Media: 21445, IM: 21180, Control: 21446, RTCTCP: 21881, RTCUDPFrom: 21882}, Secrets: s, ControlTLS: issue(tenancy.EnterpriseIdentity("fixture-a")), PublicTLS: issue("")}
}
func TestEnterpriseBundleGenerationBoundaries(t *testing.T) {
	c := bundleTestConfig(t)
	release := Release{ID: "bundle-one", Sequence: 1, Runtime: "linux/amd64", SchemaVersion: 79}
	raw, r, e := BuildEnterpriseBundle(c, release)
	if e != nil {
		t.Fatal(e)
	}
	if !r.MatchesTarget(c.TenantID, c.ServerID) || r.MatchesTarget("other", c.ServerID) {
		t.Fatal("enterprise release not bound")
	}
	again, repeated, e := BuildEnterpriseBundle(c, release)
	if e != nil || !bytes.Equal(raw, again) || r.Digest() != repeated.Digest() {
		t.Fatal("non-deterministic bundle")
	}
	var b Bundle
	if json.Unmarshal(raw, &b) != nil || len(b.Services) != 9 || validateBundle(b, r) != nil {
		t.Fatal("missing enterprise dependencies")
	}
	for name, s := range b.Services {
		if strings.Contains(s.Image, ":latest") || !imageReference.MatchString(s.Image) || s.Healthcheck == nil {
			t.Fatal("unpinned/unhealthy", name)
		}
		for _, v := range s.Volumes {
			if v.Type != "volume" {
				t.Fatal("host resource exposed")
			}
		}
		for _, p := range s.Ports {
			if p.HostIP != "127.0.0.1" {
				t.Fatal("public preview")
			}
		}
		if name == "enterprise-db" || name == "enterprise-redis" || name == "enterprise-minio" {
			if len(s.Ports) != 0 || len(s.Networks) != 1 || !b.Networks[s.Networks[0]].Internal {
				t.Fatal("data endpoint exposed")
			}
		}
		encoded, _ := json.Marshal(s)
		if name != "enterprise-api" && bytes.Contains(encoded, []byte(c.Secrets.JWT)) {
			t.Fatal("auth key leaked to other role")
		}
	}
	root := t.TempDir()
	if e = os.Mkdir(filepath.Join(root, r.ID), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, r.ID, "compose.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = readBundle(root, r.ID, r.ComposeSHA256); e != nil {
		t.Fatal(e)
	}
	catalog, _ := json.Marshal([]Release{r})
	if _, e = ReadCatalog(bytes.NewReader(catalog)); e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(catalog, []byte(c.Secrets.JWT)) || bytes.Contains(catalog, []byte("PRIVATE KEY")) {
		t.Fatal("catalog secret")
	}
	other := r
	other.ID, other.TenantID, other.ServerID = "other-one", "other", "server-other"
	combined, _ := json.Marshal([]Release{r, other})
	if _, e := ReadCatalog(bytes.NewReader(combined)); e != nil {
		t.Fatal("independent enterprise sequence rejected", e)
	}
	other.Sequence = 2
	other.RollbackTo = []string{r.ID}
	combined, _ = json.Marshal([]Release{r, other})
	if _, e := ReadCatalog(bytes.NewReader(combined)); e == nil {
		t.Fatal("cross-enterprise rollback accepted")
	}
	if x, e := OpenExecutor(t.TempDir(), "server-other", "other", strings.Repeat("a", 64), r.Runtime, map[string]Release{r.ID: r}, &fixtureRunner{}); e == nil {
		x.Close()
		t.Fatal("foreign enterprise catalog loaded")
	}
	for _, mutation := range []func(*EnterpriseConfig){
		func(c *EnterpriseConfig) { c.ToolsImage = "frogim/test:latest" },
		func(c *EnterpriseConfig) { c.TenantID = "../default" },
		func(c *EnterpriseConfig) { c.Secrets.MediaSigning = c.Secrets.JWT },
		func(c *EnterpriseConfig) { c.Secrets.Database = "fixed-password" },
		func(c *EnterpriseConfig) { c.PlatformControlURL = "https://public.example" },
		func(c *EnterpriseConfig) { c.PlatformWebOrigin = "*" },
		func(c *EnterpriseConfig) { c.Ports.Control = c.Ports.Media },
		func(c *EnterpriseConfig) { c.Ports.RTCUDPFrom = 65534 },
		func(c *EnterpriseConfig) { c.ControlTLS = c.PublicTLS },
		func(c *EnterpriseConfig) { c.ControlTLS.PrivateKey = c.PublicTLS.PrivateKey },
		func(c *EnterpriseConfig) { c.Secrets.AdminPasswordHash = "password" },
	} {
		changed := c
		mutation(&changed)
		if _, _, e := BuildEnterpriseBundle(changed, release); e == nil {
			t.Fatal("unsafe config accepted")
		}
	}
	encoded, _ := json.Marshal(c)
	if _, e := ReadEnterpriseConfig(bytes.NewReader(encoded)); e != nil {
		t.Fatal(e)
	}
	encoded = append(encoded[:len(encoded)-1], []byte(`,"arbitraryShell":"true"}`)...)
	if _, e := ReadEnterpriseConfig(bytes.NewReader(encoded)); e == nil {
		t.Fatal("extra config accepted")
	}
}
func TestRuntimeTmpfsFilesAndFixedEntrypoints(t *testing.T) {
	root := t.TempDir()
	b, _ := json.Marshal(map[string]string{"ca.pem": "fixture-ca", "control.pem": "fixture-cert", "control.key": "fixture-key"})
	if e := PrepareRuntime("api", root, string(b)); e != nil {
		t.Fatal(e)
	}
	if e := PrepareRuntime("api", root, string(b)); e == nil {
		t.Fatal("overwrote live secret")
	}
	for _, body := range []string{`{"../key":"secret"}`, `{"wk.yaml":"test","../../host":"secret"}`, `{"wk.yaml":"test"} {}`} {
		if PrepareRuntime("im", t.TempDir(), body) == nil {
			t.Fatal("unsafe file shape")
		}
	}
	if PrepareRuntime("shell", root, string(b)) == nil {
		t.Fatal("unknown role")
	}
	if _, _, e := RuntimeCommand("sh"); e == nil {
		t.Fatal("arbitrary command")
	}
	for _, role := range []string{"api", "im", "gateway", "livekit"} {
		if _, _, e := RuntimeCommand(role); e != nil {
			t.Fatal(e)
		}
	}
}

func TestEnterpriseReleaseArtifactImmutable(t *testing.T) {
	c := bundleTestConfig(t)
	r := Release{ID: "artifact-one", Sequence: 1, Runtime: "linux/amd64", SchemaVersion: 79}
	root := t.TempDir()
	first, e := WriteEnterpriseRelease(root, c, r)
	if e != nil {
		t.Fatal(e)
	}
	second, e := WriteEnterpriseRelease(root, c, r)
	if e != nil || first.Digest() != second.Digest() {
		t.Fatal("same artifact replay failed")
	}
	c.Secrets.JWT, _ = tenancy.Secret()
	if _, e = WriteEnterpriseRelease(root, c, r); e == nil {
		t.Fatal("replaced immutable secrets")
	}
	if _, _, e = readBundle(root, r.ID, first.ComposeSHA256); e != nil {
		t.Fatal("original artifact damaged")
	}
	partial := Release{ID: "partial", Sequence: 2, Runtime: "linux/amd64", SchemaVersion: 79}
	if e = os.Mkdir(filepath.Join(root, partial.ID), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = WriteEnterpriseRelease(root, c, partial); e == nil {
		t.Fatal("adopted incomplete artifact")
	}
	if _, e = WriteEnterpriseRelease("relative", c, r); e == nil {
		t.Fatal("relative output root")
	}
	missing := Release{ID: "bad-rollback", Sequence: 3, Runtime: "linux/amd64", SchemaVersion: 79, RollbackTo: []string{"not-present"}}
	if _, e = WriteEnterpriseRelease(root, c, missing); e == nil {
		t.Fatal("unknown rollback target")
	}
	valid := Release{ID: "artifact-two", Sequence: 2, Runtime: "linux/amd64", SchemaVersion: 79, RollbackTo: []string{r.ID}}
	if _, e = WriteEnterpriseRelease(root, c, valid); e != nil {
		t.Fatal("valid rollback reference", e)
	}
}

func bundleClientTLS(t *testing.T, material EnterpriseTLS) *tls.Config {
	t.Helper()
	cert, e := tls.X509KeyPair([]byte(material.Certificate), []byte(material.PrivateKey))
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(material.CA)) {
		t.Fatal("invalid test CA")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ClientCAs: roots, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert}
}
