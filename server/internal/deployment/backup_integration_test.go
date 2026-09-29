package deployment

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/tenancy"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Called only by the explicit disposable nine-service Docker fixture. It never
// selects or stops the persistent default enterprise or its deployment agent.
func coldBackupDrill(t *testing.T, r *ComposeRunner, x *Executor, c EnterpriseConfig, control *tenancy.RPC, prior Release, inspectIdentity, checkReady func()) {
	t.Helper()
	ctx := t.Context()
	var key [32]byte
	if _, e := rand.Read(key[:]); e != nil {
		t.Fatal(e)
	}
	status, _, e := x.Status("")
	if e != nil {
		t.Fatal(e)
	}
	binding := backup.Binding{TenantID: r.Tenant, ServerID: r.Server, ReleaseID: prior.ID, ReleaseDigest: prior.Digest(), Generation: status.Generation, AccessVersion: 2, SchemaVersion: 79}
	path := filepath.Join(t.TempDir(), "encrypted-set")
	if x.ColdBackup(ctx, binding, path, key[:]) == nil {
		t.Fatal("live enterprise backed up")
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("live backup created artifacts")
	}
	// A real object, not a volume marker, must survive MinIO restart/restore.
	transport := &http.Transport{TLSClientConfig: bundleClientTLS(t, c.PublicTLS)}
	defer transport.CloseIdleConnections()
	s3, e := minio.New(fmt.Sprintf("127.0.0.1:%d", c.Ports.Media), &minio.Options{Creds: credentials.NewStaticV4("tenantmedia", c.Secrets.Media, ""), Secure: true, Transport: transport})
	if e != nil {
		t.Fatal("S3 client")
	}
	media := []byte("isolated binary media \x00\xff\n")
	const object = "restore-fixture/image.bin"
	if _, e = s3.PutObject(ctx, "enterprise-media", object, bytes.NewReader(media), int64(len(media)), minio.PutObjectOptions{ContentType: "application/octet-stream"}); e != nil {
		t.Fatal("real MinIO fixture upload failed")
	}
	// Real WuKong stored message: the API remains private, accessed from its
	// own container using a fixed loopback command. No public manager port.
	imCall := func(path string, body any) []byte {
		t.Helper()
		items, e := r.containers(ctx)
		if e != nil {
			t.Fatal(e)
		}
		im := ""
		for _, item := range items {
			if item.Labels["com.docker.compose.service"] == "enterprise-im" {
				im = item.ID
			}
		}
		if im == "" {
			t.Fatal("IM container absent")
		}
		data, _ := json.Marshal(body)
		request, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:5001"+path, bytes.NewReader(data))
		request.Close = true
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("token", c.Secrets.IMManager)
		var wire, out bytes.Buffer
		if request.Write(&wire) != nil {
			t.Fatal("IM request encoding")
		}
		if r.stream(ctx, &wire, &out, "exec", "-i", im, "nc", "-w", "10", "127.0.0.1", "5001") != nil {
			t.Fatal("private IM request failed")
		}
		response, e := http.ReadResponse(bufio.NewReader(&out), request)
		if e != nil {
			t.Fatal("private IM response malformed")
		}
		defer response.Body.Close()
		data, e = io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if e != nil || response.StatusCode != 200 {
			t.Fatal("private IM request rejected", response.StatusCode)
		}
		return data
	}
	imCall("/user/systemuids_add", map[string]any{"uids": []string{"restore-fixture-sender"}})
	payload := []byte(`{"type":1,"content":"restore-fixture-message"}`)
	sent := imCall("/message/send", map[string]any{"header": map[string]int{"no_persist": 0, "red_dot": 0, "sync_once": 0}, "client_msg_no": "restore-fixture-message-id", "from_uid": "restore-fixture-sender", "channel_id": "local-fixture", "channel_type": 1, "payload": payload})
	var sentResult struct {
		Status int
		Data   struct {
			MessageID int64 `json:"message_id"`
		}
	}
	if json.Unmarshal(sent, &sentResult) != nil || sentResult.Status != 200 || sentResult.Data.MessageID == 0 {
		t.Fatal("IM stored message not confirmed")
	}
	checkMessage := func() {
		t.Helper()
		data := imCall("/messages", map[string]any{"login_uid": "local-fixture", "channel_id": "restore-fixture-sender", "channel_type": 1, "client_msg_nos": []string{"restore-fixture-message-id"}})
		var result struct {
			Messages []struct {
				Payload []byte `json:"payload"`
			}
		}
		if json.Unmarshal(data, &result) != nil || len(result.Messages) != 1 || !bytes.Equal(result.Messages[0].Payload, payload) {
			t.Fatal("real stored IM message missing")
		}
	}
	checkMessage()
	x = coldMediaRepairDrill(t, r, x, binding, control)
	var ack tenancy.RealmAck
	if e = control.Call(ctx, "/internal/tenancy/realm", tenancy.RealmOperation{OperationID: "backup-maintenance", TenantID: r.Tenant, Version: 2, Enabled: false}, &ack); e != nil || ack.State != "completed" {
		t.Fatal("test maintenance not confirmed", e)
	}
	durableBackupDrill(t, r, x, binding, key[:])
	checkReady()
	inspectIdentity()
	checkMessage()
	stopWriters := func() {
		t.Helper()
		items, e := r.containers(ctx)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range items {
			if !r.owns(item.Labels) {
				t.Fatal("foreign container")
			}
			if item.Labels["com.docker.compose.service"] != "enterprise-db" {
				if _, e = r.command(ctx, nil, "stop", "--time=30", item.ID); e != nil {
					t.Fatal("fixture writer stop")
				}
			}
		}
	}
	stopWriters()
	if e = x.ColdBackup(ctx, binding, path, key[:]); e != nil {
		items, _ := r.containers(ctx)
		for _, item := range items {
			t.Log("cold state", item.Labels["com.docker.compose.service"], item.Status, item.ExitCode)
		}
		t.Fatal("cold backup", e)
	}
	archive, e := backup.Open(path, binding, key[:])
	if e != nil {
		t.Fatal("complete backup verification", e)
	}
	archive.Close()
	const restoreID = "restored"
	var restored Release
	// Remove ONLY disposable fixture resources bearing the exact ownership and
	// restore labels. Original fixture cleanup is separately registered upstream.
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if restored.ID != "" {
			o := Operation{ID: "restore-cleanup", ServerID: r.Server, TenantID: r.Tenant, ReleaseID: restored.ID, ReleaseDigest: restored.Digest()}
			raw, b, e := r.bundle(restored, o)
			if e != nil || r.resources(cleanup, b) != nil {
				t.Error("staged cleanup ownership failed")
				return
			}
			items, e := r.containers(cleanup)
			if e != nil {
				t.Error("staged cleanup inspect")
				return
			}
			for _, item := range items {
				if !r.owns(item.Labels) {
					t.Error("foreign staged cleanup")
					return
				}
			}
			if _, e = r.compose(cleanup, raw, "down", "--volumes", "--timeout", "2"); e != nil {
				t.Error("staged cleanup failed")
			}
		}
		out, e := r.command(cleanup, nil, "volume", "ls", "--filter", "label=io.frogim.restore="+restoreID, "--format", "{{.Name}}")
		if e != nil {
			t.Error("partial staging cleanup unavailable")
			return
		}
		for _, name := range strings.Fields(string(out)) {
			if !strings.HasPrefix(name, r.Project+"_restore-"+restoreID+"-") {
				continue
			}
			if r.unusedVolume(cleanup, name) != nil {
				t.Error("partial staging volume unsafe")
				continue
			}
			if _, e = r.command(cleanup, nil, "volume", "rm", name); e != nil {
				t.Error("partial staging cleanup failed")
			}
		}
	})
	wrong := binding
	wrong.TenantID = "other"
	if _, e = x.StageColdRestore(ctx, wrong, path, key[:], restoreID, 3); e == nil {
		t.Fatal("wrong enterprise restore accepted")
	}
	badKey := bytes.Repeat([]byte{11}, 32)
	if _, e = x.StageColdRestore(ctx, binding, path, badKey, restoreID, 3); e == nil {
		t.Fatal("wrong key restore accepted")
	}
	if _, e = os.Stat(filepath.Join(r.BundleRoot, restoreID)); !os.IsNotExist(e) {
		t.Fatal("failed preflight created staging release")
	}
	restored, e = x.StageColdRestore(ctx, binding, path, key[:], restoreID, 3)
	if e != nil {
		t.Fatal("staged restore", e)
	}
	// Original data volumes still exist. Staging did not change the journal.
	_, originalBundle, err := readBundle(r.BundleRoot, prior.ID, prior.ComposeSHA256)
	if err != nil {
		t.Fatal(err)
	}
	originalVolumes, err := coldVolumeSources(originalBundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range originalVolumes {
		if _, e = r.command(ctx, nil, "volume", "inspect", "--format", "{{.Name}}", r.Project+"_"+name); e != nil {
			t.Fatal("original volume lost")
		}
	}
	current, _, e := x.Status("")
	if e != nil || current.Generation != status.Generation || current.CurrentReleaseID != prior.ID {
		t.Fatal("staging activated itself")
	}
	if _, e = x.StageColdRestore(ctx, binding, path, key[:], restoreID, 3); e == nil {
		t.Fatal("reused restore overwrote data")
	}
	// Test-only explicit operator catalog review; production must use a platform
	// maintenance/deployment job, which this offline core does not impersonate.
	r.Catalog[restored.ID] = restored
	x.catalog[restored.ID] = restored
	op := Operation{ID: "restore-activate-fixture", ServerID: r.Server, TenantID: r.Tenant, HostFingerprint: status.HostFingerprint, ReleaseID: restored.ID, ReleaseDigest: restored.Digest(), ExpectedGeneration: status.Generation, ExpectedReleaseID: prior.ID, Action: "deploy"}
	if _, e = x.Submit(op); e != nil {
		t.Fatal("restore deploy submit", e)
	}
	if _, e = x.Once(ctx); e != nil {
		t.Fatal("restore deployment", e)
	}
	checkReady()
	inspectIdentity()
	checkMessage()
	storedObject, e := s3.GetObject(ctx, "enterprise-media", object, minio.GetObjectOptions{})
	if e != nil {
		t.Fatal("restored object")
	}
	actual, e := io.ReadAll(storedObject)
	storedObject.Close()
	if e != nil || !bytes.Equal(actual, media) {
		t.Fatal("MinIO object changed")
	}
	nonce, _ := tenancy.Secret()
	var ready tenancy.Readiness
	if control.Call(ctx, "/internal/tenancy/readiness", map[string]string{"nonce": nonce}, &ready) != nil || ready.Realm == nil || ready.Realm.Enabled || ready.Realm.Version != 2 || !ready.Realm.SuspensionConfirmed {
		t.Fatal("restore re-enabled access")
	}
	// Restored physical volume names remain compatible with the next backup.
	stopWriters()
	next := binding
	next.Generation++
	next.ReleaseID = restored.ID
	next.ReleaseDigest = restored.Digest()
	if e = x.ColdBackup(ctx, next, filepath.Join(t.TempDir(), "next-backup"), key[:]); e != nil {
		t.Fatal("backup after restore failed", e)
	}
	t.Log("encrypted backup and NEW-volume restore preserved real PostgreSQL identity, WuKong message and MinIO bytes; originals retained, realm stayed suspended, wrong tenant/key rejected, repeated restore refused, next backup succeeded")
}
