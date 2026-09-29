package deployment

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/backup"
)

func TestColdMediaRepairRequiresExplicitBoundaries(t *testing.T) {
	r := MediaRepairRequest{RequestID: "repair-one", PauseOperationID: "pause-one", Actor: "operator", Reason: "confirmed maintenance", Confirmed: true}
	if !r.valid() {
		t.Fatal("valid request rejected")
	}
	for _, change := range []func(*MediaRepairRequest){
		func(r *MediaRepairRequest) { r.Confirmed = false }, func(r *MediaRepairRequest) { r.RequestID = "../unsafe" }, func(r *MediaRepairRequest) { r.PauseOperationID = "" }, func(r *MediaRepairRequest) { r.Actor = " " }, func(r *MediaRepairRequest) { r.Reason = " " }, func(r *MediaRepairRequest) { r.Reason = strings.Repeat("x", 501) },
	} {
		bad := r
		change(&bad)
		if bad.valid() {
			t.Fatal("unconfirmed request accepted")
		}
	}
	x := fixtureExecutor(t, t.TempDir(), fixtureCatalog(), &fixtureRunner{})
	b := backup.Binding{TenantID: "a", ServerID: "server-a", ReleaseID: "release-one", ReleaseDigest: strings.Repeat("a", 64), Generation: 1, AccessVersion: 2, SchemaVersion: 79}
	if _, e := x.RepairColdMedia(t.Context(), b, r); e == nil {
		t.Fatal("unadopted/non-Docker source repaired")
	}
}

func TestColdMediaRepairSQLIsBoundAndTransactional(t *testing.T) {
	input := mediaRepairInput{Request: MediaRepairRequest{RequestID: "repair", PauseOperationID: "pause", Actor: "operator", Reason: "验证'; DROP TABLE im_users; --\n$()", Confirmed: true}}
	sql := mediaRepairSQL(input)
	if strings.Contains(sql, input.Request.Reason) {
		t.Fatal("operator text entered SQL syntax")
	}
	raw, _ := json.Marshal(input)
	if !strings.Contains(sql, base64.StdEncoding.EncodeToString(raw)) {
		t.Fatal("request not bound")
	}
	for _, required := range []string{"BEGIN;", "NOT access_enabled", "pauseOperationId", "state IN ('revoking','completed')", "backend_type='client backend'", "FOR UPDATE", "ACCESS EXCLUSIVE", "tenant.media.cold_repaired", "COMMIT;"} {
		if !strings.Contains(sql, required) {
			t.Fatal("missing guard", required)
		}
	}
	for _, forbidden := range []string{"UPDATE im_users", "UPDATE im_tenant_identity", "UPDATE im_tenant_realm_operations", "UPDATE im_tenant_realm_targets", "DELETE FROM im_calls"} {
		if strings.Contains(sql, forbidden) {
			t.Fatal("repair changes authority or business data", forbidden)
		}
	}
}
