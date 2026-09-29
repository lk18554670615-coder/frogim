package platform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

type deploymentRunner struct {
	applies int
	fail    bool
}

func (r *deploymentRunner) Apply(context.Context, deployment.Release, deployment.Operation) error {
	r.applies++
	if r.fail {
		return errors.New("private deployment credential")
	}
	return nil
}
func (r *deploymentRunner) Verify(context.Context, deployment.Release, deployment.Operation) error {
	return nil
}

type deploymentPeer struct {
	x            *deployment.Executor
	lost         bool
	afterInspect func()
	activeBackup string
}

func (p *deploymentPeer) Inspect(ctx context.Context, id, nonce string) (deployment.Inspection, error) {
	r, e := serverFixture("a", "a")(ctx, id, nonce)
	r.Capabilities = []string{"inspect", "deploy"}
	if p.afterInspect != nil {
		p.afterInspect()
	}
	return r, e
}
func (p *deploymentPeer) Deployment(_ context.Context, _ string, action string, in deployment.ControlRequest) (deployment.ControlReport, error) {
	var r deployment.Receipt
	var e error
	if action == "submit" {
		r, e = p.x.Submit(in.Operation)
	}
	if action == "retry" {
		r, e = p.x.Retry(in.Operation.ID, in.ExpectedAttempts)
	}
	if e != nil {
		return deployment.ControlReport{}, e
	}
	s, receipt, e := p.x.Status(in.Operation.ID)
	s.ActiveBackupID = p.activeBackup
	if in.Operation.ID != "" {
		r = receipt
	}
	if p.lost && action != "status" {
		p.lost = false
		return deployment.ControlReport{}, errors.New("lost acknowledgement secret")
	}
	return deployment.ControlReport{Nonce: in.Nonce, Status: s, Receipt: r}, e
}
func deployFixture(t *testing.T) (*Store, string, DeploymentRequest, map[string]deployment.Release, *deploymentPeer, *deploymentRunner, DeploymentWorker) {
	t.Helper()
	s := isolatedPlatform(t)
	token := releaseTestAdmin(t, s)
	if _, e := s.ManageServer(t.Context(), "release-operator", token, serverInput(), serverFixture("a", "a")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RequestRealm(t.Context(), "a", "pause-deploy", "release-operator", "isolated maintenance", 1, false, true); e != nil {
		t.Fatal(e)
	}
	if _, e := (RealmWorker{Store: s, Enterprise: &realmPeer{}}).Once(t.Context()); e != nil {
		t.Fatal(e)
	}
	r := deployment.Release{ID: "release-one", Sequence: 1, Runtime: "linux/amd64", ComposeSHA256: strings.Repeat("a", 64), SchemaVersion: 79, TenantID: "a", ServerID: "server-a"}
	catalog := map[string]deployment.Release{r.ID: r}
	r2 := deployment.Release{ID: "release-two", Sequence: 2, Runtime: "linux/amd64", ComposeSHA256: strings.Repeat("b", 64), SchemaVersion: 79, RollbackTo: []string{r.ID}, TenantID: "a", ServerID: "server-a"}
	catalog[r2.ID] = r2
	runner := &deploymentRunner{}
	x, e := deployment.OpenExecutor(t.TempDir(), "server-a", "a", strings.Repeat("a", 64), "linux/amd64", catalog, runner)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(x.Close)
	peer := &deploymentPeer{x: x}
	in := DeploymentRequest{RequestID: "deploy-first", TenantID: "a", ServerID: "server-a", ReleaseID: r.ID, ReleaseDigest: r.Digest(), Action: "deploy", ExpectedRevision: 1, ExpectedConfigVersion: 1, ExpectedAccessVersion: 2, Reason: "local test deployment", Confirmed: true}
	w := DeploymentWorker{Store: s, Agent: peer, Catalog: catalog, Readiness: deploymentReady}
	return s, token, in, catalog, peer, runner, w
}
func deploymentReady(_ context.Context, id, nonce string) (tenancy.Readiness, error) {
	return tenancy.Readiness{Nonce: nonce, TenantID: id, HTTPBaseURL: "https://" + id + ".example", SchemaVersion: 79, Checks: map[string]bool{"databaseBinding": true, "databaseAndCache": true, "im": true, "media": true, "calls": true}, Realm: &tenancy.RealmSnapshot{Version: 2, Enabled: false, SuspensionConfirmed: true}}, nil
}
func deployRequest(t *testing.T, s *Store, token string, in DeploymentRequest, c map[string]deployment.Release, p *deploymentPeer) DeploymentJob {
	t.Helper()
	j, e := s.RequestDeployment(t.Context(), "release-operator", token, in, c, p)
	if e != nil {
		t.Fatal("request", e)
	}
	return j
}
func deployStep(t *testing.T, w DeploymentWorker) {
	t.Helper()
	if _, e := w.Store.pool.Exec(t.Context(), `UPDATE platform_deployment_jobs SET retry_at=now()`); e != nil {
		t.Fatal(e)
	}
	if worked, e := w.Once(t.Context()); e != nil || !worked {
		t.Fatal("worker", worked, e)
	}
}
func deployJob(t *testing.T, s *Store, id string) DeploymentJob {
	t.Helper()
	j, e := scanDeployment(s.pool.QueryRow(t.Context(), `SELECT `+deploymentColumns+` FROM platform_deployment_jobs WHERE id=$1`, id))
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func TestPlatformPostgresDeploymentLifecycle(t *testing.T) {
	s, token, in, c, p, runner, w := deployFixture(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, e := s.RequestDeployment(ctx, "release-operator", token, in, c, p); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	j := deployRequest(t, s, token, in, c, p)
	if _, e := s.RequestDeployment(ctx, "release-operator", token, in, nil, nil); e != nil {
		t.Fatal("offline replay", e)
	}
	changed := in
	changed.Reason = "another reason"
	if _, e := s.RequestDeployment(ctx, "release-operator", token, changed, c, p); !errors.Is(e, ErrRequestChanged) {
		t.Fatal(e)
	}
	changed = in
	changed.RequestID = "other"
	if _, e := s.RequestDeployment(ctx, "release-operator", token, changed, c, p); !errors.Is(e, ErrDeploymentMaintenance) {
		t.Fatal("second job", e)
	}
	if _, e := s.RequestRealm(ctx, "a", "resume", "release-operator", "resume test", 2, true, true); !errors.Is(e, ErrDeploymentMaintenance) {
		t.Fatal("resumed in deployment", e)
	}
	recheck := serverInput()
	recheck.Action = "inspect"
	recheck.RequestID = "reinspect"
	recheck.ExpectedRevision = 1
	if _, e := s.ManageServer(ctx, "release-operator", token, recheck, p); !errors.Is(e, ErrDeploymentMaintenance) {
		t.Fatal("server changed mid-job", e)
	}
	p.lost = true
	deployStep(t, w)
	if j = deployJob(t, s, j.ID); j.State != "pending" || j.ErrorCode != "DEPLOYMENT_CONTROL_UNCONFIRMED" {
		t.Fatal(j)
	}
	if _, e := p.x.Once(ctx); e != nil {
		t.Fatal(e)
	}
	w.Readiness = func(ctx context.Context, id, nonce string) (tenancy.Readiness, error) {
		r, e := deploymentReady(ctx, id, nonce)
		r.SchemaVersion++
		return r, e
	}
	deployStep(t, w)
	if j = deployJob(t, s, j.ID); j.State != "pending" || j.Phase != "verifying_business" {
		t.Fatal(j)
	}
	w.Readiness = func(ctx context.Context, id, nonce string) (tenancy.Readiness, error) {
		r, e := deploymentReady(ctx, id, nonce)
		r.Realm.Enabled = true
		return r, e
	}
	deployStep(t, w)
	if j = deployJob(t, s, j.ID); j.State != "pending" {
		t.Fatal("enabled realm accepted", j)
	}
	w.Readiness = deploymentReady
	deployStep(t, w)
	j = deployJob(t, s, j.ID)
	if j.State != "completed" || j.Generation != 1 || runner.applies != 1 {
		t.Fatal(j, runner.applies)
	}
	var state string
	var count int
	if e := s.pool.QueryRow(ctx, `SELECT status FROM platform_tenants WHERE id='a'`).Scan(&state); e != nil || state != "suspended" {
		t.Fatal("autoactivation", state, e)
	}
	if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_audits WHERE action LIKE 'deployment.%'`).Scan(&count); e != nil || count != 2 {
		t.Fatal("duplicate audit", count, e)
	}
	if worked, e := w.Once(ctx); e != nil || worked {
		t.Fatal("completed reexecuted", e)
	}
	if _, e := s.RequestRealm(ctx, "a", "resume", "release-operator", "resume test", 2, true, true); e != nil {
		t.Fatal("final gate", e)
	}
	if e := s.Migrate(ctx); e != nil {
		t.Fatal("idempotent migration", e)
	}
	var version int
	if e := s.pool.QueryRow(ctx, `SELECT max(version) FROM platform_schema_migrations`).Scan(&version); e != nil || version != SchemaVersion {
		t.Fatal(version, e)
	}
}
func TestPlatformPostgresDeploymentExplicitRetry(t *testing.T) {
	s, token, in, c, p, runner, w := deployFixture(t)
	ctx := t.Context()
	runner.fail = true
	j := deployRequest(t, s, token, in, c, p)
	deployStep(t, w)
	if _, e := p.x.Once(ctx); !errors.Is(e, deployment.ErrUnconfirmed) {
		t.Fatal(e)
	}
	deployStep(t, w)
	j = deployJob(t, s, j.ID)
	if j.State != "unconfirmed" || j.AgentAttempts != 1 {
		t.Fatal(j)
	}
	if worked, e := w.Once(ctx); e != nil || worked || runner.applies != 1 {
		t.Fatal("automatic retry", e)
	}
	r := DeploymentRetry{RequestID: "retry-one", ExpectedAttempts: 1, Reason: "corrected prerequisite", Confirmed: true}
	if _, e := s.RetryDeployment(ctx, "release-operator", token, j.ID, r); e != nil {
		t.Fatal(e)
	}
	p.lost = true
	deployStep(t, w) // retry reached agent; its acknowledgement was lost
	p.lost = false
	deployStep(t, w)
	runner.fail = false
	if _, e := p.x.Once(ctx); e != nil {
		t.Fatal(e)
	}
	deployStep(t, w)
	j = deployJob(t, s, j.ID)
	if j.State != "completed" || j.AgentAttempts != 2 || runner.applies != 2 {
		t.Fatal(j, runner.applies)
	}
	if _, e := s.RetryDeployment(ctx, "release-operator", token, j.ID, r); e != nil {
		t.Fatal("lost HTTP retry response", e)
	}
	r.RequestID = "stale-retry"
	if _, e := s.RetryDeployment(ctx, "release-operator", token, j.ID, r); !errors.Is(e, ErrDeploymentChanged) {
		t.Fatal(e)
	}
	if _, e := s.RequestRealm(ctx, "a", "resume", "release-operator", "after retry", 2, true, true); e != nil {
		t.Fatal(e)
	}
}
func TestPlatformPostgresDeploymentRequestFences(t *testing.T) {
	for _, name := range []string{"active", "active_backup", "revoked_operator", "server_revision", "audit_failure", "legacy_import", "catalog_changed", "wrong_generation", "foreign_tenant_bundle", "foreign_server_bundle", "unbound_bundle"} {
		t.Run(name, func(t *testing.T) {
			s, token, in, c, p, _, _ := deployFixture(t)
			ctx := t.Context()
			switch name {
			case "active_backup":
				p.activeBackup = "backup-fixture"
			case "foreign_tenant_bundle", "foreign_server_bundle", "unbound_bundle":
				r := c[in.ReleaseID]
				if name == "foreign_tenant_bundle" {
					r.TenantID = "b"
				} else if name == "foreign_server_bundle" {
					r.ServerID = "server-b"
				} else {
					r.TenantID = ""
					r.ServerID = ""
				}
				c[in.ReleaseID] = r
				in.ReleaseDigest = r.Digest()
				p.afterInspect = func() { t.Error("foreign release reached the agent") }
			case "active":
				_, _ = s.pool.Exec(ctx, `UPDATE platform_tenants SET status='active' WHERE id='a'`)
			case "revoked_operator":
				p.afterInspect = func() {
					_, _ = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET enabled=false WHERE id='release-operator'`)
				}
			case "server_revision":
				p.afterInspect = func() { _, _ = s.pool.Exec(ctx, `UPDATE platform_servers SET revision=2 WHERE id='server-a'`) }
			case "audit_failure":
				_, e := s.pool.Exec(ctx, `CREATE FUNCTION reject_deployment_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='deployment.requested' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_deploy BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION reject_deployment_audit()`)
				if e != nil {
					t.Fatal(e)
				}
			case "legacy_import":
				_, e := s.pool.Exec(ctx, `INSERT INTO platform_legacy_import_batches(id,tenant_id,actor_id,reason,fingerprint,allow_passwordless,realm_version,total,state) VALUES('fixture','a','test','test',$1,false,2,1,'running')`, strings.Repeat("a", 64))
				if e != nil {
					t.Fatal(e)
				}
			case "catalog_changed":
				r := c[in.ReleaseID]
				r.Sequence++
				c[in.ReleaseID] = r
			case "wrong_generation":
				in.ExpectedGeneration = 1
				in.ExpectedReleaseID = in.ReleaseID
			}
			if _, e := s.RequestDeployment(ctx, "release-operator", token, in, c, p); e == nil {
				t.Fatal("unsafe request accepted")
			}
			var n int
			if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_deployment_jobs`).Scan(&n); e != nil || n != 0 {
				t.Fatal("partial transaction", n, e)
			}
		})
	}
}
func TestPlatformPostgresDeploymentCompletionFences(t *testing.T) {
	for _, name := range []string{"lost_lease", "audit_failure", "missing_realm", "dependency_failure", "journal_reset", "active_backup"} {
		t.Run(name, func(t *testing.T) {
			s, token, in, c, p, runner, w := deployFixture(t)
			ctx := t.Context()
			j := deployRequest(t, s, token, in, c, p)
			deployStep(t, w)
			if _, e := p.x.Once(ctx); e != nil {
				t.Fatal(e)
			}
			if name == "active_backup" {
				p.activeBackup = "backup-fixture"
			}
			if name == "audit_failure" {
				_, e := s.pool.Exec(ctx, `CREATE FUNCTION reject_deployment_completion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='deployment.completed' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_complete BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION reject_deployment_completion()`)
				if e != nil {
					t.Fatal(e)
				}
			}
			w.Readiness = func(ctx context.Context, id, nonce string) (tenancy.Readiness, error) {
				r, e := deploymentReady(ctx, id, nonce)
				switch name {
				case "missing_realm":
					r.Realm = nil
				case "dependency_failure":
					r.Checks["media"] = false
				case "lost_lease":
					_, e = s.pool.Exec(ctx, `UPDATE platform_deployment_jobs SET lease_id='new-owner' WHERE id=$1`, j.ID)
				}
				return r, e
			}
			if name == "journal_reset" {
				p.x.Close()
				x, e := deployment.OpenExecutor(t.TempDir(), "server-a", "a", strings.Repeat("a", 64), "linux/amd64", c, runner)
				if e != nil {
					t.Fatal(e)
				}
				p.x = x
				t.Cleanup(x.Close)
			}
			_, _ = s.pool.Exec(ctx, `UPDATE platform_deployment_jobs SET retry_at=now()`)
			_, _ = w.Once(ctx)
			j = deployJob(t, s, j.ID)
			if j.State == "completed" {
				t.Fatal("unverified completion", name)
			}
			if _, e := s.RequestRealm(ctx, "a", "resume", "release-operator", "unsafe resume", 2, true, true); e == nil {
				t.Fatal("unsafe resume")
			}
		})
	}
}
func TestPlatformPostgresDeploymentBackoffCountsOnlyConsecutiveFailures(t *testing.T) {
	s, token, in, catalog, peer, _, worker := deployFixture(t)
	job := deployRequest(t, s, token, in, catalog, peer)
	for range 12 {
		deployStep(t, worker)
	} // genuine, successful "pending" polls
	assertDelay := func(failures, seconds int) {
		t.Helper()
		var gotFailures int
		var delay float64
		if err := s.pool.QueryRow(t.Context(), `SELECT attempts,extract(epoch FROM (retry_at-updated_at))::double precision FROM platform_deployment_jobs WHERE id=$1`, job.ID).Scan(&gotFailures, &delay); err != nil {
			t.Fatal(err)
		}
		if gotFailures != failures || delay < float64(seconds)-0.1 || delay > float64(seconds)+0.2 {
			t.Fatal("incorrect durable backoff", gotFailures, delay)
		}
	}
	assertDelay(0, 2)
	if _, err := peer.x.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	worker.Readiness = func(context.Context, string, string) (tenancy.Readiness, error) {
		return tenancy.Readiness{}, ErrUnavailable
	}
	deployStep(t, worker)
	assertDelay(1, 2) // not 256 seconds after twelve healthy polls
	// A new worker object (no in-memory retry state) retains consecutive failure
	// backoff. This counts control attempts, not the agent's apply attempts.
	restarted := DeploymentWorker{Store: s, Agent: peer, Catalog: catalog, Readiness: worker.Readiness}
	deployStep(t, restarted)
	assertDelay(2, 4)
	for range 8 {
		deployStep(t, restarted)
	}
	assertDelay(10, 300)
	restarted.Readiness = deploymentReady
	deployStep(t, restarted)
	assertDelay(0, 2)
	if done := deployJob(t, s, job.ID); done.State != "completed" || done.AgentAttempts != 1 {
		t.Fatal("control retry repeated the deployment")
	}
}

func TestPlatformPostgresDeploymentHTTPAndProvisioning(t *testing.T) {
	s, token, in, c, p, _, w := deployFixture(t)
	ctx := t.Context()
	_, e := s.pool.Exec(ctx, `DELETE FROM platform_realm_jobs WHERE tenant_id='a'; UPDATE platform_tenants SET status='provisioning',access_version=1 WHERE id='a'`)
	if e != nil {
		t.Fatal(e)
	}
	in.ExpectedAccessVersion = 1
	j := deployRequest(t, s, token, in, c, p)
	if e := s.ActivateTenant(ctx, "a", "release-operator", "activate test", true, 1, deploymentReady); !errors.Is(e, ErrDeploymentMaintenance) {
		t.Fatal("premature activation", e)
	}
	api := &API{Store: s, Limiter: testLimit{}, DeploymentCatalog: c}
	status, result := adminTestCall(t, api, "GET", "/platform/admin/deployments?q="+j.ID, token, "")
	if status != 200 || result["total"] != float64(1) {
		t.Fatal("list", status, result)
	}
	if status, _ = adminTestCall(t, api, "GET", "/platform/admin/deployment-releases", token, ""); status != 200 {
		t.Fatal(status)
	}
	if _, e = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET role='reader' WHERE id='release-operator'`); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(in)
	if status, _ = adminTestCall(t, api, "POST", "/platform/admin/deployments", token, string(b)); status != 401 {
		t.Fatal("viewer write", status)
	}
	_, _ = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET role='operator' WHERE id='release-operator'`)
	deployStep(t, w)
	if _, e = p.x.Once(ctx); e != nil {
		t.Fatal(e)
	}
	w.Readiness = func(ctx context.Context, id, nonce string) (tenancy.Readiness, error) {
		r, e := deploymentReady(ctx, id, nonce)
		r.Realm.Version = 1
		r.Realm.Enabled = true
		r.Realm.SuspensionConfirmed = false
		return r, e
	}
	deployStep(t, w)
	if j = deployJob(t, s, j.ID); j.State != "completed" {
		t.Fatal(j)
	}
	if e = s.ActivateTenant(ctx, "a", "release-operator", "activate test", true, 1, w.Readiness); e != nil {
		t.Fatal(e)
	}
}

func TestPlatformPostgresDeploymentUpgradeAndRollback(t *testing.T) {
	s, token, in, c, p, runner, w := deployFixture(t)
	ctx := t.Context()
	complete := func(in DeploymentRequest) DeploymentJob {
		j := deployRequest(t, s, token, in, c, p)
		deployStep(t, w)
		if _, e := p.x.Once(ctx); e != nil {
			t.Fatal(e)
		}
		deployStep(t, w)
		j = deployJob(t, s, j.ID)
		if j.State != "completed" {
			t.Fatal(j)
		}
		return j
	}
	complete(in)
	in.RequestID = "upgrade"
	in.ReleaseID = "release-two"
	in.ReleaseDigest = c[in.ReleaseID].Digest()
	in.ExpectedGeneration = 1
	in.ExpectedReleaseID = "release-one"
	complete(in)
	in.RequestID = "downgrade"
	in.ReleaseID = "release-one"
	in.ReleaseDigest = c[in.ReleaseID].Digest()
	in.ExpectedGeneration = 2
	in.ExpectedReleaseID = "release-two"
	if _, e := s.RequestDeployment(ctx, "release-operator", token, in, c, p); !errors.Is(e, ErrDeploymentChanged) {
		t.Fatal("silent downgrade", e)
	}
	in.RequestID = "rollback"
	in.Action = "rollback"
	j := complete(in)
	if j.Generation != 3 || runner.applies != 3 {
		t.Fatal(j, runner.applies)
	}
	// Resetting the host's journal cannot reset platform history to generation 0.
	p.x.Close()
	replacement, e := deployment.OpenExecutor(t.TempDir(), "server-a", "a", strings.Repeat("a", 64), "linux/amd64", c, runner)
	if e != nil {
		t.Fatal(e)
	}
	p.x = replacement
	t.Cleanup(replacement.Close)
	in.RequestID = "reset-journal"
	in.Action = "deploy"
	in.ExpectedGeneration = 0
	in.ExpectedReleaseID = ""
	if _, e = s.RequestDeployment(ctx, "release-operator", token, in, c, p); !errors.Is(e, ErrDeploymentChanged) {
		t.Fatal("journal history reset adopted", e)
	}
}
