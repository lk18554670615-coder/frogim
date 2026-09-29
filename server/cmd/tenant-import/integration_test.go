package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/legacyimport"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

func importCommandDB(t *testing.T, env string) (string, *pgxpool.Pool) {
	t.Helper()
	raw := os.Getenv(env)
	if raw == "" {
		t.Skip("explicit disposable PostgreSQL not configured")
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "postgres" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || u.Path != "/tenancy_test" || u.RawQuery != "sslmode=disable" {
		t.Fatal("command tests require disposable loopback tenancy_test")
	}
	c, e := pgx.Connect(t.Context(), raw)
	if e != nil {
		t.Fatal("isolated database unavailable")
	}
	var id [12]byte
	if _, e = rand.Read(id[:]); e != nil {
		t.Fatal(e)
	}
	schema := "import_command_" + hex.EncodeToString(id[:])
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, e = c.Exec(t.Context(), "CREATE SCHEMA "+quoted); e != nil {
		c.Close(context.Background())
		t.Fatal("isolated schema failed")
	}
	t.Cleanup(func() {
		defer c.Close(context.Background())
		if _, e := c.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); e != nil {
			t.Error("isolated schema cleanup failed")
		}
	})
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	p, e := pgxpool.New(t.Context(), u.String())
	if e != nil {
		t.Fatal("isolated pool failed")
	}
	t.Cleanup(p.Close)
	return u.String(), p
}

// Temporary test certificates only; no machine trust store or runtime key files.
func importCommandTLS(t *testing.T) (string, string, string, *tls.Config) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	ca, e = x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if os.WriteFile(path, data, 0600) != nil {
			t.Fatal("temporary PKI file failed")
		}
		return path
	}
	caFile := write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	var certFile, keyFile string
	var server *tls.Config
	for n, identity := range []string{tenancy.PlatformIdentity, tenancy.EnterpriseIdentity("default")} {
		public, private, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		uri, _ := url.Parse(identity)
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(n + 2)), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, URIs: []*url.URL{uri}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
		cert, e := x509.CreateCertificate(rand.Reader, leaf, ca, public, key)
		if e != nil {
			t.Fatal(e)
		}
		if n == 0 {
			encoded, e := x509.MarshalPKCS8PrivateKey(private)
			if e != nil {
				t.Fatal(e)
			}
			certFile = write("platform.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}))
			keyFile = write("platform.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
		} else {
			server = &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, Certificates: []tls.Certificate{{Certificate: [][]byte{cert, der}, PrivateKey: private}}}
		}
	}
	return caFile, certFile, keyFile, server
}

