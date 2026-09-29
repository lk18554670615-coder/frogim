package deployment

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

func testAgentPKI(t *testing.T) (*x509.CertPool, []byte, func(string) (tls.Certificate, []byte, []byte)) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated-agent-tests"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	root, e = x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	pool = x509.NewCertPool()
	pool.AddCert(root)
	makeCert := func(identity string) (tls.Certificate, []byte, []byte) {
		k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(identity)
		leaf := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "agent-test-leaf"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{u}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		cert, e := x509.CreateCertificate(rand.Reader, leaf, root, &k.PublicKey, key)
		if e != nil {
			t.Fatal(e)
		}
		kb, e := x509.MarshalPKCS8PrivateKey(k)
		if e != nil {
			t.Fatal(e)
		}
		cp, kp := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb})
		pair, e := tls.X509KeyPair(cp, kp)
		if e != nil {
			t.Fatal(e)
		}
		return pair, cp, kp
	}
	return pool, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), makeCert
}
func TestAgentMTLSInspectionBoundary(t *testing.T) {
	pool, _, cert := testAgentPKI(t)
	serverCert, _, _ := cert(AgentIdentity("server-a"))
	a, e := New("server-a", "a", "https://a.example", "local_preview", []byte(strings.Repeat("x", 43)))
	if e != nil {
		t.Fatal(e)
	}
	a.report.Runtime = "linux/amd64"
	s := httptest.NewUnstartedServer(a.Handler())
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}, MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	s.StartTLS()
	defer s.Close()
	nonce, _ := tenancy.Secret()
	for _, identity := range []string{tenancy.PlatformIdentity, tenancy.EnterpriseIdentity("a"), AgentIdentity("server-b")} {
		pair, _, _ := cert(identity)
		cfg := &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13}
		rpc, e := tenancy.NewRPC(s.URL, cfg, AgentIdentity("server-a"))
		if e != nil {
			t.Fatal(e)
		}
		var report Inspection
		e = rpc.Call(t.Context(), "/internal/agent/inspect", map[string]string{"nonce": nonce}, &report)
		if identity == tenancy.PlatformIdentity {
			if e != nil || !report.Valid(nonce, "server-a", "a", "https://a.example") {
				t.Fatal("platform inspection", e)
			}
		} else if e == nil {
			t.Fatal("wrong principal accepted", identity)
		}
		wrong, _ := tenancy.NewRPC(s.URL, cfg, AgentIdentity("another-server"))
		if wrong.Call(t.Context(), "/internal/agent/inspect", map[string]string{"nonce": nonce}, &report) == nil {
			t.Fatal("wrong server accepted")
		}
	}
	pair, _, _ := cert(tenancy.PlatformIdentity)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	for _, body := range []string{`{}`, `{"nonce":"` + nonce + `","command":"rm"}`, `{"nonce":"` + nonce + `"} {}`, `{"nonce":"` + nonce + `"}` + strings.Repeat(" ", 4200)} {
		r, e := client.Post(s.URL+"/internal/agent/inspect", "application/json", strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Fatal("bad request", r.StatusCode)
		}
	}
	r, e := client.Post(s.URL+"/internal/agent/execute", "application/json", strings.NewReader(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatal("unexpected execution route")
	}
	raw := httptest.NewRequest("POST", "https://agent.example/internal/agent/inspect", strings.NewReader(`{"nonce":"`+nonce+`"}`))
	raw.Header.Set("X-Platform-Identity", tenancy.PlatformIdentity)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, raw)
	if w.Code != 403 {
		t.Fatal("header bypass")
	}
}
func TestAgentImmutableReportAndPeerConfiguration(t *testing.T) {
	_, ca, cert := testAgentPKI(t)
	pair, cp, kp := cert(tenancy.PlatformIdentity)
	dir := t.TempDir()
	files := map[string][]byte{"ca.pem": ca, "cert.pem": cp, "key.pem": kp}
	for name, data := range files {
		if e := os.WriteFile(filepath.Join(dir, name), data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	cfg := PeerConfig{ServerID: "server-a", ControlURL: "https://agent.example", CAFile: filepath.Join(dir, "ca.pem"), CertFile: filepath.Join(dir, "cert.pem"), KeyFile: filepath.Join(dir, "key.pem")}
	b, _ := json.Marshal([]PeerConfig{cfg})
	peers, e := ReadPeers(strings.NewReader(string(b)))
	if e != nil || len(peers) != 1 {
		t.Fatal(e)
	}
	for _, raw := range []string{`[] {}`, `[{"command":"x"}]`, string(b[:len(b)-1]) + "," + string(b[1:]), `[null]`} {
		if _, e := ReadPeers(strings.NewReader(raw)); e == nil {
			t.Fatal("invalid config accepted")
		}
	}
	if !LocalIdentity(&tls.Config{Certificates: []tls.Certificate{pair}}, tenancy.PlatformIdentity) || LocalIdentity(&tls.Config{Certificates: []tls.Certificate{pair}}, AgentIdentity("server-a")) {
		t.Fatal("local identity")
	}
	for _, id := range []string{"", strings.Repeat("x", 31), "../../host", strings.Repeat("x", 129)} {
		if _, e := New("server-a", "a", "https://a.example", "local_preview", []byte(id)); e == nil {
			t.Fatal("invalid host identifier")
		}
	}
	if _, e := New("server-a", "a", "https://a.example", "dedicated_host", []byte(strings.Repeat("z", 32))); e == nil {
		t.Fatal("dedicated identifier")
	}
	nonce, _ := tenancy.Secret()
	r := Inspection{Nonce: nonce, Protocol: 1, ServerID: "server-a", TenantID: "a", HTTPBaseURL: "https://a.example", HostFingerprint: strings.Repeat("a", 64), IsolationMode: "local_preview", Runtime: "linux/amd64", Capabilities: []string{"inspect"}}
	for _, change := range []func(*Inspection){func(x *Inspection) { x.Nonce = "old" }, func(x *Inspection) { x.TenantID = "b" }, func(x *Inspection) { x.HTTPBaseURL = "https://b.example" }, func(x *Inspection) { x.Protocol = 2 }, func(x *Inspection) { x.Capabilities = []string{"shell"} }, func(x *Inspection) { x.IsolationMode = "physical-verified" }, func(x *Inspection) { x.HostFingerprint = "raw-host-id" }} {
		bad := r
		change(&bad)
		if bad.Valid(nonce, "server-a", "a", "https://a.example") {
			t.Fatal("report accepted mismatch")
		}
	}
}
