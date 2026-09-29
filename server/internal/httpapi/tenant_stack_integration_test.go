package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	livekitcontrol "github.com/linli/im/server/internal/livekit"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/tenancy"
	"github.com/redis/go-redis/v9"
)

// This suite intentionally requires three distinct local PostgreSQL endpoints,
// two real WuKongIM instances and Redis. No fallback to production or mocks.
func tenancyStackSchema(t *testing.T, env string) (string, *pgx.Conn) {
	t.Helper()
	raw := os.Getenv(env)
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Scheme != "postgres" {
		t.Fatal("stack tests require explicit loopback PostgreSQL URLs")
	}
	conn, err := pgx.Connect(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("tenant_stack_%d", time.Now().UnixNano())
	if _, err = conn.Exec(t.Context(), `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
		_ = conn.Close(context.Background())
	})
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	// This direct connection is only for test setup/inspection of its own schema.
	if _, err = conn.Exec(t.Context(), `SET search_path TO `+pgx.Identifier{schema}.Sanitize()+`,public`); err != nil {
		t.Fatal(err)
	}
	return u.String(), conn
}

func tenancyStackPKI(t *testing.T, additional ...string) (map[string]*tls.Config, *tls.Config) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated tenancy integration CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	configs := map[string]*tls.Config{}
	for index, id := range append([]string{"platform", "a", "b"}, additional...) {
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		identity := tenancy.EnterpriseIdentity(id)
		if id == "platform" {
			identity = tenancy.PlatformIdentity
		}
		uri, _ := url.Parse(identity)
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(index + 2)), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, URIs: []*url.URL{uri}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
		cert, err := x509.CreateCertificate(rand.Reader, leaf, ca, public, key)
		if err != nil {
			t.Fatal(err)
		}
		configs[id] = &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, Certificates: []tls.Certificate{{Certificate: [][]byte{cert, der}, PrivateKey: private}}}
	}
	return configs, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}
}

func tenancyStackServer(t *testing.T, handler http.Handler, cfg *tls.Config, control bool) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.TLS = cfg.Clone()
	if !control {
		server.TLS.ClientAuth = tls.NoClientCert
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func tenancyStackRequest(t *testing.T, client *http.Client, method, address, token string, body any) (int, map[string]any) {
	t.Helper()
	return tenancyStackDeviceRequest(t, client, "web", method, address, token, body)
}

func tenancyStackDeviceRequest(t *testing.T, client *http.Client, device, method, address, token string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	r, err := http.NewRequestWithContext(t.Context(), method, address, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Client-Platform", device)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var out map[string]any
	if err = json.NewDecoder(response.Body).Decode(&out); err != nil {
		t.Fatal("non JSON stack response", response.StatusCode)
	}
	return response.StatusCode, out
}

// Each disposable database fixture gets a matching Redis namespace. Keep the
// real limits inside a run without inheriting counters from a previous run.
type tenancyStackLimiter struct {
	base  platform.RedisLimiter
	scope string
}

// Captures only the isolated test's generated SMS code; runtime never installs
// a fake provider and no code or capability is logged.
type tenancyStackRecoverySMS struct{ code atomic.Value }

func (s *tenancyStackRecoverySMS) DeliverRecovery(_ context.Context, _, code, _ string) error {
	s.code.Store(code)
	return nil
}

func (l tenancyStackLimiter) Allow(ctx context.Context, key string, max int, window time.Duration) (bool, error) {
	return l.base.Allow(ctx, l.scope+":"+key, max, window)
}

func TestTenantStackRealPostgresRedisMutualTLSAndWukong(t *testing.T) {
	envs := []string{"TENANCY_TEST_PLATFORM_DATABASE_URL", "TENANCY_TEST_A_DATABASE_URL", "TENANCY_TEST_B_DATABASE_URL"}
	for _, env := range envs {
		if os.Getenv(env) == "" {
			t.Skip("explicit isolated three-database stack not configured")
		}
	}
	seen := map[string]bool{}
	for _, env := range envs {
		u, _ := url.Parse(os.Getenv(env))
		if seen[u.Host] {
			t.Fatal("enterprises must use distinct PostgreSQL instances")
		}
		seen[u.Host] = true
	}
	ctx := t.Context()
	pdsn, pconn := tenancyStackSchema(t, envs[0])
	ps, err := platform.Open(ctx, pdsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ps.Close)
	pki, clientTLS := tenancyStackPKI(t)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}, Timeout: 15 * time.Second}
	t.Cleanup(func() { client.CloseIdleConnections() })
	cache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:16373"})
	t.Cleanup(func() { _ = cache.Close() })
	if err = cache.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	peers := platform.EnterpriseRPC{Peers: map[string]*tenancy.RPC{}}
	recoverySMS := &tenancyStackRecoverySMS{}
	pa := &platform.API{Store: ps, Limiter: tenancyStackLimiter{base: platform.RedisLimiter{Client: cache}, scope: fmt.Sprintf("stack-%d", time.Now().UnixNano())}, Peers: peers, RecoverySMS: recoverySMS}
	pushCapture := &tenantStackPushCapture{}
	pa.Push, err = platform.NewPushService(ps, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{27}, 32)), pushCapture, []string{"getui"})
	if err != nil {
		t.Fatal(err)
	}
	var loseNextPushAck atomic.Bool
	var loseNextAdminResponse atomic.Bool
	var loseNextCredentialAck atomic.Bool
	var loseNextAccessAck atomic.Bool
	platformControl := pa.InternalHandler()
	control := tenancyStackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/tenancy/push/deliver" && loseNextPushAck.CompareAndSwap(true, false) {
			capture := httptest.NewRecorder()
			platformControl.ServeHTTP(capture, r)
			if capture.Code != 200 {
				t.Error("push lost-response fixture did not commit", capture.Code)
			}
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/internal/tenancy/admin/accounts" && loseNextAdminResponse.CompareAndSwap(true, false) {
			capture := httptest.NewRecorder()
			platformControl.ServeHTTP(capture, r) // Commit, then lose the response.
			if capture.Code != 200 {
				t.Error("lost-response fixture did not commit")
			}
			w.WriteHeader(503)
			return
		}
		platformControl.ServeHTTP(w, r)
	}), pki["platform"], true)
	public := tenancyStackServer(t, pa.PublicHandler(), pki["platform"], false)
	business := map[string]*httptest.Server{}
	stores := map[string]*store.Postgres{}
	apis := map[string]*API{}
	adminTokens := map[string]string{}
	dbConnections := map[string]*pgx.Conn{}
	dbDSNs := map[string]string{}
	for index, id := range []string{"a", "b"} {
		dsn, conn := tenancyStackSchema(t, envs[index+1])
		dbDSNs[id] = dsn
		dbConnections[id] = conn
		db, err := store.NewPostgresWithOptions(ctx, dsn, store.PostgresOptions{TenantID: id})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(db.Close)
		stores[id] = db
		application, err := app.New(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		cfg := config.Config{TenantID: id, JWTSecret: strings.Repeat(id, 40), MediaSigningSecret: strings.Repeat("media-"+id, 8), AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour, WukongEnabled: true, WukongAPIURL: fmt.Sprintf("http://127.0.0.1:%d", 15574+index), WukongTokenSecret: strings.Repeat("im-"+id, 12), WukongTCPURL: fmt.Sprintf("tcp://127.0.0.1:%d", 15174+index), WukongWSURL: "wss://" + id + ".example/im", HTTPRateLimitPerMinute: 10000}
		cfg.WukongManagerURL = cfg.WukongAPIURL // Manager functions are not used in this auth/revocation test.
		cfg.WukongManagerToken = "isolated-test-only"
		cfg.LiveKitEnabled = true
		cfg.LiveKitAPIURL = fmt.Sprintf("http://127.0.0.1:%d", 17874+index)
		cfg.LiveKitURL = "wss://" + id + ".example/livekit"
		cfg.LiveKitAPIKey = "stack-" + id
		cfg.LiveKitAPISecret = "local-disposable-livekit-" + id + "-tests-only-32bytes"
		cfg.AllowedOrigins = []string{"https://app.example"}
		adminTokens[id] = postgresAdminTestToken(t, db, cfg.JWTSecret)
		x := New(cfg, application)
		if err = x.SetupError(); err != nil {
			t.Fatal(err)
		}
		if err = x.wukongClient.Health(ctx); err != nil {
			t.Fatal("real IM unavailable", id, err)
		}
		rpc, err := tenancy.NewRPC(control.URL, pki[id], tenancy.PlatformIdentity)
		if err != nil {
			t.Fatal(err)
		}
		x.ConfigureTenant(db, rpc)
		apis[id] = x
		controlHandler := x.TenantControlHandler()
		tenantControl := tenancyStackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/internal/tenancy/access" && loseNextAccessAck.CompareAndSwap(true, false) {
				capture := httptest.NewRecorder()
				controlHandler.ServeHTTP(capture, r)
				if capture.Code != 200 {
					t.Error("access revocation fixture did not commit")
				}
				w.WriteHeader(503)
				return
			}
			if r.URL.Path == "/internal/tenancy/credentials/revoke" && loseNextCredentialAck.CompareAndSwap(true, false) {
				capture := httptest.NewRecorder()
				controlHandler.ServeHTTP(capture, r)
				if capture.Code != 200 {
					t.Error("credential revocation fixture did not commit")
				}
				w.WriteHeader(503)
				return
			}
			controlHandler.ServeHTTP(w, r)
		}), pki[id], true)
		peers.Peers[id], err = tenancy.NewRPC(tenantControl.URL, pki["platform"], tenancy.EnterpriseIdentity(id))
		if err != nil {
			t.Fatal(err)
		}
		business[id] = tenancyStackServer(t, x.Handler(), pki[id], false)
		x.cfg.TenantPublicURL = business[id].URL
		if err = ps.PutTenant(ctx, id, id, business[id].URL, "test", "isolated fixture", id == "a"); err != nil {
			t.Fatal(err)
		}
	}
	// Activation is fixture-only, not a production bypass endpoint.
	if _, err = pconn.Exec(ctx, `UPDATE platform_tenants SET status='active'`); err != nil {
		t.Fatal(err)
	}
	// Run migration against a clean directory, before later media tests create
	// deliberately synthetic observer identities without platform accounts.
	// Preflight must reject those inconsistent identities, not waive its checks.
	if !t.Run("legacy default account import", func(t *testing.T) {
		testLegacyImportStack(t, ps, peers, pdsn, dbDSNs["a"], pconn, dbConnections["a"], apis["a"], public.URL, business["a"].URL, client, &loseNextCredentialAck)
	}) {
		t.Fatal("legacy import fixture did not finish; refusing dependent tests")
	}
	creation := map[string]any{"requestId": "stack-admin-1", "phone": "13800000101", "password": "StackPassword123!", "name": "isolated", "gender": "female", "reason": "isolated admin creation", "confirmed": true}
	status, created := tenancyStackRequest(t, client, "POST", business["a"].URL+"/v2/admin/users", adminTokens["a"], creation)
	if status != 202 {
		t.Fatal("managed admin create", status, created["error"])
	}
	jobID := created["item"].(map[string]any)["jobId"].(string)
	if _, ok := created["item"].(map[string]any)["pollToken"]; ok {
		t.Fatal("admin returned user poll capability")
	}
	status, replayed := tenancyStackRequest(t, client, "POST", business["a"].URL+"/v2/admin/users", adminTokens["a"], creation)
	if status != 202 || replayed["item"].(map[string]any)["jobId"] != jobID {
		t.Fatal("admin request retry", status)
	}
	status, denied := tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/admin/users", adminTokens["b"], creation)
	if status != 409 || denied["error"].(map[string]any)["code"] != "ACCOUNT_UNAVAILABLE" {
		t.Fatal("foreign phone accepted or assignment disclosed", status)
	}
	status, foreign := tenancyStackRequest(t, client, "GET", business["b"].URL+"/v2/admin/users/provisioning-jobs?jobId="+jobID, adminTokens["b"], nil)
	if status != 200 || len(foreign["items"].([]any)) != 0 {
		t.Fatal("foreign job disclosed", status)
	}
	worker := platform.Worker{Store: ps, Enterprise: peers}
	for range 2 {
		if _, err = worker.Once(ctx); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := ps.TenantAccountJobs(ctx, "a", jobID)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "completed" {
		t.Fatal("provisioning not complete", err)
	}
	var gender, hash string
	var inviteCount int
	if err = dbConnections["a"].QueryRow(ctx, `SELECT gender,COALESCE(password_hash,''),(SELECT count(*) FROM im_user_invite_codes WHERE user_id=u.id) FROM im_users u WHERE id=$1`, jobs[0].LocalUserID).Scan(&gender, &hash, &inviteCount); err != nil || gender != "female" || hash != "" || inviteCount != 1 {
		t.Fatal("local identity/own invite or authentication ownership incorrect", err)
	}
	status, login := tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-login", "", map[string]string{"phone": "13800000101", "password": "StackPassword123!"})
	if status != 200 {
		t.Fatal("platform login", status, login["error"])
	}
	ticket := login["sessionTicket"].(string)
	status, _ = tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": ticket})
	if status != 401 {
		t.Fatal("cross enterprise ticket accepted", status)
	}
	status, session := tenancyStackRequest(t, client, "POST", business["a"].URL+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": ticket})
	if status != 200 {
		t.Fatal("business exchange", status, session["error"])
	}
	oldToken := session["accessToken"].(string)
	oldUser := session["user"].(map[string]any)["id"].(string)
	verifyRetiredPush := tenancyStackPushBridge(t, client, public.URL, control.URL, pki, stores["a"], dbConnections["a"], oldUser, login["refreshToken"].(string), pushCapture, &loseNextPushAck)
	oldCall := tenancyStackMediaJoin(t, client, clientTLS, business["a"].URL, apis["a"], stores["a"], dbConnections["a"], oldUser, oldToken, "transfer")
	status, _ = tenancyStackRequest(t, client, "POST", business["a"].URL+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": ticket})
	if status != 401 {
		t.Fatal("ticket replay", status)
	}
	status, _ = tenancyStackRequest(t, client, "GET", business["a"].URL+"/v2/users/me", oldToken, nil)
	if status != 200 {
		t.Fatal("direct business access", status)
	}
	status, _ = tenancyStackRequest(t, client, "GET", business["b"].URL+"/v2/users/me", oldToken, nil)
	if status != 401 {
		t.Fatal("foreign JWT accepted", status)
	}
	identity, err := stores["a"].TenantIdentity(ctx, "a", oldUser)
	if err != nil {
		t.Fatal(err)
	}
	mediaFault := &tenantStackMediaFault{Control: apis["a"].livekit.(*livekitcontrol.Control)}
	mediaFault.fail.Store(true)
	apis["a"].livekit = mediaFault
	transferID, err := ps.RequestTransfer(ctx, identity.AccountID, "b", "test", "isolation transfer", true, peers)
	if err != nil {
		t.Fatal(err)
	}
	if worked, e := worker.Once(ctx); e != nil || !worked {
		t.Fatal("media failure step", worked, e)
	}
	var step string
	if err = pconn.QueryRow(ctx, `SELECT step FROM platform_jobs WHERE id=$1`, transferID).Scan(&step); err != nil || step != "revoke_source" {
		t.Fatal("advanced past unconfirmed media revocation", step, err)
	}
	var targetIdentities int
	if err = dbConnections["b"].QueryRow(ctx, `SELECT count(*) FROM im_users WHERE platform_account_id=$1`, identity.AccountID).Scan(&targetIdentities); err != nil || targetIdentities != 0 {
		t.Fatal("activated another enterprise with old call still live", err)
	}
	select {
	case <-oldCall.disconnected:
		t.Fatal("fault fixture had no live source connection")
	default:
	}
	mediaFault.fail.Store(false)
	if _, err = pconn.Exec(ctx, `UPDATE platform_jobs SET retry_at=now() WHERE id=$1`, transferID); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err = worker.Once(ctx); err != nil {
			t.Fatal("transfer step", err)
		}
	}
	oldCall.assertRevoked(t, client, business["a"].URL, apis["a"])
	status, _ = tenancyStackRequest(t, client, "GET", business["a"].URL+"/v2/users/me", oldToken, nil)
	if status != 401 {
		t.Fatal("retired identity remained usable", status)
	}
	status, login = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-login", "", map[string]string{"phone": "13800000101", "password": "StackPassword123!"})
	if status != 200 {
		t.Fatal("login after transfer", status)
	}
	context := login["tenantContext"].(map[string]any)
	if context["tenantId"] != "b" || context["assignmentVersion"] != float64(2) {
		t.Fatal("wrong new assignment")
	}
	status, session = tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": login["sessionTicket"].(string)})
	if status != 200 {
		t.Fatal("target exchange", status, session["error"])
	}
	verifyRetiredPush()
	if session["user"].(map[string]any)["id"] == oldUser {
		t.Fatal("reused source identity")
	}
	status, _ = tenancyStackRequest(t, client, "GET", business["a"].URL+"/v2/users/me", session["accessToken"].(string), nil)
	if status != 401 {
		t.Fatal("new token accepted by old enterprise")
	}
	// Password rotation retains the target business identity. A response lost
	// after real IM revocation must keep login frozen, then resume safely.
	beforePasswordToken := session["accessToken"].(string)
	beforePasswordSession := session
	currentUser := session["user"].(map[string]any)["id"].(string)
	passwordCall := tenancyStackMediaJoin(t, client, clientTLS, business["b"].URL, apis["b"], stores["b"], dbConnections["b"], currentUser, beforePasswordToken, "password")
	refresh := login["refreshToken"].(string)
	passwordBody := map[string]string{"requestId": "stack-password-1", "currentPassword": "StackPassword123!", "newPassword": "RotatedPassword123!"}
	status, rotation := tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-change", refresh, passwordBody)
	if status != 202 {
		t.Fatal("password task not accepted", status, rotation["error"])
	}
	rotationID := rotation["jobId"].(string)
	status, passwordRecovered := tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-change/status", refresh, map[string]string{"requestId": "stack-password-1"})
	if status != 200 || passwordRecovered["jobId"] != rotationID || passwordRecovered["status"] != "pending" {
		t.Fatal("lost password acceptance not recoverable by request id", status)
	}
	status, _ = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-change/status", strings.Repeat("z", 43), map[string]string{"requestId": "stack-password-1"})
	if status != 401 {
		t.Fatal("password status leaked to foreign credential", status)
	}
	cw := platform.CredentialWorker{Store: ps, Enterprise: peers}
	loseNextCredentialAck.Store(true)
	if worked, e := cw.Once(ctx); e != nil || !worked {
		t.Fatal("credential worker", worked, e)
	}
	status, progress := tenancyStackRequest(t, client, "GET", public.URL+"/v2/auth/credential-jobs/"+rotationID, refresh, nil)
	if status != 200 || progress["status"] != "pending" {
		t.Fatal("unconfirmed reset reported complete", status)
	}
	passwordCall.assertRevoked(t, client, business["b"].URL, apis["b"])
	status, _ = tenancyStackRequest(t, client, "GET", business["b"].URL+"/v2/users/me", beforePasswordToken, nil)
	if status != 401 {
		t.Fatal("old API token survived reset", status)
	}
	status, _ = tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/auth/im-session", beforePasswordToken, map[string]any{})
	if status != 401 {
		t.Fatal("old API token recreated IM session", status)
	}
	status, _ = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-login", "", map[string]string{"phone": "13800000101", "password": "RotatedPassword123!"})
	if status != 401 {
		t.Fatal("login resumed before revocation acknowledgement", status)
	}
	if _, err = pconn.Exec(ctx, `UPDATE platform_credential_jobs SET retry_at=now() WHERE id=$1`, rotationID); err != nil {
		t.Fatal(err)
	}
	if worked, e := cw.Once(ctx); e != nil || !worked {
		t.Fatal(worked, e)
	}
	status, progress = tenancyStackRequest(t, client, "GET", public.URL+"/v2/auth/credential-jobs/"+rotationID, refresh, nil)
	if status != 200 || progress["status"] != "completed" {
		t.Fatal("reset retry failed", status)
	}
	status, passwordRecovered = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-change/status", refresh, map[string]string{"requestId": "stack-password-1"})
	if status != 200 || passwordRecovered["status"] != "completed" {
		t.Fatal("completed password task not recovered", status)
	}
	status, _ = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-change", refresh, passwordBody)
	if status != 202 {
		t.Fatal("reset replay rejected", status)
	}
	status, login = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-login", "", map[string]string{"phone": "13800000101", "password": "RotatedPassword123!"})
	if status != 200 {
		t.Fatal("new password login", status)
	}
	status, session = tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": login["sessionTicket"].(string)})
	if status != 200 || session["user"].(map[string]any)["id"] != currentUser {
		t.Fatal("reset changed local identity", status)
	}
	if session["imSession"].(map[string]any)["token"] == beforePasswordSession["imSession"].(map[string]any)["token"] {
		t.Fatal("password reset restored old IM token")
	}
	tenancyStackIMConnect(t, beforePasswordSession, false)
	identityB, version, err := stores["b"].TenantAuthIdentity(ctx, "b", currentUser)
	if err != nil {
		t.Fatal(err)
	}
	if err = peers.RevokeCredentials(ctx, tenancy.CredentialOperation{OperationID: rotationID, Identity: identityB, AuthVersion: version}); err != nil {
		t.Fatal(err)
	}
	status, _ = tenancyStackRequest(t, client, "GET", business["b"].URL+"/v2/users/me", session["accessToken"].(string), nil)
	if status != 200 {
		t.Fatal("duplicate acknowledgement revoked new session", status)
	}
	adminReset := map[string]any{"requestId": "stack-admin-reset", "newPassword": "AdminResetPassword123!", "reason": "isolated reset", "confirmed": true}
	status, _ = tenancyStackRequest(t, client, "POST", business["a"].URL+"/v2/admin/users/"+currentUser+"/tenant-password-reset", adminTokens["a"], adminReset)
	if status != 409 {
		t.Fatal("foreign enterprise reset target", status)
	}
	if _, err = dbConnections["b"].Exec(ctx, `UPDATE im_admin_accounts SET role_id='support' WHERE id='test-admin'`); err != nil {
		t.Fatal(err)
	}
	status, _ = tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/admin/users/"+currentUser+"/tenant-password-reset", adminTokens["b"], adminReset)
	if status != 403 {
		t.Fatal("read-only admin reset password", status)
	}
	if _, err = dbConnections["b"].Exec(ctx, `UPDATE im_admin_accounts SET role_id='platform_admin' WHERE id='test-admin'`); err != nil {
		t.Fatal(err)
	}
	status, adminTask := tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/admin/users/"+currentUser+"/tenant-password-reset", adminTokens["b"], adminReset)
	if status != 200 || adminTask["item"].(map[string]any)["status"] != "pending" {
		t.Fatal("admin reset task", status)
	}
	if _, err = cw.Once(ctx); err != nil {
		t.Fatal(err)
	}
	status, login = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-login", "", map[string]string{"phone": "13800000101", "password": "AdminResetPassword123!"})
	if status != 200 {
		t.Fatal("admin reset never completed", status)
	}
	status, session = tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": login["sessionTicket"].(string)})
	if status != 200 {
		t.Fatal("pre-recovery session", status)
	}
	beforeRecoveryToken := session["accessToken"].(string)
	capability, err := tenancy.Secret()
	if err != nil {
		t.Fatal(err)
	}
	status, response := tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-reset/code", "", map[string]string{"requestId": "stack-recovery", "phone": "13800000101", "queryToken": capability})
	if status != 200 || response["ok"] != true || len(response) != 2 {
		t.Fatal("recovery challenge response", status)
	}
	recoveryBody := map[string]any{"requestId": "stack-recovery", "code": recoverySMS.code.Load().(string), "newPassword": "RecoveredPassword123!", "confirmed": false}
	status, _ = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-reset", capability, recoveryBody)
	if status != 400 {
		t.Fatal("recovery without confirmation", status)
	}
	recoveryBody["confirmed"] = true
	status, response = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-reset", capability, recoveryBody)
	if status != 202 || response["status"] != "pending" {
		t.Fatal("recovery not accepted", status)
	}
	recoveryQuery := map[string]string{"requestId": "stack-recovery"}
	status, response = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-reset/status", capability, recoveryQuery)
	if status != 200 || response["status"] != "pending" {
		t.Fatal("recovery pending status", status)
	}
	foreignCapability, _ := tenancy.Secret()
	status, _ = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-reset/status", foreignCapability, recoveryQuery)
	if status != 401 {
		t.Fatal("foreign recovery query", status)
	}
	if worked, e := cw.Once(ctx); e != nil || !worked {
		t.Fatal("recovery revocation worker", worked, e)
	}
	status, response = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-reset/status", capability, recoveryQuery)
	if status != 200 || response["status"] != "completed" {
		t.Fatal("recovery completion not reconciled", status)
	}
	status, _ = tenancyStackRequest(t, client, "GET", business["b"].URL+"/v2/users/me", beforeRecoveryToken, nil)
	if status != 401 {
		t.Fatal("pre-recovery API survived", status)
	}
	status, _ = tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/auth/im-session", beforeRecoveryToken, map[string]any{})
	if status != 401 {
		t.Fatal("pre-recovery API regained IM", status)
	}
	status, login = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-login", "", map[string]string{"phone": "13800000101", "password": "RecoveredPassword123!"})
	if status != 200 {
		t.Fatal("recovered password cannot login", status)
	}
	status, session = tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": login["sessionTicket"].(string)})
	if status != 200 || session["user"].(map[string]any)["id"] != currentUser {
		t.Fatal("recovery changed business identity", status)
	}
	status, _ = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-reset", capability, recoveryBody)
	if status != 202 {
		t.Fatal("recovery replay not idempotent", status)
	}
	// A fresh fixture keeps ban tests below the unchanged per-account login
	// limit, rather than bypassing Redis or clearing another test's counters.
	{
		status, _ := tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/admin/users", adminTokens["b"], map[string]any{"requestId": "stack-ban-user", "phone": "13800000102", "password": "RecoveredPassword123!", "name": "ban fixture", "gender": "unspecified", "reason": "isolated global access", "confirmed": true})
		if status != 202 {
			t.Fatal("ban fixture creation", status)
		}
		for range 2 {
			if _, err := worker.Once(ctx); err != nil {
				t.Fatal(err)
			}
		}
		status, login := tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-login", "", map[string]string{"phone": "13800000102", "password": "RecoveredPassword123!"})
		if status != 200 {
			t.Fatal("ban fixture login", status)
		}
		status, session := tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": login["sessionTicket"].(string)})
		if status != 200 {
			t.Fatal("ban fixture session", status)
		}
		currentUser := session["user"].(map[string]any)["id"].(string)
		// Global access uses its own durable task and preserves business identity.
		beforeBanToken := session["accessToken"].(string)
		beforeBanSession := session
		banCall := tenancyStackMediaJoin(t, client, clientTLS, business["b"].URL, apis["b"], stores["b"], dbConnections["b"], currentUser, beforeBanToken, "ban")
		identityB, version, err := stores["b"].TenantAuthIdentity(ctx, "b", currentUser)
		if err != nil {
			t.Fatal(err)
		}
		banJob, err := ps.RequestAccess(ctx, identityB.AccountID, "stack-ban", "test-operator", "isolated global ban", version, true, true)
		if err != nil {
			t.Fatal(err)
		}
		status, _ = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-login", "", map[string]string{"phone": "13800000102", "password": "RecoveredPassword123!"})
		if status != 401 {
			t.Fatal("login not frozen immediately", status)
		}
		aw := platform.AccessWorker{Store: ps, Enterprise: peers}
		loseNextAccessAck.Store(true)
		if worked, e := aw.Once(ctx); e != nil || !worked {
			t.Fatal(worked, e)
		}
		banCall.assertRevoked(t, client, business["b"].URL, apis["b"])
		if err = pconn.QueryRow(ctx, `SELECT state FROM platform_access_jobs WHERE id=$1`, banJob.ID).Scan(&step); err != nil || step != "applying" {
			t.Fatal("lost ban ACK reported complete", step, err)
		}
		for _, path := range []string{"/v2/users/me", "/v2/auth/im-session"} {
			method := "GET"
			if path == "/v2/auth/im-session" {
				method = "POST"
			}
			status, _ = tenancyStackRequest(t, client, method, business["b"].URL+path, beforeBanToken, map[string]any{})
			if status != 401 {
				t.Fatal("banned token accepted", path, status)
			}
		}
		if _, err = pconn.Exec(ctx, `UPDATE platform_access_jobs SET retry_at=now() WHERE id=$1`, banJob.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = aw.Once(ctx); err != nil {
			t.Fatal(err)
		}
		if err = pconn.QueryRow(ctx, `SELECT state FROM platform_access_jobs WHERE id=$1`, banJob.ID).Scan(&step); err != nil || step != "completed" {
			t.Fatal("ban retry failed", step, err)
		}
		// A local ban remains in place after the platform lifts its own ban.
		if _, err = dbConnections["b"].Exec(ctx, `UPDATE im_users SET banned=true WHERE id=$1`, currentUser); err != nil {
			t.Fatal(err)
		}
		unbanJob, err := ps.RequestAccess(ctx, identityB.AccountID, "stack-unban", "test-operator", "isolated global unblock", version+1, false, true)
		if err != nil {
			t.Fatal(err)
		}
		loseNextAccessAck.Store(true)
		if _, err = aw.Once(ctx); err != nil {
			t.Fatal(err)
		}
		status, _ = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-login", "", map[string]string{"phone": "13800000102", "password": "RecoveredPassword123!"})
		if status != 401 {
			t.Fatal("unconfirmed unban allowed login", status)
		}
		if _, err = pconn.Exec(ctx, `UPDATE platform_access_jobs SET retry_at=now() WHERE id=$1`, unbanJob.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = aw.Once(ctx); err != nil {
			t.Fatal(err)
		}
		status, login = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-login", "", map[string]string{"phone": "13800000102", "password": "RecoveredPassword123!"})
		if status != 200 {
			t.Fatal("global unban did not permit authentication", status)
		}
		status, _ = tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": login["sessionTicket"].(string)})
		if status != 403 {
			t.Fatal("local ban bypassed", status)
		}
		if _, err = dbConnections["b"].Exec(ctx, `UPDATE im_users SET banned=false WHERE id=$1`, currentUser); err != nil {
			t.Fatal(err)
		}
		status, login = tenancyStackRequest(t, client, "POST", public.URL+"/v2/auth/password-login", "", map[string]string{"phone": "13800000102", "password": "RecoveredPassword123!"})
		if status != 200 {
			t.Fatal(status)
		}
		status, session = tenancyStackRequest(t, client, "POST", business["b"].URL+"/v2/auth/tenant-session", "", map[string]string{"sessionTicket": login["sessionTicket"].(string)})
		if status != 200 || session["user"].(map[string]any)["id"] != currentUser {
			t.Fatal("unban lost identity", status)
		}
		if session["imSession"].(map[string]any)["token"] == beforeBanSession["imSession"].(map[string]any)["token"] {
			t.Fatal("unban restored old IM token")
		}
		tenancyStackIMConnect(t, beforeBanSession, false)
		if err = peers.SetAccess(ctx, tenancy.AccessOperation{CredentialOperation: tenancy.CredentialOperation{OperationID: banJob.ID, Identity: identityB, AuthVersion: version + 1}, Blocked: true}); err != nil {
			t.Fatal("old completed ban replay", err)
		}
		status, _ = tenancyStackRequest(t, client, "GET", business["b"].URL+"/v2/users/me", session["accessToken"].(string), nil)
		if status != 200 {
			t.Fatal("replay revoked new session", status)
		}
		status, _ = tenancyStackRequest(t, client, "GET", business["b"].URL+"/v2/users/me", beforeBanToken, nil)
		if status != 401 {
			t.Fatal("old API token restored by unban", status)
		}
	}
	// Enterprise roles stay independent: a read-only admin cannot use the
	// platform account-creation bridge, and a foreign admin JWT cannot cross.
	if _, err = dbConnections["a"].Exec(ctx, `UPDATE im_admin_accounts SET role_id='support' WHERE id='test-admin'`); err != nil {
		t.Fatal(err)
	}
	status, _ = tenancyStackRequest(t, client, "POST", business["a"].URL+"/v2/admin/users", adminTokens["a"], creation)
	if status != 403 {
		t.Fatal("read-only enterprise administrator created account", status)
	}
	if _, err = dbConnections["a"].Exec(ctx, `UPDATE im_admin_accounts SET role_id='platform_admin' WHERE id='test-admin'`); err != nil {
		t.Fatal(err)
	}
	status, _ = tenancyStackRequest(t, client, "POST", business["a"].URL+"/v2/admin/users", adminTokens["b"], creation)
	if status != 401 {
		t.Fatal("foreign enterprise admin token accepted", status)
	}
	// Administrative creation is exempt from public registration/invite policy,
	// not password policy. Each row has an independently recoverable request.
	if _, err = dbConnections["a"].Exec(ctx, `INSERT INTO im_settings(key,value) VALUES('registrationEnabled','false'),('inviteRegistrationMode','"required"') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		t.Fatal(err)
	}
	batch := map[string]any{"requestId": "stack-batch", "reason": "isolated batch", "confirmed": true, "items": []map[string]any{
		{"clientRow": 2, "phone": "02800000201", "name": "batch valid", "gender": "male", "password": "StackPassword123!"},
		{"clientRow": 3, "phone": "13800000101", "name": "already belongs elsewhere", "gender": "male", "password": "StackPassword123!"},
		{"clientRow": 4, "phone": "bad", "name": "invalid", "gender": "male", "password": "StackPassword123!"},
	}}
	status, batchResult := tenancyStackRequest(t, client, "POST", business["a"].URL+"/v2/admin/users/batch", adminTokens["a"], batch)
	if status != 200 || batchResult["pending"] != float64(1) || batchResult["failed"] != float64(2) {
		t.Fatal("managed batch result", status, batchResult)
	}
	batchJob := batchResult["items"].([]any)[0].(map[string]any)["job"].(map[string]any)["jobId"]
	for range 2 {
		if _, err = worker.Once(ctx); err != nil {
			t.Fatal(err)
		}
	}
	status, batchResult = tenancyStackRequest(t, client, "POST", business["a"].URL+"/v2/admin/users/batch", adminTokens["a"], batch)
	if status != 200 || batchResult["succeeded"] != float64(1) || batchResult["items"].([]any)[0].(map[string]any)["job"].(map[string]any)["jobId"] != batchJob {
		t.Fatal("batch replay not idempotent", status)
	}
	status, _ = tenancyStackRequest(t, client, "POST", business["a"].URL+"/v2/admin/users/batch", adminTokens["a"], map[string]any{"requestId": "oversized", "reason": "test limit", "confirmed": true, "items": make([]map[string]any, 101)})
	if status != 400 {
		t.Fatal("101 row batch accepted", status)
	}
	status, _ = tenancyStackRequest(t, client, "POST", business["a"].URL+"/v2/admin/users/batch", adminTokens["a"], map[string]any{"requestId": "duplicate-row", "reason": "test duplicate", "confirmed": true, "items": []map[string]any{{"clientRow": 2}, {"clientRow": 2}}})
	if status != 400 {
		t.Fatal("duplicate row identifiers accepted", status)
	}
	lost := map[string]any{"requestId": "response-lost", "phone": "02800000301", "name": "lost response", "gender": "unspecified", "password": "StackPassword123!", "reason": "lost response fixture", "confirmed": true}
	loseNextAdminResponse.Store(true)
	status, failed := tenancyStackRequest(t, client, "POST", business["a"].URL+"/v2/admin/users", adminTokens["a"], lost)
	if status != 503 || failed["error"].(map[string]any)["code"] != "PLATFORM_UNAVAILABLE" {
		t.Fatal("lost response was reported as definite success/failure", status)
	}
	status, recovered := tenancyStackRequest(t, client, "POST", business["a"].URL+"/v2/admin/users", adminTokens["a"], lost)
	if status != 202 {
		t.Fatal("committed task could not be resumed", status)
	}
	var taskCount int
	if err = pconn.QueryRow(ctx, `SELECT count(*) FROM platform_jobs WHERE request_id='response-lost'`).Scan(&taskCount); err != nil || taskCount != 1 {
		t.Fatal("lost response duplicated durable work", taskCount, err)
	}
	if recovered["item"].(map[string]any)["status"] != "pending" {
		t.Fatal("unprocessed task misreported")
	}
	t.Run("enterprise suspension", func(t *testing.T) {
		testTenantRealmStack(t, ps, peers, public.URL, client, clientTLS, business, apis, stores, dbConnections, pconn, adminTokens)
	})
}
