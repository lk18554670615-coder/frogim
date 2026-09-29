package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
)

func releaseTestAdmin(t *testing.T, s *Store) string {
	t.Helper()
	token, e := tenancy.Secret()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(t.Context(), `INSERT INTO platform_admin_accounts(id,username,password_hash,role) VALUES('release-operator','release-operator','test-not-used-for-login','operator');`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(t.Context(), `INSERT INTO platform_admin_sessions(token_hash,admin_id,auth_version,expires_at) VALUES($1,'release-operator',1,now()+interval '1 hour')`, tenancy.Hash(token)); e != nil {
		t.Fatal(e)
	}
	return token
}
func releaseInput(id string) ClientReleaseWrite {
	return ClientReleaseWrite{RequestID: id, Reason: "isolated release verification", Confirmed: true, ClientReleaseInput: ClientReleaseInput{Enabled: true, MinimumVersion: "1.0.0", LatestVersion: "2.0.0", RolloutPercentage: 100, ReleaseNotes: "isolated changes", DownloadURL: "https://downloads.example/app"}}
}
func TestPlatformPostgresClientReleaseLifecycle(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	token := releaseTestAdmin(t, s)
	a := &API{Store: s, Limiter: testLimit{}}
	for _, p := range []string{"android", "ios", "web", "macos"} {
		r, e := s.ClientRelease(ctx, p)
		if e != nil || r.Enabled || r.Revision != 0 {
			t.Fatal("unsafe initial policy", p, e)
		}
		status, data := adminTestCall(t, a, "GET", "/v2/config/version?platform="+p+"&version=1.0.12&installId=test-device-123", "", "")
		if status != 200 || data["data"].(map[string]any)["updateAvailable"] != false {
			t.Fatal("initial version check", status)
		}
	}
	in := releaseInput("publish-1")
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			r, e := s.PublishClientRelease(ctx, "android", "release-operator", in)
			if e != nil || r.Revision != 1 {
				t.Error("duplicate request", r.Revision, e)
			}
		})
	}
	wg.Wait()
	var count int
	if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_client_version_releases`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	if e := s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	r, e := s.ClientRelease(ctx, "android")
	if e != nil || !r.Enabled || r.Revision != 1 {
		t.Fatal("migration overwrote policy", e)
	}
	status, data := adminTestCall(t, a, "GET", "/v2/config/version?platform=android&version=1.1.0&installId=test-device-123", "", "")
	public, _ := json.Marshal(data)
	if status != 200 || data["data"].(map[string]any)["updateAvailable"] != true || strings.Contains(string(public), "updatedBy") || strings.Contains(string(public), "release-operator") || strings.Contains(string(public), "test-device") {
		t.Fatal("public response incorrect", status)
	}
	changed := in
	changed.DownloadURL = "https://other.example/app"
	if _, e = s.PublishClientRelease(ctx, "android", "release-operator", changed); !errors.Is(e, ErrRequestChanged) {
		t.Fatal("same request changed", e)
	}
	changed = in
	changed.RequestID = "stale"
	if _, e = s.PublishClientRelease(ctx, "android", "release-operator", changed); !errors.Is(e, ErrReleaseChanged) {
		t.Fatal("stale overwrite", e)
	}
	changed = in
	changed.ExpectedRevision = 1
	changed.Enabled = false
	changed.RequestID = "disable"
	if _, e = s.PublishClientRelease(ctx, "android", "release-operator", changed); e != nil {
		t.Fatal(e)
	}
	if r, e = s.PublishClientRelease(ctx, "android", "release-operator", in); e != nil || r.Revision != 1 || !r.Enabled {
		t.Fatal("lost response replay changed", e)
	}
	r, e = s.ClientRelease(ctx, "android")
	if e != nil || r.Enabled || r.Revision != 2 {
		t.Fatal("replay undid newer policy", e)
	}
	status, data = adminTestCall(t, a, "GET", "/v2/config/version?platform=android&version=0.1&installId=test-device-123", "", "")
	if status != 200 || data["data"].(map[string]any)["forceUpdate"] != false || data["data"].(map[string]any)["downloadUrl"] != "" {
		t.Fatal("disabled still active", status)
	}
	status, data = adminTestCall(t, a, "GET", "/platform/admin/client-versions/android/history?pageSize=1&page=2", token, "")
	if status != 200 || data["total"] != float64(2) || data["items"].([]any)[0].(map[string]any)["revision"] != float64(1) {
		t.Fatal("history", status)
	}
	// A release audit failure must roll back the policy and immutable snapshot.
	if _, e = s.pool.Exec(ctx, `CREATE FUNCTION reject_release_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='client_version.published' THEN RAISE EXCEPTION 'isolated audit outage'; END IF; RETURN NEW; END $$; CREATE TRIGGER test_release_audit BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION reject_release_audit();`); e != nil {
		t.Fatal(e)
	}
	changed.RequestID = "audit-outage"
	changed.ExpectedRevision = 2
	changed.Enabled = true
	if _, e = s.PublishClientRelease(ctx, "android", "release-operator", changed); e == nil {
		t.Fatal("audit failure accepted")
	}
	r, e = s.ClientRelease(ctx, "android")
	if e != nil || r.Revision != 2 || r.Enabled {
		t.Fatal("audit partial commit", e)
	}
	if e = s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_client_version_releases`).Scan(&count); e != nil || count != 2 {
		t.Fatal("partial snapshot", count, e)
	}
}

