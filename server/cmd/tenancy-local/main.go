// tenancy-local creates an isolated, loopback-only development installation.
// It never reads existing deployment credentials or modifies a production DB.
package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	root := flag.String("root", "../.data/tenancy-local", "isolated local configuration directory")
	bootstrap := flag.Bool("bootstrap", false, "bootstrap default enterprise from inside the local platform container")
	verify := flag.Bool("verify", false, "verify the local gateway authentication flow with an explicit local CA")
	verifyAdminCreate := flag.Bool("verify-admin-create", false, "create/resume one named local fixture through enterprise admin and verify platform activation")
	verifyPassword := flag.Bool("verify-password-reset", false, "reset the dedicated local fixture to its existing random password and verify revocation; idempotent task")
	verifyAgent := flag.Bool("verify-agent", false, "register/recheck only the named default-local inspection agent; never deploy or invoke commands")
	renewPublic := flag.Bool("renew-public-tls", false, "renew only local gateway certificates with a recoverable backup; never install trust")
	flag.Parse()
	var err error
	if *renewPublic {
		err = renewPublicTLS(*root)
	} else if *verify || *verifyAdminCreate || *verifyPassword || *verifyAgent {
		err = verifyLocal(*root, *verifyAdminCreate || *verifyPassword, *verifyPassword, *verifyAgent)
	} else if *bootstrap {
		err = bootstrapLocal()
	} else {
		err = initialize(*root)
		if err == nil {
			err = initializeAgent(*root)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Local tenancy operation failed:", err)
		os.Exit(1)
	}
}

func secret() string {
	s, e := tenancy.Secret()
	if e != nil {
		panic(e)
	}
	return s
}
func save(root, name string, data []byte) error {
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("cannot create %s (existing files are never overwritten)", name)
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func jsonFile(root, name string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return save(root, name, append(b, '\n'))
}
func envFile(root, name string, v map[string]string) error {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, k := range keys {
		if strings.ContainsAny(v[k], "\r\n") {
			return errors.New("invalid local setting")
		}
		fmt.Fprintf(&out, "%s=%s\n", k, v[k])
	}
	return save(root, name, []byte(out.String()))
}

type authority struct {
	cert *x509.Certificate
	key  crypto.Signer
	der  []byte
}

func ca(name string) (authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return authority{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return authority{}, err
	}
	t := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, t, t, key.Public(), key)
	return authority{t, key, der}, err
}
func leaf(root, dir string, a authority, identity string, dns []string, public bool) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	t := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: dir}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(30 * 24 * time.Hour), DNSNames: dns, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	name := "control"
	if public {
		name = "public"
		t.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	} else {
		u, _ := url.Parse(identity)
		t.URIs = []*url.URL{u}
		t.ExtKeyUsage = append(t.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	}
	der, err := x509.CreateCertificate(rand.Reader, t, a.cert, key.Public(), a.key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	for file, bytes := range map[string][]byte{"ca.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.der}), name + ".pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), name + ".key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})} {
		if err := save(root, dir+"/"+file, bytes); err != nil {
			return err
		}
	}
	return nil
}

type localCredentials struct {
	PlatformAdmin, EnterpriseAdmin, UserPassword string
	UserPhone                                    string
}

