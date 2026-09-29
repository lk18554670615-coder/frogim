package deployment

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
)

func TestAgentDeploymentMTLSControl(t *testing.T) {
	pool, _, cert := testAgentPKI(t)
	serverCert, _, _ := cert(AgentIdentity("server-a"))
	a, e := New("server-a", "a", "https://a.example", "local_preview", []byte(strings.Repeat("x", 43)))
	if e != nil {
		t.Fatal(e)
	}
	a.report.Runtime = "linux/amd64"
	a.report.Capabilities = []string{"inspect", "deploy"}
	c := fixtureCatalog()
	runner := &fixtureRunner{}
	x, e := OpenExecutor(t.TempDir(), "server-a", "a", a.report.HostFingerprint, "linux/amd64", c, runner)
	if e != nil {
		t.Fatal(e)
	}
	a.executor = x
	t.Cleanup(a.Close)
	s := httptest.NewUnstartedServer(a.Handler())
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}, MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	s.StartTLS()
	defer s.Close()
	nonce, _ := tenancy.Secret()
	op := fixtureOperation(c, "job-one", "release-one", "", 0)
	op.HostFingerprint = a.report.HostFingerprint
	for _, identity := range []string{tenancy.EnterpriseIdentity("a"), AgentIdentity("server-b"), tenancy.PlatformIdentity} {
		pair, _, _ := cert(identity)
		cfg := &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13}
		rpc, e := tenancy.NewRPC(s.URL, cfg, AgentIdentity("server-a"))
		if e != nil {
			t.Fatal(e)
		}
		var out ControlReport
		e = rpc.Call(t.Context(), "/internal/agent/deployment/submit", ControlRequest{Nonce: nonce, Operation: op}, &out)
		if identity != tenancy.PlatformIdentity {
			if e == nil {
				t.Fatal("unauthorized dispatch")
			}
			continue
		}
		if e != nil || out.Nonce != nonce || out.Receipt.State != "pending" || out.Receipt.Operation != op {
			t.Fatal("dispatch", e, out)
		}
		runner.failApply = true
		if _, e = x.Once(t.Context()); e == nil {
			t.Fatal("missing injected failure")
		}
		if e = rpc.Call(t.Context(), "/internal/agent/deployment/status", ControlRequest{Nonce: nonce, Operation: op}, &out); e != nil || out.Receipt.State != "unconfirmed" {
			t.Fatal(e)
		}
		if e = rpc.Call(t.Context(), "/internal/agent/deployment/retry", ControlRequest{Nonce: nonce, Operation: op, ExpectedAttempts: 1}, &out); e != nil || out.Receipt.State != "pending" {
			t.Fatal(e)
		}
		runner.failApply = false
		if _, e = x.Once(t.Context()); e != nil {
			t.Fatal(e)
		}
		for range 3 {
			if e = rpc.Call(t.Context(), "/internal/agent/deployment/submit", ControlRequest{Nonce: nonce, Operation: op}, &out); e != nil || out.Receipt.State != "completed" || out.Status.Generation != 1 {
				t.Fatal(e, out)
			}
		}
		// Base64URL challenges are not entity IDs: '-' and '_' are legal
		// first characters of the cryptographically random platform nonce.
		for _, challenge := range []string{"-" + strings.Repeat("a", 42), "_" + strings.Repeat("b", 42)} {
			if e = rpc.Call(t.Context(), "/internal/agent/deployment/submit", ControlRequest{Nonce: challenge, Operation: op}, &out); e != nil || out.Nonce != challenge {
				t.Fatal("valid Base64URL challenge rejected", e)
			}
		}
		for _, challenge := range []string{"!" + strings.Repeat("a", 42), strings.Repeat("a", 42), strings.Repeat("a", 44)} {
			if e = rpc.Call(t.Context(), "/internal/agent/deployment/status", ControlRequest{Nonce: challenge, Operation: op}, &out); e == nil {
				t.Fatal("malformed challenge accepted")
			}
		}
		if runner.applies != 2 {
			t.Fatal("replay executed")
		}
		transport := &http.Transport{TLSClientConfig: cfg}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport}
		b, _ := json.Marshal(ControlRequest{Nonce: nonce, Operation: op})
		for _, body := range []string{string(b[:len(b)-1]) + `,"command":"rm"}`, string(b) + ` {}`, `{"nonce":"` + nonce + `","operation":{"path":"/"}}`, strings.Repeat(" ", 9000)} {
			r, e := client.Post(s.URL+"/internal/agent/deployment/submit", "application/json", strings.NewReader(body))
			if e != nil {
				t.Fatal(e)
			}
			r.Body.Close()
			if r.StatusCode != 400 {
				t.Fatal("unsafe input", r.StatusCode)
			}
		}
	}
}

func TestAgentDeploymentDisabledByDefault(t *testing.T) {
	a, e := New("server-a", "a", "https://a.example", "local_preview", []byte(strings.Repeat("x", 43)))
	if e != nil {
		t.Fatal(e)
	}
	if a.executor != nil || len(a.report.Capabilities) != 1 {
		t.Fatal("default execution enabled")
	}
	for _, path := range []string{"", "relative.json", "../config.json"} {
		if a.ConfigureExecutor(path) == nil {
			t.Fatal("unsafe configuration path")
		}
	}
}