func TestPlatformPostgresClientReleasePermissionsAndValidation(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	token := releaseTestAdmin(t, s)
	a := &API{Store: s, Limiter: testLimit{}}
	in := releaseInput("http-release")
	body, _ := json.Marshal(in)
	path := "/platform/admin/client-versions/android"
	for _, credential := range []string{"", strings.Repeat("e", 43)} {
		if status, _ := adminTestCall(t, a, "PUT", path, credential, string(body)); status != 401 {
			t.Fatal("foreign permission", status)
		}
	}
	if status, _ := adminTestCall(t, a, "PUT", path, token, string(body)); status != 200 {
		t.Fatal("operator publish", status)
	}
	stale := in
	stale.RequestID = "new-stale-request"
	staleBody, _ := json.Marshal(stale)
	if status, data := adminTestCall(t, a, "PUT", path, token, string(staleBody)); status != 409 || data["error"].(map[string]any)["code"] != "CLIENT_VERSION_POLICY_CHANGED" {
		t.Fatal("stale HTTP contract", status)
	}
	if _, e := s.PublishClientRelease(ctx, "ios", "release-operator", in); !errors.Is(e, ErrRequestChanged) {
		t.Fatal("request ID reused across platforms", e)
	}
	response := httptest.NewRecorder()
	a.PublicHandler().ServeHTTP(response, httptest.NewRequest("GET", "/v2/config/version?platform=android&version=1.1&installId=valid-client-id", nil))
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("personal rollout response must not be shared or cached")
	}
	for i, change := range []func(*ClientReleaseWrite){
		func(v *ClientReleaseWrite) { v.Confirmed = false }, func(v *ClientReleaseWrite) { v.Reason = "" }, func(v *ClientReleaseWrite) { v.MinimumVersion = "3.0.0" },
		func(v *ClientReleaseWrite) { v.RolloutPercentage = 101 }, func(v *ClientReleaseWrite) { v.RolloutPercentage = -1 }, func(v *ClientReleaseWrite) { v.DownloadURL = "" },
		func(v *ClientReleaseWrite) { v.DownloadURL = "http://downloads.example/app" }, func(v *ClientReleaseWrite) { v.DownloadURL = "https://secret@downloads.example/app" },
		func(v *ClientReleaseWrite) { v.DownloadURL = "https://downloads.example/app?token=secret" }, func(v *ClientReleaseWrite) { v.DownloadURL = "https://downloads.example/app#hash" },
		func(v *ClientReleaseWrite) { v.DownloadURL = "https://downloads.example/app#" }, func(v *ClientReleaseWrite) { v.DownloadURL = "https://downloads.example/app?" },
		func(v *ClientReleaseWrite) { v.ReleaseNotes = strings.Repeat("n", 4001) }, func(v *ClientReleaseWrite) { v.LatestVersion = "2-beta" },
	} {
		v := releaseInput(fmt.Sprintf("invalid-%d", i))
		change(&v)
		if _, e := s.PublishClientRelease(ctx, "ios", "release-operator", v); !errors.Is(e, tenancy.ErrInvalid) {
			t.Fatal("invalid accepted", i, e)
		}
	}
	if _, e := s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET role='reader' WHERE id='release-operator'`); e != nil {
		t.Fatal(e)
	}
	if status, _ := adminTestCall(t, a, "PUT", path, token, string(body)); status != 401 {
		t.Fatal("reader published", status)
	}
	if _, e := s.PublishClientRelease(ctx, "ios", "release-operator", in); !errors.Is(e, ErrDenied) {
		t.Fatal("stale actor bypass", e)
	}
	if status, _ := adminTestCall(t, a, "GET", "/platform/admin/client-versions", token, ""); status != 200 {
		t.Fatal("reader inspection", status)
	}
	if _, e := s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET role='operator',enabled=false WHERE id='release-operator'`); e != nil {
		t.Fatal(e)
	}
	if status, _ := adminTestCall(t, a, "PUT", path, token, string(body)); status != 401 {
		t.Fatal("disabled operator", status)
	}
	// Missing storage is an error, not an invented no-update response.
	if _, e := s.pool.Exec(ctx, `ALTER TABLE platform_client_version_policies RENAME TO unavailable_policies`); e != nil {
		t.Fatal(e)
	}
	if status, _ := adminTestCall(t, a, "GET", "/v2/config/version?platform=android&version=1.0&installId=test-device-123", "", ""); status != 503 {
		t.Fatal("storage failure fell back", status)
	}
}

func TestPlatformPostgresClientReleaseConcurrentEditors(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	releaseTestAdmin(t, s)
	const editors = 8
	results := make(chan error, editors)
	var wg sync.WaitGroup
	for i := range editors {
		wg.Go(func() {
			_, err := s.PublishClientRelease(ctx, "web", "release-operator", releaseInput(fmt.Sprintf("editor-%d", i)))
			results <- err
		})
	}
	wg.Wait()
	close(results)
	committed, rejected := 0, 0
	for err := range results {
		switch {
		case err == nil:
			committed++
		case errors.Is(err, ErrReleaseChanged):
			rejected++
		default:
			t.Fatal("unexpected concurrent result", err)
		}
	}
	if committed != 1 || rejected != editors-1 {
		t.Fatal("lost update", committed, rejected)
	}
	var revisions, audits int
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM platform_client_version_releases),(SELECT count(*) FROM platform_audits WHERE action='client_version.published')`).Scan(&revisions, &audits); err != nil || revisions != 1 || audits != 1 {
		t.Fatal("losing editor produced history/audit", revisions, audits, err)
	}
}