func initialize(root string) error {
	if _, err := os.Stat(filepath.Join(root, "initialized.json")); err == nil {
		fmt.Println("Existing local configuration preserved.")
		return nil
	}
	if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 {
		return errors.New("incomplete local directory; inspect it before recovery, no secrets have been replaced")
	}
	control, err := ca("FrogIM local control CA")
	if err != nil {
		return err
	}
	public, err := ca("FrogIM local browser CA")
	if err != nil {
		return err
	}
	if err = leaf(root, "platform/pki", control, tenancy.PlatformIdentity, []string{"platform-api"}, false); err != nil {
		return err
	}
	if err = leaf(root, "enterprise/pki", control, tenancy.EnterpriseIdentity("default"), []string{"enterprise-api"}, false); err != nil {
		return err
	}
	for _, dir := range []string{"platform/gateway", "enterprise/gateway"} {
		if err = leaf(root, dir, public, "", []string{"localhost"}, true); err != nil {
			return err
		}
	}
	if err = save(root, "browser-ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: public.der})); err != nil {
		return err
	}
	creds := localCredentials{PlatformAdmin: secret(), EnterpriseAdmin: secret(), UserPassword: secret(), UserPhone: "19900000001"}
	if err = jsonFile(root, "credentials.json", creds); err != nil {
		return err
	}
	platformHash, err := bcrypt.GenerateFromPassword([]byte(creds.PlatformAdmin), 12)
	if err != nil {
		return err
	}
	enterpriseHash, err := bcrypt.GenerateFromPassword([]byte(creds.EnterpriseAdmin), 12)
	if err != nil {
		return err
	}
	pDB, eDB, pRedis, eRedis, minioPass, imToken, policyKey, lkSecret := secret(), secret(), secret(), secret(), secret(), secret(), secret(), secret()
	pluginPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	settings := map[string]map[string]string{
		"platform/db.env":    {"POSTGRES_USER": "platform", "POSTGRES_DB": "platform", "POSTGRES_PASSWORD": pDB},
		"enterprise/db.env":  {"POSTGRES_USER": "enterprise", "POSTGRES_DB": "enterprise", "POSTGRES_PASSWORD": eDB},
		"platform/redis.env": {"REDIS_PASSWORD": pRedis}, "enterprise/redis.env": {"REDIS_PASSWORD": eRedis},
		"enterprise/minio.env":   {"MINIO_ROOT_USER": "localmedia", "MINIO_ROOT_PASSWORD": minioPass, "IM_S3_BUCKET": "default-media"},
		"platform/api.env":       {"PLATFORM_ENV": "development", "PLATFORM_DATABASE_URL": "postgres://platform:" + pDB + "@platform-db:5432/platform?sslmode=disable", "PLATFORM_REDIS_URL": "redis://:" + pRedis + "@platform-redis:6379/0", "PLATFORM_ADDR": ":8090", "PLATFORM_CONTROL_ADDR": ":8443", "PLATFORM_CA_FILE": "/config/pki/ca.pem", "PLATFORM_CERT_FILE": "/config/pki/control.pem", "PLATFORM_KEY_FILE": "/config/pki/control.key", "PLATFORM_PEERS_FILE": "/config/peers.json", "PLATFORM_WEB_ORIGIN": "http://127.0.0.1:18900", "PLATFORM_ADMIN_USERNAME": "operator", "PLATFORM_ADMIN_PASSWORD_HASH": string(platformHash)},
		"enterprise/api.env":     {"IM_ENV": "development", "IM_TENANCY_PREVIEW": "true", "IM_TENANT_ID": "default", "IM_TENANT_PUBLIC_URL": "https://127.0.0.1:18444", "IM_PLATFORM_CONTROL_URL": "https://platform-api:8443", "IM_TENANT_CONTROL_ADDR": ":8444", "IM_TENANT_CA_FILE": "/config/pki/ca.pem", "IM_TENANT_CERT_FILE": "/config/pki/control.pem", "IM_TENANT_KEY_FILE": "/config/pki/control.key", "IM_DATABASE_URL": "postgres://enterprise:" + eDB + "@enterprise-db:5432/enterprise?sslmode=disable", "IM_REDIS_URL": "redis://:" + eRedis + "@enterprise-redis:6379/0", "IM_JWT_SECRET": secret(), "IM_MEDIA_SIGNING_SECRET": secret(), "IM_ADMIN_USERNAME": "enterprise-admin", "IM_ADMIN_PASSWORD_HASH": string(enterpriseHash), "IM_ADMIN_ID": "local-enterprise-admin", "IM_PUSH_PROVIDER": "noop", "IM_ALLOWED_ORIGINS": "https://127.0.0.1:18443,https://127.0.0.1:18444", "IM_S3_ENDPOINT": "enterprise-minio:9000", "IM_S3_PUBLIC_ENDPOINT": "127.0.0.1:18445", "IM_S3_PUBLIC_SECURE": "true", "IM_S3_ACCESS_KEY": "localmedia", "IM_S3_SECRET_KEY": minioPass, "IM_S3_BUCKET": "default-media", "IM_WUKONG_ENABLED": "true", "IM_WUKONG_API_URL": "http://enterprise-im:5001", "IM_WUKONG_MANAGER_URL": "http://enterprise-im:5300", "IM_WUKONG_MANAGER_TOKEN": imToken, "IM_WUKONG_TOKEN_SECRET": secret(), "IM_WUKONG_POLICY_SECRET": policyKey, "IM_WUKONG_TCP_URL": "tcp://127.0.0.1:15180", "IM_WUKONG_WS_URL": "wss://127.0.0.1:18444/im", "IM_WUKONG_PLUGIN_DIR": "/plugins", "IM_WUKONG_PLUGIN_TRUSTED_KEYS": "local-build:" + base64.StdEncoding.EncodeToString(pluginPub), "IM_WUKONG_PLUGIN_ALLOWLIST": "im-policy", "IM_LIVEKIT_ENABLED": "true", "IM_LIVEKIT_URL": "wss://127.0.0.1:18444/livekit", "IM_LIVEKIT_API_URL": "http://enterprise-livekit:7880", "IM_LIVEKIT_API_KEY": "local-default", "IM_LIVEKIT_API_SECRET": lkSecret},
		"enterprise/im.env":      {"WK_MANAGERTOKEN": imToken, "WK_TOKENAUTHON": "true", "WK_EXTERNAL_IP": "127.0.0.1", "WK_EXTERNAL_TCPADDR": "tcp://127.0.0.1:15180", "WK_EXTERNAL_WSADDR": "wss://127.0.0.1:18444/im", "WK_WEBHOOK_GRPCADDR": "enterprise-api:6970", "WK_DATASOURCE_ADDR": "http://enterprise-api:8080/internal/wukong/datasource", "WK_TRACE_PROMETHEUSAPIURL": "", "IM_WUKONG_POLICY_URL": "http://enterprise-api:8080/internal/wukong/policy/send", "IM_WUKONG_POLICY_SECRET": policyKey},
		"enterprise/livekit.env": {"LIVEKIT_KEYS": "local-default: " + lkSecret},
	}
	for name, values := range settings {
		if err = envFile(root, name, values); err != nil {
			return err
		}
	}
	if err = jsonFile(root, "platform/peers.json", []map[string]string{{"tenantId": "default", "controlUrl": "https://enterprise-api:8444"}}); err != nil {
		return err
	}
	if err = jsonFile(root, "initialized.json", map[string]any{"version": 1, "tenantId": "default", "createdAt": time.Now().UTC(), "certificateExpiresAt": time.Now().Add(30 * 24 * time.Hour).UTC()}); err != nil {
		return err
	}
	fmt.Println("Local random credentials and separate TLS identities created; see the private credentials.json file. No system certificate was installed.")
	return nil
}

