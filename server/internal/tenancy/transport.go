package tenancy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// These service identities belong to a dedicated private CA, not a public Web
// certificate or an HTTP header supplied by the reverse proxy.
const PlatformIdentity = "spiffe://frogim/platform"

func EnterpriseIdentity(id string) string { return "spiffe://frogim/tenant/" + id }

func PeerIdentity(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	cert := r.TLS.PeerCertificates[0]
	if len(cert.URIs) != 1 {
		return ""
	}
	return cert.URIs[0].String()
}

func TLSConfig(caFile, certFile, keyFile string) (*tls.Config, error) {
	ca, err := os.ReadFile(caFile)
	if err != nil {
		return nil, errors.New("cannot read tenancy CA")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("invalid tenancy CA")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, errors.New("invalid tenancy service certificate")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ClientCAs: roots, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert}, nil
}

// Check the local role before starting workers or migrating a database. Peer
// verification alone discovers a swapped certificate too late at first RPC.
func ValidateLocalIdentity(c *tls.Config, identity string) error {
	if c == nil || c.InsecureSkipVerify || c.MinVersion < tls.VersionTLS13 || c.ClientAuth != tls.RequireAndVerifyClientCert || len(c.Certificates) != 1 || len(c.Certificates[0].Certificate) == 0 || c.RootCAs == nil {
		return ErrInvalid
	}
	leaf, err := x509.ParseCertificate(c.Certificates[0].Certificate[0])
	if err != nil || leaf.IsCA || len(leaf.URIs) != 1 || leaf.URIs[0].String() != identity {
		return ErrInvalid
	}
	intermediates := x509.NewCertPool()
	for _, der := range c.Certificates[0].Certificate[1:] {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return ErrInvalid
		}
		intermediates.AddCert(cert)
	}
	for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: c.RootCAs, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
			return ErrInvalid
		}
	}
	return nil
}

type RPC struct {
	client *http.Client
	base   string
}

// expectedIdentity also prevents a different service certified by the same CA
// from becoming this peer. DNS/hostname verification remains enabled.
func NewRPC(base string, config *tls.Config, expectedIdentity string) (*RPC, error) {
	if ValidateBaseURL(base, false) != nil || config == nil || expectedIdentity == "" || config.InsecureSkipVerify {
		return nil, ErrInvalid
	}
	cfg := config.Clone()
	cfg.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
			return errors.New("unverified peer")
		}
		uris := state.PeerCertificates[0].URIs
		if len(uris) != 1 || uris[0].String() != expectedIdentity {
			return errors.New("unexpected service identity")
		}
		return nil
	}
	return &RPC{base: strings.TrimRight(base, "/"), client: &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *RPC) Call(ctx context.Context, path string, request, response any) error {
	if !strings.HasPrefix(path, "/internal/") || strings.ContainsAny(path, "?#\\") {
		return ErrInvalid
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return errors.New("tenant control peer unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		if path == "/internal/tenancy/push/deliver" {
			switch res.StatusCode {
			case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict:
				return &PushRejected{}
			}
		}
		if res.StatusCode == http.StatusConflict {
			var rejected struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&rejected) == nil && RepairableCode(rejected.Error.Code) {
				return &OperationRejected{Code: rejected.Error.Code}
			}
		}
		return errors.New("tenant control operation not acknowledged")
	}
	if response == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		return err
	}
	d := json.NewDecoder(io.LimitReader(res.Body, 1<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(response); err != nil {
		return errors.New("invalid control peer response")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("invalid trailing control peer response")
	}
	return nil
}
