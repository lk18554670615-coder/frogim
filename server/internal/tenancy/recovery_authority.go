package tenancy

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
)

// RecoveryAuthority is public trust material captured from the actual running
// TLS configuration, not an operator's claim that an old host was stopped.
type RecoveryAuthority struct {
	TenantID                string `json:"tenantId"`
	PlatformControlURL      string `json:"platformControlUrl"`
	AuthoritiesPEM          string `json:"authoritiesPem"`
	ClientCertificateSHA256 string `json:"clientCertificateSha256"`
}

func CertificateFingerprint(c *tls.Config) string {
	if c == nil || len(c.Certificates) != 1 || len(c.Certificates[0].Certificate) == 0 {
		return ""
	}
	h := sha256.Sum256(c.Certificates[0].Certificate[0])
	return hex.EncodeToString(h[:])
}

// CaptureAuthorities rejects files that differ from either live inbound or
// outbound trust. Only public CA certificates are returned.
func CaptureAuthorities(c *tls.Config, path string) (string, error) {
	raw, e := os.ReadFile(path)
	if e != nil || len(raw) > 1<<20 || c == nil {
		return "", ErrInvalid
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) || !pool.Equal(c.ClientCAs) || !pool.Equal(c.RootCAs) {
		return "", ErrInvalid
	}
	remaining := raw
	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if block.Type != "CERTIFICATE" || err != nil || !cert.IsCA {
			return "", ErrInvalid
		}
		remaining = rest
	}
	return string(raw), nil
}

// DisjointAuthorities requires a complete CA replacement. Keeping any old
// trust anchor (even alongside a new one) is not authority fencing.
func DisjointAuthorities(oldPEM, currentPEM string) bool {
	decode := func(raw string) map[string]bool {
		out := map[string]bool{}
		for len(raw) > 0 {
			block, rest := pem.Decode([]byte(raw))
			if block == nil {
				break
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if block.Type != "CERTIFICATE" || err != nil || !cert.IsCA {
				return nil
			}
			h := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
			out[hex.EncodeToString(h[:])] = true
			raw = string(rest)
		}
		return out
	}
	old, current := decode(oldPEM), decode(currentPEM)
	if len(old) == 0 || len(current) == 0 {
		return false
	}
	for key := range old {
		if current[key] {
			return false
		}
	}
	return true
}