func bootstrapLocal() error {
	if os.Getenv("PLATFORM_ENV") != "development" {
		return errors.New("local bootstrap requires development")
	}
	// This utility is only shipped in the local image, not the production API.
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	db, err := platform.Open(ctx, os.Getenv("PLATFORM_DATABASE_URL"))
	if err != nil {
		return errors.New("local platform database unavailable")
	}
	defer db.Close()
	tls, err := tenancy.TLSConfig("/config/pki/ca.pem", "/config/pki/control.pem", "/config/pki/control.key")
	if err != nil {
		return err
	}
	rpc, err := tenancy.NewRPC("https://enterprise-api:8444", tls, tenancy.EnterpriseIdentity("default"))
	if err != nil {
		return err
	}
	peer := platform.EnterpriseRPC{Peers: map[string]*tenancy.RPC{"default": rpc}}
	// Bootstrap uses audited domain operations, never direct SQL activation.
	if err = db.BootstrapLocalDefault(ctx, peer); err != nil {
		return err
	}
	data, err := os.ReadFile("/local-credentials.json")
	if err != nil {
		return errors.New("local acceptance credentials unavailable")
	}
	var creds localCredentials
	if json.Unmarshal(data, &creds) != nil || len(creds.UserPassword) != 43 || creds.UserPhone != "19900000001" {
		return errors.New("invalid local acceptance credentials")
	}
	if err = db.BootstrapLocalUser(ctx, creds.UserPassword); err != nil {
		return err
	}
	fmt.Println("Default enterprise passed authenticated runtime readiness and is active.")
	return nil
}
