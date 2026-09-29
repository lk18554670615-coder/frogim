package legacyimport

import (
	"strings"
	"testing"
)

func TestPostgresCutoverReceiptsUpgradeAndWriteBarrier(t *testing.T) {
	for _, open := range []bool{false, true} {
		t.Run(map[bool]string{false: "pre-write-rollback", true: "write-intent-no-rollback"}[open], func(t *testing.T) {
			f, s, p := importFixture(t)
			addLegacy(t, f, "original-id", "19900000298", false, nil)
			identity, e := DatabaseIdentity(t.Context(), f.source)
			if e != nil {
				t.Fatal(e)
			}
			r := CutoverRequest{ID: "cutover", TenantID: "default", BatchID: "cutover-import", Actor: "operator", Reason: "isolated cutover test", OriginalDatabase: "1/original", AdoptedDatabase: identity, OldVersions: map[string]string{"android": "1.0.0", "ios": "1.0.0", "web": "1.0.0"}}
			if _, e = StartCutover(t.Context(), f.target, r, true); e != nil {
				t.Fatal(e)
			}
			if _, e = p.RequestRealm(t.Context(), "default", "too-early", "operator", "test", 2, true, true); e == nil {
				t.Fatal("resume bypassed cutover")
			}
			previous := "planned"
			advance := func(next string) {
				t.Helper()
				step := CutoverStep{previous, next, "fixture verification", strings.Repeat("a", 64), true}
				if _, e := AdvanceCutover(t.Context(), f.source, f.target, r, step); e != nil {
					t.Fatal(next, e)
				}
				if _, e := AdvanceCutover(t.Context(), f.source, f.target, r, step); e != nil {
					t.Fatal("lost ACK", e)
				}
				previous = next
			}
			advance("stopped")
			advance("backed_up")
			advance("adopted")
			request := importRequest(t, f, r.BatchID)
			if _, e = StartImport(t.Context(), f.source, f.target, request); e != nil {
				t.Fatal(e)
			}
			if _, e = ResumeImportOne(t.Context(), f.source, f.target, r.BatchID, &dbRevoker{store: s}); e != nil {
				t.Fatal(e)
			}
			advance("imported")
			if _, e = AdvanceCutover(t.Context(), f.source, f.target, r, CutoverStep{previous, "routed", "fixture", strings.Repeat("a", 64), true}); e == nil {
				t.Fatal("missing upgrade accepted")
			}
			execFixture(t, f.target, `UPDATE platform_client_version_policies SET enabled=true,policy=jsonb_build_object('platform',platform,'minimumVersion','2.0.0','latestVersion','2.0.0','forceUpdate',true,'rolloutPercentage',100,'downloadUrl','https://download.example.test/client') WHERE platform IN ('android','ios','web')`)
			advance("routed")
			if open {
				advance("opening")
				if _, e = AdvanceCutover(t.Context(), f.source, f.target, r, CutoverStep{previous, "rolled_back", "unsafe", strings.Repeat("a", 64), true}); e == nil {
					t.Fatal("rollback after write intent")
				}
				if _, e = p.RequestRealm(t.Context(), "default", "open", "operator", "explicit open", 2, true, true); e != nil {
					t.Fatal("barrier did not permit normal controlled resume", e)
				}
			} else {
				advance("rolled_back")
				if _, e = p.RequestRealm(t.Context(), "default", "rollback-open", "operator", "test", 2, true, true); e == nil {
					t.Fatal("rolled back candidate reopened")
				}
			}
		})
	}
}

func TestPostgresCutoverAuditFailureAndChangedInput(t *testing.T) {
	f, _, _ := importFixture(t)
	identity, e := DatabaseIdentity(t.Context(), f.source)
	if e != nil {
		t.Fatal(e)
	}
	r := CutoverRequest{ID: "audit-cutover", TenantID: "default", BatchID: "batch", Actor: "operator", Reason: "test", OriginalDatabase: "1/original", AdoptedDatabase: identity, OldVersions: map[string]string{"android": "1.0", "ios": "1.0", "web": "1.0"}}
	if _, e = StartCutover(t.Context(), f.target, r, true); e != nil {
		t.Fatal(e)
	}
	changed := r
	changed.Reason = "changed"
	if _, e = StartCutover(t.Context(), f.target, changed, true); e == nil {
		t.Fatal("changed operation accepted")
	}
	execFixture(t, f.target, `CREATE FUNCTION reject_cutover() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='legacy.cutover.advanced' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_cutover BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION reject_cutover()`)
	if _, e = AdvanceCutover(t.Context(), nil, f.target, r, CutoverStep{"planned", "stopped", "test", strings.Repeat("b", 64), true}); e == nil {
		t.Fatal("audit failure accepted")
	}
	status, e := ReadCutover(t.Context(), f.target, r)
	if e != nil || status.Phase != "planned" {
		t.Fatal("audit transaction leaked", e)
	}
}
