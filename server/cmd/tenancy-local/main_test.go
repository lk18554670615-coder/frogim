package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
)

func TestInitializeIsIdempotentAndSeparatesPrivateKeys(t *testing.T) {
	root := t.TempDir()
	if err := initialize(root); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = initialize(root); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "credentials.json"))
	if string(before) != string(after) {
		t.Fatal("credentials rotated on restart")
	}
	var creds localCredentials
	if json.Unmarshal(before, &creds) != nil || len(creds.UserPassword) != 43 || creds.PlatformAdmin == creds.EnterpriseAdmin {
		t.Fatal("credentials not independent")
	}
	for dir, identity := range map[string]string{"platform": tenancy.PlatformIdentity, "enterprise": tenancy.EnterpriseIdentity("default")} {
		cfg, err := tenancy.TLSConfig(filepath.Join(root, dir, "pki/ca.pem"), filepath.Join(root, dir, "pki/control.pem"), filepath.Join(root, dir, "pki/control.key"))
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
		if err != nil || len(cert.URIs) != 1 || cert.URIs[0].String() != identity {
			t.Fatal("incorrect control identity", err)
		}
	}
	publicCA, _ := os.ReadFile(filepath.Join(root, "browser-ca.pem"))
	controlCA, _ := os.ReadFile(filepath.Join(root, "platform/pki/ca.pem"))
	if string(publicCA) == string(controlCA) {
		t.Fatal("public and control CA shared")
	}
	cert, err := tls.LoadX509KeyPair(filepath.Join(root, "enterprise/gateway/public.pem"), filepath.Join(root, "enterprise/gateway/public.key"))
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(publicCA)
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if len(leaf.URIs) > 0 {
		t.Fatal("gateway can impersonate control identity")
	}
	if leaf.PublicKeyAlgorithm != x509.ECDSA || ca.PublicKeyAlgorithm != x509.ECDSA {
		t.Fatal("local public TLS must use browser-compatible ECDSA P256")
	}
}

func TestRenewPublicTLSPreservesAccountsAndControlKeys(t *testing.T) {
	root := t.TempDir()
	if err := initialize(root); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, "credentials.json"))
	control, _ := os.ReadFile(filepath.Join(root, "platform/pki/control.key"))
	oldCA, _ := os.ReadFile(filepath.Join(root, "browser-ca.pem"))
	if err := renewPublicTLS(root); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "credentials.json"))
	newControl, _ := os.ReadFile(filepath.Join(root, "platform/pki/control.key"))
	newCA, _ := os.ReadFile(filepath.Join(root, "browser-ca.pem"))
	if string(before) != string(after) || string(control) != string(newControl) || string(oldCA) == string(newCA) {
		t.Fatal("incorrect rotation scope")
	}
	backups, _ := filepath.Glob(filepath.Join(root, "public-tls-renewal-*", "previous", "browser-ca.pem"))
	if len(backups) != 1 {
		t.Fatal("missing backup")
	}
	backup, _ := os.ReadFile(backups[0])
	if string(backup) != string(oldCA) {
		t.Fatal("backup mismatch")
	}
	if renewPublicTLS(t.TempDir()) == nil {
		t.Fatal("uninitialized directory accepted")
	}
}

func TestInitializeRefusesPartialDirectory(t *testing.T) {
	root := t.TempDir()
	if err := save(root, "sentinel", []byte("preserve")); err != nil {
		t.Fatal(err)
	}
	if initialize(root) == nil {
		t.Fatal("partial directory overwritten")
	}
	b, _ := os.ReadFile(filepath.Join(root, "sentinel"))
	if string(b) != "preserve" {
		t.Fatal("sentinel changed")
	}
}
