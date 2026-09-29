package tenancy

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestRPCPushRefusalsAreScopedAndSanitized(t *testing.T) {
	for _, status := range []int{400, 401, 403, 409, 404, 429, 500, 503} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"private-sentinel"}}`))
		}))
		c := &RPC{base: s.URL, client: s.Client()}
		var rejected *PushRejected
		err := c.Call(t.Context(), "/internal/tenancy/push/deliver", PushRequest{}, &PushReceipt{})
		permanent := status == 400 || status == 401 || status == 403 || status == 409
		if errors.As(err, &rejected) != permanent {
			t.Fatal("push refusal classification", status)
		}
		err = c.Call(t.Context(), "/internal/tenancy/tickets/consume", map[string]string{}, nil)
		if errors.As(err, &rejected) {
			t.Fatal("push classification affected another operation")
		}
		s.Close()
	}
}

func TestPeerIdentityNeedsVerifiedSingleURI(t *testing.T) {
	r := httptest.NewRequest("POST", "https://platform.example/internal/tenancy/tickets/consume", nil)
	r.Header.Set("X-Tenant-ID", "a")
	if PeerIdentity(r) != "" {
		t.Fatal("trusted a forwarded identity")
	}
	u, _ := url.Parse(EnterpriseIdentity("a"))
	cert := &x509.Certificate{URIs: []*url.URL{u}}
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	if PeerIdentity(r) != "" {
		t.Fatal("trusted unverified certificate")
	}
	r.TLS.VerifiedChains = [][]*x509.Certificate{{cert}}
	if PeerIdentity(r) != EnterpriseIdentity("a") {
		t.Fatal("lost verified identity")
	}
	cert.URIs = append(cert.URIs, u)
	if PeerIdentity(r) != "" {
		t.Fatal("ambiguous service identity")
	}
}

func TestRPCRefusesInsecureOrAmbiguousPeerConfig(t *testing.T) {
	for _, raw := range []string{"http://platform.example", "https://user:pass@platform.example", "https://platform.example?token=secret"} {
		if _, err := NewRPC(raw, &tls.Config{}, PlatformIdentity); err == nil {
			t.Fatal(raw)
		}
	}
	if _, err := NewRPC("https://platform.example", &tls.Config{InsecureSkipVerify: true}, PlatformIdentity); err == nil {
		t.Fatal("disabled TLS verification")
	}
}