func TestPostgresImportCommandStartStatusAndMutualTLSResume(t *testing.T) {
	sourceURL, s := importCommandDB(t, "IM_TEST_DATABASE_URL")
	targetURL, p := importCommandDB(t, "PLATFORM_TEST_DATABASE_URL")
	ctx := t.Context()
	source, e := store.NewPostgresWithOptions(ctx, sourceURL, store.PostgresOptions{TenantID: "default"})
	if e != nil {
		t.Fatal("fixture enterprise migration failed")
	}
	defer source.Close()
	target, e := platform.Open(ctx, targetURL)
	if e != nil {
		t.Fatal("fixture platform migration failed")
	}
	defer target.Close()
	if target.PutTenant(ctx, "default", "Default", "https://default.example", "fixture", "isolated CLI test", true) != nil {
		t.Fatal("fixture tenant failed")
	}
	exec := func(pool *pgxpool.Pool, sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal("isolated fixture mutation failed")
		}
	}
	// Only setup supplies acknowledged suspension; this command test makes no
	// claims about real sockets. The HTTP API stack suite covers real IM revocation.
	exec(s, `UPDATE im_tenant_identity SET access_version=2,access_enabled=false;
INSERT INTO im_tenant_realm_operations(operation_id,access_version,enabled,state) VALUES('cmd-pause',2,false,'completed')`)
	exec(p, `UPDATE platform_tenants SET status='suspended',access_version=2;
INSERT INTO platform_realm_jobs(id,request_id,actor_id,tenant_id,enabled,expected_version,access_version,reason,state) VALUES('cmd-pause','cmd-pause','fixture','default',false,1,2,'isolated CLI pause','completed')`)
	hash, e := bcrypt.GenerateFromPassword([]byte("CommandFixturePassword123!"), 4)
	if e != nil {
		t.Fatal(e)
	}
	exec(s, `INSERT INTO im_users(id,phone,name,password_hash,created_at) VALUES('historical-command','19900000778','legacy command',$1,now())`, string(hash))
	r, e := legacyimport.Preflight(ctx, s, p, "default", time.Now().UTC())
	if e != nil || !r.DataChecksPassed {
		t.Fatal("fixture preflight failed")
	}
	ca, cert, key, tlsConfig := importCommandTLS(t)
	var calls atomic.Int32
	var loseAck atomic.Bool
	loseAck.Store(true)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tenancy.PeerIdentity(r) != tenancy.PlatformIdentity || r.Method != "POST" || r.URL.Path != "/internal/tenancy/credentials/revoke" {
			w.WriteHeader(403)
			return
		}
		calls.Add(1)
		var op tenancy.CredentialOperation
		if json.NewDecoder(r.Body).Decode(&op) != nil || op.Identity.TenantID != "default" {
			w.WriteHeader(400)
			return
		}
		e := source.WithTenantSessionFence(r.Context(), op.Identity.LocalUserID, func() error {
			done, e := source.BeginTenantCredentialRevocation(r.Context(), op)
			if e != nil || done {
				return e
			}
			return source.FinishTenantCredentialRevocation(r.Context(), op)
		})
		if e != nil || loseAck.CompareAndSwap(true, false) {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(tenancy.CredentialAck{CredentialOperation: op, State: "completed"})
	}))
	srv.TLS = tlsConfig
	srv.StartTLS()
	defer srv.Close()
	control := filepath.Join(t.TempDir(), "control.json")
	data, _ := json.Marshal(map[string]string{"tenantId": "default", "baseUrl": srv.URL, "caFile": ca, "certFile": cert, "keyFile": key})
	if os.WriteFile(control, data, 0600) != nil {
		t.Fatal("temporary control config failed")
	}
	env := map[string]string{"TENANCY_IMPORT_ENV": "development", "TENANCY_IMPORT_OFFLINE_CUTOVER_CONFIRMED": "true", "TENANCY_IMPORT_PLATFORM_DATABASE_URL": targetURL, "TENANCY_IMPORT_SOURCE_DATABASE_URL": sourceURL, "TENANCY_IMPORT_CONTROL_FILE": control}
	invoke := func(want int, args ...string) string {
		t.Helper()
		var out, diagnostic bytes.Buffer
		code := run(ctx, args, func(k string) string { return env[k] }, &out, &diagnostic)
		if code != want {
			t.Fatal("unexpected CLI result", code, diagnostic.String())
		}
		for _, secret := range []string{sourceURL, targetURL, string(hash), "CommandFixturePassword123!", "19900000778", "historical-command", key} {
			if strings.Contains(out.String()+diagnostic.String(), secret) {
				t.Fatal("CLI disclosed credential or account data")
			}
		}
		return out.String()
	}
	start := []string{"-mode", "start", "-batch", "command-batch", "-actor", "operator", "-reason", "isolated import", "-confirmed", "-expected-fingerprint", r.Fingerprint}
	invoke(3, start...)
	invoke(3, start...) // Stable request replay, not a second account.
	invoke(3, "-mode", "status", "-batch", "command-batch")
	invoke(2, "-mode", "resume", "-batch", "command-batch") // Commit, then lose mTLS acknowledgement.
	invoke(3, "-mode", "status", "-batch", "command-batch")
	exec(p, `UPDATE platform_legacy_import_items SET retry_at=now()`)
	invoke(0, "-mode", "resume", "-batch", "command-batch")
	if !strings.Contains(invoke(0, "-mode", "status", "-batch", "command-batch"), `"completed":1`) {
		t.Fatal("CLI completion count missing")
	}
	if calls.Load() != 2 {
		t.Fatal("resume bypassed real mTLS handler")
	}
	invoke(0, "-mode", "resume", "-batch", "command-batch")
	if calls.Load() != 2 {
		t.Fatal("completed replay called revocation again")
	}
	var enabled bool
	var state string
	if s.QueryRow(ctx, `SELECT access_enabled FROM im_tenant_identity`).Scan(&enabled) != nil || enabled || p.QueryRow(ctx, `SELECT status FROM platform_tenants`).Scan(&state) != nil || state != "suspended" {
		t.Fatal("CLI resumed enterprise service")
	}
}
