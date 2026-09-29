package tenancy

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoveryAuthorityRequiresNewKeysAndActualTrust(t *testing.T) {
	makeCA := func(key ed25519.PrivateKey, serial int64) string {
		t.Helper()
		c := &x509.Certificate{SerialNumber: big.NewInt(serial), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
		der, e := x509.CreateCertificate(rand.Reader, c, c, key.Public(), key)
		if e != nil {
			t.Fatal(e)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	_, oldKey, _ := ed25519.GenerateKey(rand.Reader)
	_, newKey, _ := ed25519.GenerateKey(rand.Reader)
	oldCA, newCA := makeCA(oldKey, 1), makeCA(newKey, 2)
	if !DisjointAuthorities(oldCA, newCA) {
		t.Fatal("replacement rejected")
	}
	for _, value := range []string{oldCA, oldCA + newCA, makeCA(oldKey, 3), "invalid"} {
		if DisjointAuthorities(oldCA, value) {
			t.Fatal("old or invalid trust accepted")
		}
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(newCA))
	path := filepath.Join(t.TempDir(), "roots.pem")
	if os.WriteFile(path, []byte(newCA), 0600) != nil {
		t.Fatal("fixture")
	}
	cfg := &tls.Config{RootCAs: roots, ClientCAs: roots}
	if got, e := CaptureAuthorities(cfg, path); e != nil || got != newCA {
		t.Fatal("live trust", e)
	}
	extra := roots.Clone()
	extra.AppendCertsFromPEM([]byte(oldCA))
	cfg.ClientCAs = extra
	if _, e := CaptureAuthorities(cfg, path); e == nil {
		t.Fatal("file differs from loaded inbound trust")
	}
}
