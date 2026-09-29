package platform

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

// Fault injection happens AFTER the real agent accepted the request. It never
// synthesizes inspection, execution, health, identity or readiness results.
type deploymentStackLostACK struct {
	AgentRPC
	lose       bool
	loseBackup bool
}

func (a *deploymentStackLostACK) Backup(ctx context.Context, server, action string, in deployment.BackupControlRequest) (deployment.BackupControlReport, error) {
	out, e := a.AgentRPC.Backup(ctx, server, action, in)
	if e == nil && action == "submit" && a.loseBackup {
		a.loseBackup = false
		return deployment.BackupControlReport{}, errors.New("injected backup acknowledgement loss")
	}
	return out, e
}

func (a *deploymentStackLostACK) Deployment(ctx context.Context, server, action string, in deployment.ControlRequest) (deployment.ControlReport, error) {
	out, e := a.AgentRPC.Deployment(ctx, server, action, in)
	if e == nil && action == "submit" && a.lose {
		a.lose = false
		return deployment.ControlReport{}, errors.New("injected lost acknowledgement")
	}
	return out, e
}

// Explicit Docker-local opt-in. This executes the real Linux agent binary and
// nine fresh services, not a fake runner or a hand-written Linux runtime report.
func TestPlatformPostgresDeploymentFullStack(t *testing.T) {
	if os.Getenv("TENANCY_DEPLOYMENT_STACK_TEST") != "local" {
		t.Skip("full platform deployment rehearsal not explicitly enabled")
	}
	pinned := regexp.MustCompile(`^frogim/[a-z-]+@sha256:[a-f0-9]{64}$`)
	image, agentImage := os.Getenv("TENANCY_ENTERPRISE_BUNDLE_IMAGE"), os.Getenv("TENANCY_AGENT_IMAGE")
	if !pinned.MatchString(image) || !pinned.MatchString(agentImage) {
		t.Fatal("preloaded immutable local bundle and agent images required")
	}
	dsn, e := url.Parse(os.Getenv("PLATFORM_TEST_DATABASE_URL"))
	if e != nil || dsn.Scheme != "postgres" || dsn.Hostname() != "127.0.0.1" || dsn.Port() != "15473" || dsn.Path != "/tenancy_test" || dsn.User == nil || dsn.User.Username() != "tenancy_test" {
		t.Fatal("full-stack rehearsal only accepts the dedicated local disposable platform database")
	}
	s := isolatedPlatform(t)
	issue := deploymentStackPKI(t)
	platformCert := issue(tenancy.PlatformIdentity)
	api := &API{Store: s, Limiter: testLimit{}} // limiter behavior is tested separately
	control := httptest.NewUnstartedServer(api.InternalHandler())
	control.TLS = deploymentStackTLS(t, platformCert)
	control.StartTLS()
	t.Cleanup(control.Close)
	public := httptest.NewUnstartedServer(api.PublicHandler())
	public.TLS = deploymentStackTLS(t, platformCert)
	public.TLS.ClientAuth = tls.NoClientCert
	// Start after wiring immutable peers/catalog below; no runtime map mutation.
	var suffix [6]byte
	if _, e := rand.Read(suffix[:]); e != nil {
		t.Fatal("fixture identity generation failed")
	}
	tenant := "full-" + hex.EncodeToString(suffix[:])
	secrets, e := deployment.NewEnterpriseSecrets("Synthetic-Enterprise-Admin-Only!")
	if e != nil {
		t.Fatal("fixture secrets unavailable")
	}
	c := deployment.EnterpriseConfig{TenantID: tenant, ServerID: "server-" + tenant, ToolsImage: image, PlatformControlURL: strings.Replace(control.URL, "127.0.0.1", "host.docker.internal", 1), PlatformWebOrigin: "https://127.0.0.1:18443", Secrets: secrets, ControlTLS: issue(tenancy.EnterpriseIdentity(tenant)), PublicTLS: issue("")}
	c.Ports = deployment.EnterprisePorts{HTTP: deploymentStackPort(t), Media: deploymentStackPort(t), IM: deploymentStackPort(t), Control: deploymentStackPort(t), RTCTCP: deploymentStackPort(t), RTCUDPFrom: deploymentStackPort(t)}
	root := t.TempDir()
	one, e := deployment.WriteEnterpriseRelease(root, c, deployment.Release{ID: "initial", Sequence: 1, Runtime: "linux/amd64", SchemaVersion: 79})
	if e != nil {
		t.Fatal("initial enterprise artifact invalid", e)
	}
	two, e := deployment.WriteEnterpriseRelease(root, c, deployment.Release{ID: "upgrade", Sequence: 2, Runtime: "linux/amd64", SchemaVersion: 79, RollbackTo: []string{one.ID}})
	if e != nil {
		t.Fatal("upgrade enterprise artifact invalid", e)
	}
	agentPort := deploymentStackPort(t)
	docker := deploymentStackStartAgent(t, c, issue(deployment.AgentIdentity(c.ServerID)), []deployment.Release{one, two}, root, agentImage, agentPort)
	before := docker.defaultSnapshot(t)
	agentRPC, e := tenancy.NewRPC(fmt.Sprintf("https://127.0.0.1:%d", agentPort), deploymentStackTLS(t, platformCert), deployment.AgentIdentity(c.ServerID))
	if e != nil {
		t.Fatal("agent peer invalid")
	}
	peer := &deploymentStackLostACK{AgentRPC: AgentRPC{Peers: map[string]*tenancy.RPC{c.ServerID: agentRPC}}}
	enterpriseRPC, e := tenancy.NewRPC(fmt.Sprintf("https://127.0.0.1:%d", c.Ports.Control), deploymentStackTLS(t, platformCert), tenancy.EnterpriseIdentity(tenant))
	if e != nil {
		t.Fatal("enterprise peer invalid")
	}
	api.Peers = EnterpriseRPC{Peers: map[string]*tenancy.RPC{tenant: enterpriseRPC}}
	api.Agents = peer.AgentRPC
	api.DeploymentCatalog = map[string]deployment.Release{one.ID: one, two.ID: two}
	public.StartTLS()
	t.Cleanup(public.Close)
	client := &http.Client{Timeout: 25 * time.Second, Transport: &http.Transport{TLSClientConfig: deploymentStackTLS(t, platformCert)}}
	t.Cleanup(client.CloseIdleConnections)
	until := time.Now().Add(25 * time.Second)
	for {
		nonce, _ := tenancy.Secret()
		report, e := peer.Inspect(t.Context(), c.ServerID, nonce)
		if e == nil && report.Valid(nonce, c.ServerID, tenant, c.PublicURL()) && report.Runtime == "linux/amd64" && len(report.Capabilities) == 3 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("real Linux agent did not become available")
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Log("real Linux agent mTLS identity and execution capability verified")
	foreignAgent, e := tenancy.NewRPC(fmt.Sprintf("https://127.0.0.1:%d", agentPort), deploymentStackTLS(t, c.ControlTLS), deployment.AgentIdentity(c.ServerID))
	if e != nil {
		t.Fatal("foreign-agent probe invalid")
	}
	nonce, _ := tenancy.Secret()
	if e = foreignAgent.Call(t.Context(), "/internal/agent/inspect", map[string]string{"nonce": nonce}, nil); e == nil {
		t.Fatal("enterprise principal received host-agent authority")
	}
	hash, e := bcrypt.GenerateFromPassword([]byte("Synthetic-Platform-Operator!"), 12)
	if e != nil || s.BootstrapAdmin(t.Context(), "operator", string(hash)) != nil {
		t.Fatal("isolated operator bootstrap failed")
	}
	httpCall := func(method, path, token string, body any, status int) map[string]any {
		t.Helper()
		return deploymentStackHTTP(t, client, method, public.URL+path, token, body, status)
	}
	login := httpCall("POST", "/platform/admin/auth/login", "", map[string]string{"username": "operator", "password": "Synthetic-Platform-Operator!"}, 200)
	token := login["accessToken"].(string)
	httpCall("POST", "/platform/admin/tenants", token, map[string]any{"id": tenant, "displayName": "Disposable enterprise", "httpBaseURL": c.PublicURL(), "reason": "isolated deployment rehearsal", "confirmed": true}, 201)
	httpCall("POST", "/platform/admin/servers/operations", token, ServerOperation{RequestID: "bind-server", Action: "register", ServerID: c.ServerID, TenantID: tenant, DisplayName: "Docker-local test host", ExpectedConfigVersion: 1, Reason: "isolated deployment rehearsal", Confirmed: true}, 200)
	worker := DeploymentWorker{Store: s, Agent: peer, Catalog: api.DeploymentCatalog, Readiness: api.Peers.CheckReadiness}
	// A real healthy report with a lost ACK still must not complete the job or
	// permit activation. Only the transport result is fault-injected.
	loseReadyACK := true
	worker.Readiness = func(ctx context.Context, id, nonce string) (tenancy.Readiness, error) {
		r, err := api.Peers.CheckReadiness(ctx, id, nonce)
		if err == nil && loseReadyACK {
			loseReadyACK = false
			return tenancy.Readiness{}, errors.New("injected readiness ACK loss")
		}
		return r, err
	}
	activationBlocked := false
	in := DeploymentRequest{RequestID: "first-deploy", TenantID: tenant, ServerID: c.ServerID, ReleaseID: one.ID, ReleaseDigest: one.Digest(), Action: "deploy", ExpectedRevision: 1, ExpectedConfigVersion: 1, ExpectedAccessVersion: 1, Reason: "isolated deployment rehearsal", Confirmed: true}
	request := func(input DeploymentRequest) DeploymentJob {
		t.Helper()
		var j DeploymentJob
		deploymentStackDecode(t, httpCall("POST", "/platform/admin/deployments", token, input, 200), &j)
		if j.ID == "" {
			t.Fatal("deployment job missing")
		}
		return j
	}
	wait := func(j DeploymentJob) DeploymentJob {
		t.Helper()
		deadline := time.Now().Add(4 * time.Minute)
		previous := ""
		for {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			_, e := worker.Once(ctx)
			cancel()
			if e != nil {
				t.Fatal("deployment worker failed", e)
			}
			j = deployJob(t, s, j.ID)
			phase := j.State + "/" + j.Phase + "/" + j.ErrorCode
			if phase != previous {
				t.Log("deployment progress", phase)
				previous = phase
			}
			if j.Phase == "verifying_business" && j.ErrorCode == "DEPLOYMENT_BUSINESS_UNCONFIRMED" && !activationBlocked {
				httpCall("POST", "/platform/admin/tenants/"+tenant+"/activate", token, map[string]any{"expectedConfigVersion": 1, "reason": "must remain closed while unconfirmed", "confirmed": true}, 409)
				activationBlocked = true
			}
			if j.State == "completed" {
				return j
			}
			if j.State == "unconfirmed" || time.Now().After(deadline) {
				t.Fatal("real deployment requires investigation", phase)
			}
			time.Sleep(time.Second)
		}
	}
	j := request(in)
	peer.lose = true
	if _, e = worker.Once(t.Context()); e != nil {
		t.Fatal("lost-ACK dispatch failed", e)
	}
	if observed := deployJob(t, s, j.ID); observed.State != "pending" || observed.ErrorCode != "DEPLOYMENT_CONTROL_UNCONFIRMED" {
		t.Fatal("lost ACK falsely completed")
	}
	if replay := request(in); replay.ID != j.ID {
		t.Fatal("duplicate deployment job after lost ACK")
	}
	j = wait(j)
	if j.Generation != 1 || j.AgentAttempts != 1 || !activationBlocked {
		t.Fatal("lost ACK reapplied original deployment")
	}
	var state string
	if e = s.pool.QueryRow(t.Context(), `SELECT status FROM platform_tenants WHERE id=$1`, tenant).Scan(&state); e != nil || state != "provisioning" {
		t.Fatal("deployment autoactivated tenant")
	}
	httpCall("POST", "/platform/admin/tenants/"+tenant+"/activate", token, map[string]any{"expectedConfigVersion": 1, "reason": "verified full local stack", "confirmed": true}, 200)
	httpCall("POST", "/platform/admin/tenants/"+tenant+"/codes", token, map[string]any{"reason": "local activation verified", "confirmed": true}, 201)
	t.Log("platform durable deployment, lost-ACK replay and explicit activation verified")

	// Real enterprise-controlled onboarding; no fake/fixed public OTP provider.
	enterpriseClient, e := tenancy.NewRPC(control.URL, deploymentStackTLS(t, c.ControlTLS), tenancy.PlatformIdentity)
	if e != nil {
		t.Fatal("enterprise registration peer invalid")
	}
	var registered TenantAdminResult
	create := TenantAdminCreate{RequestID: "account-one", Actor: "fixture-admin", Phone: "19900000993", Name: "Deployment customer", Password: "Synthetic-Customer-Password!", Gender: "unspecified", Reason: "isolated onboarding verification", Confirmed: true}
	if e = enterpriseClient.Call(t.Context(), "/internal/tenancy/admin/accounts", create, &registered); e != nil || registered.Item == nil {
		t.Fatal("platform onboarding unavailable", e)
	}
	accountWorker := Worker{Store: s, Enterprise: api.Peers}
	for range 4 {
		if _, e = accountWorker.Once(t.Context()); e != nil {
			t.Fatal("registration worker failed", e)
		}
	}
	jobs, e := s.TenantAccountJobs(t.Context(), tenant, registered.Item.JobID)
	if e != nil || len(jobs) != 1 || jobs[0].Status != "completed" {
		t.Fatal("registration not completed")
	}
	userID := jobs[0].LocalUserID
	business := func(method, path, token string, body any, status int) map[string]any {
		t.Helper()
		return deploymentStackHTTP(t, client, method, c.PublicURL()+path, token, body, status)
	}
	userLogin := func() string {
		t.Helper()
		login := httpCall("POST", "/v2/auth/password-login", "", map[string]string{"phone": create.Phone, "password": create.Password}, 200)
		var result LoginResult
		deploymentStackDecode(t, login, &result)
		if result.TenantContext.TenantID != tenant || result.TenantContext.HTTPBaseURL != c.PublicURL() {
			t.Fatal("wrong business routing")
		}
		session := business("POST", "/v2/auth/tenant-session", "", map[string]string{"sessionTicket": result.SessionTicket}, 200)
		if session["user"].(map[string]any)["id"] != userID || session["imSession"] == nil {
			t.Fatal("business or IM session missing")
		}
		business("POST", "/v2/auth/tenant-session", "", map[string]string{"sessionTicket": result.SessionTicket}, 401)
		access := session["accessToken"].(string)
		business("GET", "/v2/users/me", access, nil, 200)
		return access
	}
	oldToken := userLogin()
	t.Log("new enterprise account, platform login, real ticket consumption and direct business access verified")

	// Pause first, then upgrade, with real enterprise revocation acknowledgements.
	changeRealm := func(id string, enabled bool, expected int64) {
		t.Helper()
		httpCall("POST", "/platform/admin/tenants/"+tenant+"/access", token, map[string]any{"requestId": id, "enabled": enabled, "expectedAccessVersion": expected, "reason": "isolated maintenance rehearsal", "confirmed": true}, 202)
		rw := RealmWorker{Store: s, Enterprise: api.Peers}
		deadline := time.Now().Add(45 * time.Second)
		for {
			if _, e := rw.Once(t.Context()); e != nil {
				t.Fatal("realm worker failed", e)
			}
			var status string
			if e := s.pool.QueryRow(t.Context(), `SELECT state FROM platform_realm_jobs WHERE tenant_id=$1 AND request_id=$2`, tenant, id).Scan(&status); e != nil {
				t.Fatal("realm receipt unavailable")
			}
			if status == "completed" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("realm revocation unconfirmed")
			}
			time.Sleep(time.Second)
		}
	}
	changeRealm("pause-upgrade", false, 1)
	in.RequestID, in.ReleaseID, in.ReleaseDigest, in.ExpectedReleaseID, in.ExpectedGeneration, in.ExpectedAccessVersion = "upgrade-stack", two.ID, two.Digest(), one.ID, 1, 2
	// Restart the actual agent process; its journal must restore generation 1.
	if _, e = docker.command(t.Context(), nil, "restart", "--time", "5", docker.agent); e != nil {
		t.Fatal("fixture agent restart failed")
	}
	time.Sleep(time.Second)
	upgrade := wait(request(in))
	if upgrade.Generation != 2 || upgrade.AgentAttempts != 1 {
		t.Fatal("upgrade generation incorrect")
	}
	if e = s.pool.QueryRow(t.Context(), `SELECT status FROM platform_tenants WHERE id=$1`, tenant).Scan(&state); e != nil || state != "suspended" {
		t.Fatal("upgrade resumed tenant automatically")
	}
	in.RequestID, in.ReleaseID, in.ReleaseDigest, in.ExpectedReleaseID, in.ExpectedGeneration, in.Action = "rollback-stack", one.ID, one.Digest(), two.ID, 2, "rollback"
	rollback := wait(request(in))
	if rollback.Generation != 3 {
		t.Fatal("rollback generation incorrect")
	}
	// Platform -> mTLS agent -> actual cold archive -> original service restore.
	// Lose the first ACK, keep the platform running, and require business gating
	// to remain suspended until the independently polled receipt is verified.
	backupInput := BackupRequest{RequestID: "backup-stack", TenantID: tenant, ServerID: c.ServerID, ReleaseID: one.ID, ReleaseDigest: one.Digest(), ExpectedRevision: 1, ExpectedConfigVersion: 1, ExpectedAccessVersion: 2, ExpectedGeneration: 3, Reason: "isolated encrypted backup rehearsal", Confirmed: true}
	var backupJobResult BackupJob
	deploymentStackDecode(t, httpCall("POST", "/platform/admin/backups", token, backupInput, 200), &backupJobResult)
	peer.loseBackup = true
	backupWorker := BackupWorker{Store: s, Agent: peer, Catalog: api.DeploymentCatalog, Readiness: api.Peers.CheckReadiness}
	backupDeadline := time.Now().Add(6 * time.Minute)
	backupPhase := ""
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		_, err := backupWorker.Once(ctx)
		cancel()
		if err != nil {
			t.Fatal("backup platform worker failed", err)
		}
		backupJobResult = backupJob(t, s, backupJobResult.ID)
		current := backupJobResult.State + "/" + backupJobResult.Phase + "/" + backupJobResult.ErrorCode
		if current != backupPhase {
			t.Log("backup progress", current)
			backupPhase = current
		}
		if backupJobResult.State == "completed" {
			break
		}
		if backupJobResult.State != "pending" || time.Now().After(backupDeadline) {
			t.Fatal("real backup needs investigation", current)
		}
		httpCall("POST", "/platform/admin/tenants/"+tenant+"/access", token, map[string]any{"requestId": "resume-during-backup", "enabled": true, "expectedAccessVersion": 2, "reason": "must stay closed during backup", "confirmed": true}, 409)
		time.Sleep(time.Second)
	}
	if !archiveProofValid(backupJobResult.Receipt.Archive) || backupJobResult.Receipt.Attempt != 1 || peer.loseBackup {
		t.Fatal("archive proof or ACK recovery invalid")
	}
	var replay BackupJob
	deploymentStackDecode(t, httpCall("POST", "/platform/admin/backups", token, backupInput, 200), &replay)
	if replay.ID != backupJobResult.ID || replay.State != "completed" {
		t.Fatal("backup replay duplicated capture")
	}
	if e = s.pool.QueryRow(t.Context(), `SELECT status FROM platform_tenants WHERE id=$1`, tenant).Scan(&state); e != nil || state != "suspended" {
		t.Fatal("backup resumed tenant")
	}
	t.Log("actual encrypted archive, platform lost-ACK recovery, service restoration and suspension exclusion verified")
	changeRealm("resume-after-rollback", true, 2)
	preDailyToken := userLogin()
	business("GET", "/v2/users/me", oldToken, nil, 401)
	// Exercise the actual daily dispatcher and its child workers, not a direct
	// script that impersonates their pause/capture/resume side effects.
	nowUTC := time.Now().UTC().Add(-time.Minute)
	schedule := BackupScheduleInput{RequestID: "daily-stack", Enabled: true, StartMinuteUTC: nowUTC.Hour()*60 + nowUTC.Minute(), WindowMinutes: 60, Reason: "isolated daily maintenance rehearsal", Confirmed: true}
	httpCall("PUT", "/platform/admin/backup-schedules/"+tenant, token, schedule, 200)
	if n, err := s.ScheduleBackups(t.Context()); err != nil || n != 1 {
		t.Fatal("daily dispatcher", n, err)
	}
	maintenance := MaintenanceWorker{Store: s, Agent: peer, Catalog: api.DeploymentCatalog, Readiness: api.Peers.CheckReadiness}
	realm := RealmWorker{Store: s, Enterprise: api.Peers}
	dailyDeadline, previous := time.Now().Add(7*time.Minute), ""
	var dailyID, dailyBackup, dailyState, dailyPhase, dailyError string
	for {
		for _, step := range []func(context.Context) (bool, error){maintenance.Once, realm.Once, backupWorker.Once} {
			call, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			_, err := step(call)
			cancel()
			if err != nil {
				t.Fatal("daily maintenance worker", err)
			}
		}
		if err := s.pool.QueryRow(t.Context(), `SELECT id,state,phase,error_code,backup_id FROM platform_maintenance_runs WHERE tenant_id=$1`, tenant).Scan(&dailyID, &dailyState, &dailyPhase, &dailyError, &dailyBackup); err != nil {
			t.Fatal("daily run unavailable", err)
		}
		progress := dailyState + "/" + dailyPhase + "/" + dailyError
		if progress != previous {
			t.Log("daily progress", progress)
			previous = progress
		}
		if dailyState == "completed" {
			break
		}
		if dailyState != "pending" || time.Now().After(dailyDeadline) {
			t.Fatal("daily maintenance not confirmed", progress)
		}
		time.Sleep(time.Second)
	}
	proof := backupJob(t, s, dailyBackup)
	if dailyID == "" || proof.State != "completed" || !archiveProofValid(proof.Receipt.Archive) || proof.Operation.Binding.AccessVersion != 4 {
		t.Fatal("daily archive not verified")
	}
	if n, err := s.ScheduleBackups(t.Context()); err != nil || n != 0 {
		t.Fatal("duplicate UTC slot", n, err)
	}
	if err := s.pool.QueryRow(t.Context(), `SELECT status FROM platform_tenants WHERE id=$1 AND access_version=5`, tenant).Scan(&state); err != nil || state != "active" {
		t.Fatal("daily restoration not confirmed")
	}
	userLogin()
	business("GET", "/v2/users/me", preDailyToken, nil, 401)
	t.Log("daily UTC dispatcher -> real revocation -> encrypted archive -> verified access restore; no duplicate slot; old session rejected")
	if before != docker.defaultSnapshot(t) {
		t.Fatal("persistent default enterprise containers changed")
	}
	var count int
	if e = s.pool.QueryRow(t.Context(), `SELECT count(*) FROM platform_deployment_jobs WHERE tenant_id=$1`, tenant).Scan(&count); e != nil || count != 3 {
		t.Fatal("duplicate deployment history")
	}
	t.Log("real maintenance, agent restart, upgrade, rollback, preserved identity and stale-token rejection verified; default stack unchanged")
}
