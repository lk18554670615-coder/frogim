package deployment

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/tenancy"
)

// Runs inside the explicit, random nine-service cold-backup fixture. The
// unknown attempt is injected to model process death before the HTTP 101 ACK;
// the surrounding PostgreSQL, IM, LiveKit, Docker and operator CLI are real.
func coldMediaRepairDrill(t *testing.T, r *ComposeRunner, x *Executor, binding backup.Binding, control *tenancy.RPC) *Executor {
	t.Helper()
	ctx := t.Context()
	binary := os.Getenv("TENANCY_MEDIA_REPAIR_CLI")
	if !filepath.IsAbs(binary) {
		t.Fatal("compiled media repair CLI required by explicit backup fixture")
	}
	request := MediaRepairRequest{RequestID: "repair-media-one", PauseOperationID: "backup-maintenance", Actor: "fixture-operator", Reason: "unknown handshake recovery; literal ' quote and 中文", Confirmed: true}
	items, e := r.containers(ctx)
	if e != nil {
		t.Fatal(e)
	}
	ids := map[string]string{}
	for _, c := range items {
		if !r.owns(c.Labels) {
			t.Fatal("foreign fixture")
		}
		ids[c.Labels["com.docker.compose.service"]] = c.ID
	}
	_, b, err := readBundle(r.BundleRoot, binding.ReleaseID, r.Catalog[binding.ReleaseID].ComposeSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if sharedBundle(b) {
		ids["enterprise-db"], err = r.sharedStore(ctx, "shared-postgres")
		if err != nil {
			t.Fatal(err)
		}
	}
	query := func(sql string) string {
		t.Helper()
		out, e := r.command(ctx, nil, "exec", ids["enterprise-db"], "psql", "-X", "-q", "-U", "enterprise", "-d", sharedDatabase(b), "-At", "-v", "ON_ERROR_STOP=1", "-c", sql)
		if e != nil {
			t.Fatal("private fixture SQL failed")
		}
		return strings.TrimSpace(string(out))
	}
	query(`INSERT INTO im_tenant_media_attempts(id,local_user_id,assignment_version,auth_version) VALUES(repeat('q',43),'local-fixture',1,1)`)
	if _, e = x.RepairColdMedia(ctx, binding, request); e == nil {
		t.Fatal("running enterprise repaired")
	}
	var ack tenancy.RealmAck
	pause := tenancy.RealmOperation{OperationID: request.PauseOperationID, TenantID: r.Tenant, Version: 2}
	if e = control.Call(ctx, "/internal/tenancy/realm", pause, &ack); e == nil && ack.State == "completed" {
		t.Fatal("ambiguous handshake did not block realm drain")
	}
	if query(`SELECT access_enabled::text||':'||access_version::text FROM im_tenant_identity`) != "false:2" {
		t.Fatal("pause gate not committed")
	}
	for _, name := range []string{"enterprise-gateway", "enterprise-api", "enterprise-im", "enterprise-livekit", "enterprise-media-init", "enterprise-minio", "enterprise-redis", "enterprise-plugins"} {
		if sharedBundle(b) && name == "enterprise-redis" {
			continue
		}
		if _, e = r.command(ctx, nil, "stop", "--time", "30", ids[name]); e != nil {
			t.Fatal("disposable service stop failed")
		}
	}
	bad := request
	bad.PauseOperationID = "wrong-pause"
	if _, e = x.RepairColdMedia(ctx, binding, bad); e == nil {
		t.Fatal("wrong pause accepted")
	}
	query(`CREATE FUNCTION reject_media_repair_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='tenant.media.cold_repaired' THEN RAISE EXCEPTION 'fixture reject'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_media_repair BEFORE INSERT ON im_audits FOR EACH ROW EXECUTE FUNCTION reject_media_repair_audit()`)
	if _, e = x.RepairColdMedia(ctx, binding, request); e == nil {
		t.Fatal("audit failure accepted")
	}
	if query(`SELECT count(*) FROM im_tenant_media_attempts`) != "1" {
		t.Fatal("audit failure deleted recovery barrier")
	}
	query(`DROP TRIGGER reject_media_repair ON im_audits; DROP FUNCTION reject_media_repair_audit()`)
	status, _, e := x.Status("")
	if e != nil {
		t.Fatal(e)
	}
	stateDir := x.root.Name()
	config := map[string]any{"expected": binding, "hostFingerprint": status.HostFingerprint, "runtime": "linux/amd64", "stateDirectory": stateDir, "bundleDirectory": r.BundleRoot, "dockerBinary": r.Binary, "dockerEndpoint": r.Endpoint, "catalog": catalogList(r.Catalog), "mediaRepair": request}
	file := filepath.Join(t.TempDir(), "repair.json")
	write := func() {
		t.Helper()
		raw, _ := json.Marshal(config)
		if os.WriteFile(file, raw, 0600) != nil {
			t.Fatal("private CLI fixture write")
		}
	}
	write()
	run := func(wantSuccess bool) MediaRepairResult {
		t.Helper()
		call, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		command := exec.CommandContext(call, binary, "-mode", "repair-media", "-config", file, "-confirmed")
		command.Stderr = io.Discard
		out, e := command.Output()
		if (e == nil) != wantSuccess {
			t.Fatal("cold repair CLI result", e)
		}
		if !wantSuccess {
			return MediaRepairResult{}
		}
		for _, secret := range []string{file, stateDir, request.Reason, "local-fixture"} {
			if bytes.Contains(out, []byte(secret)) {
				t.Fatal("operator output disclosed private input")
			}
		}
		var result MediaRepairResult
		if json.Unmarshal(out, &result) != nil || result.RequestID != request.RequestID || result.ClearedAttempts != 1 || result.AccessEnabled || result.AccessVersion != 2 {
			t.Fatal("wrong repair acknowledgment")
		}
		return result
	}
	run(false) // The agent's existing OS journal lock still owns the target.
	x.Close()
	first := run(true)
	second := run(true)
	if first != second {
		t.Fatal("lost acknowledgment retry changed result")
	}
	bad = request
	bad.Reason = "changed operator intent"
	config["mediaRepair"] = bad
	write()
	run(false)
	if query(`SELECT count(*) FROM im_tenant_media_attempts`) != "0" || query(`SELECT count(*) FROM im_audits WHERE action='tenant.media.cold_repaired'`) != "1" {
		t.Fatal("repair or audit idempotency")
	}
	if query(`SELECT state FROM im_tenant_realm_operations WHERE operation_id='backup-maintenance'`) != "revoking" || query(`SELECT access_enabled::text FROM im_tenant_identity`) != "false" {
		t.Fatal("repair finished revocation or enabled access")
	}
	x, e = OpenExecutor(stateDir, r.Server, r.Tenant, status.HostFingerprint, "linux/amd64", r.Catalog, r)
	if e != nil {
		t.Fatal("journal reopen", e)
	}
	t.Cleanup(x.Close)
	// Test operator resumes only the inspected original IDs; never compose up,
	// no new volumes, no restore of business access, no automatic client retry.
	io := &composeBackupIO{r: r}
	for _, name := range []string{"enterprise-db", "enterprise-plugins", "enterprise-redis", "enterprise-minio", "enterprise-media-init", "enterprise-im", "enterprise-livekit", "enterprise-api", "enterprise-gateway"} {
		if sharedBundle(b) && (name == "enterprise-redis" || name == "enterprise-db") {
			continue
		}
		if _, e = r.command(ctx, nil, "start", ids[name]); e != nil {
			t.Fatal("original fixture service start")
		}
		if e = io.waitHealthy(ctx, ids[name]); e != nil {
			t.Fatal("original fixture readiness", name)
		}
	}
	if e = control.Call(ctx, "/internal/tenancy/realm", pause, &ack); e != nil || ack.State != "completed" {
		t.Fatal("normal revocation could not finish after cold repair", e)
	}
	t.Log("cold media repair: running/wrong pause/active agent rejected; audit rollback preserves barrier; real CLI retries idempotently; original services restored, ordinary realm drain completed without enabling access")
	return x
}
