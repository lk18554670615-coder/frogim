package platform

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

func adminCreateInput(id, name, role string) AdminOperation {
	enabled := true
	return AdminOperation{RequestID: id, Confirmed: true, Password: "IsolatedAdminPassword123!", AdminOperationInput: AdminOperationInput{Action: "create", Username: name, Role: role, Enabled: &enabled, Reason: "isolated administrator test"}}
}
func adminSessionForTest(t *testing.T, s *Store, id string) string {
	t.Helper()
	token, err := tenancy.Secret()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(t.Context(), `INSERT INTO platform_admin_sessions(token_hash,admin_id,auth_version,expires_at) SELECT $1,id,auth_version,now()+interval '1 hour' FROM platform_admin_accounts WHERE id=$2`, tenancy.Hash(token), id); err != nil {
		t.Fatal(err)
	}
	return token
}
func TestPlatformPostgresAdministratorLifecycle(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	token := releaseTestAdmin(t, s)
	const actor = "release-operator"
	in := adminCreateInput("new-reader", "SupportReader", "reader")
	in.Password = "123456"
	created, err := s.ManageAdministrator(ctx, actor, token, in)
	if err != nil || created.Role != "reader" || !created.Enabled || created.AuthVersion != 1 {
		t.Fatal("create", err)
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			r, e := s.ManageAdministrator(ctx, actor, token, in)
			if e != nil || r.ID != created.ID {
				t.Error("creation replay", e)
			}
		})
	}
	wg.Wait()
	changed := in
	changed.Password = "DifferentPassword123!"
	if _, err = s.ManageAdministrator(ctx, actor, token, changed); !errors.Is(err, ErrRequestChanged) {
		t.Fatal("password drift replay", err)
	}
	changed = in
	changed.RequestID = "case-collision"
	changed.Username = "supportreader"
	if _, err = s.ManageAdministrator(ctx, actor, token, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("case collision", err)
	}
	api := &API{Store: s, Limiter: testLimit{}}
	body, _ := json.Marshal(map[string]string{"username": "SUPPORTREADER", "password": in.Password})
	status, data := adminTestCall(t, api, "POST", "/platform/admin/auth/login", "", string(body))
	if status != 200 {
		t.Fatal("folded login", status)
	}
	readerToken := data["accessToken"].(string)
	if _, err = s.ManageAdministrator(ctx, created.ID, readerToken, adminCreateInput("reader-escalate", "Escalation", "operator")); !errors.Is(err, ErrDenied) {
		t.Fatal("reader escalation", err)
	}
	self := AdminOperation{RequestID: "self-reset", Confirmed: true, Password: "ReaderNextPassword123!", CurrentPassword: in.Password, AdminOperationInput: AdminOperationInput{Action: "password", TargetID: created.ID, ExpectedVersion: 1, Reason: "own password change"}}
	result, err := s.ManageAdministrator(ctx, created.ID, readerToken, self)
	if err != nil || result.AuthVersion != 2 {
		t.Fatal("self password", err)
	}
	if status, _ = adminTestCall(t, api, "GET", "/platform/admin/auth/me", readerToken, ""); status != 401 {
		t.Fatal("reader old session survived", status)
	}
	if status, _ = adminTestCall(t, api, "POST", "/platform/admin/auth/login", "", string(body)); status != 401 {
		t.Fatal("old password survived", status)
	}
	body, _ = json.Marshal(map[string]string{"username": created.Username, "password": self.Password})
	if status, data = adminTestCall(t, api, "POST", "/platform/admin/auth/login", "", string(body)); status != 200 {
		t.Fatal("new password", status)
	}
	readerToken = data["accessToken"].(string)
	if status, _ = adminTestCall(t, api, "GET", "/platform/admin/administrators/operations/self-reset", readerToken, ""); status != 200 {
		t.Fatal("self reset result after reauthentication", status)
	}
	disabled := false
	access := AdminOperation{RequestID: "disable-reader", Confirmed: true, AdminOperationInput: AdminOperationInput{Action: "access", TargetID: created.ID, Role: "reader", Enabled: &disabled, ExpectedVersion: 2, Reason: "disable test"}}
	result, err = s.ManageAdministrator(ctx, actor, token, access)
	if err != nil || result.Enabled || result.AuthVersion != 3 {
		t.Fatal("disable", err)
	}
	if status, _ = adminTestCall(t, api, "GET", "/platform/admin/auth/me", readerToken, ""); status != 401 {
		t.Fatal("disabled session survived", status)
	}
	enabled := true
	access.RequestID = "reenable-reader"
	access.Enabled = &enabled
	access.ExpectedVersion = 3
	result, err = s.ManageAdministrator(ctx, actor, token, access)
	if err != nil || !result.Enabled || result.AuthVersion != 4 {
		t.Fatal("reenable", err)
	}
	if status, _ = adminTestCall(t, api, "GET", "/platform/admin/auth/me", readerToken, ""); status != 401 {
		t.Fatal("old session restored", status)
	}
	result, err = s.ManageAdministrator(ctx, actor, token, in)
	if err != nil || result.AuthVersion != 1 {
		t.Fatal("creation replay overwritten", err)
	}
	access.RequestID = "stale"
	access.ExpectedVersion = 2
	if _, err = s.ManageAdministrator(ctx, actor, token, access); !errors.Is(err, ErrAdminChanged) {
		t.Fatal("stale edit", err)
	}
	access.RequestID = "disable-self"
	access.TargetID = actor
	access.ExpectedVersion = 1
	access.Enabled = &disabled
	access.Role = "operator"
	if _, err = s.ManageAdministrator(ctx, actor, token, access); !errors.Is(err, ErrAdminSelf) {
		t.Fatal("self lockout", err)
	}
	var audits string
	if err = s.pool.QueryRow(ctx, `SELECT coalesce(jsonb_agg(metadata)::text,'') FROM platform_audits WHERE action='administrator.updated'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audits, in.Password) || strings.Contains(audits, self.Password) || strings.Contains(audits, `"password":`) || strings.Contains(audits, `"password_hash":`) || strings.Contains(audits, "$2a$") {
		t.Fatal("audit contains credentials")
	}
	if status, data = adminTestCall(t, api, "GET", "/platform/admin/administrators?q=support&state=enabled", token, ""); status != 200 || data["total"] != float64(1) {
		t.Fatal("filtered list", status)
	}
	public, _ := json.Marshal(data)
	if strings.Contains(string(public), "password") {
		t.Fatal("list leaked hash")
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("repeat migration", err)
	}
	var version int64
	if err = s.pool.QueryRow(ctx, `SELECT auth_version FROM platform_admin_accounts WHERE id=$1`, created.ID).Scan(&version); err != nil || version != 4 {
		t.Fatal("migration altered admin", err)
	}
}

func TestPlatformPostgresAdministratorRacesAndRollback(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	token := releaseTestAdmin(t, s)
	second, err := s.ManageAdministrator(ctx, "release-operator", token, adminCreateInput("create-second", "SecondOperator", "operator"))
	if err != nil {
		t.Fatal(err)
	}
	secondToken := adminSessionForTest(t, s, second.ID)
	disabled := false
	left := AdminOperation{RequestID: "left-disable", Confirmed: true, AdminOperationInput: AdminOperationInput{Action: "access", TargetID: second.ID, ExpectedVersion: 1, Role: "operator", Enabled: &disabled, Reason: "concurrent lifecycle"}}
	right := left
	right.RequestID = "right-disable"
	right.TargetID = "release-operator"
	results := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Go(func() { _, e := s.ManageAdministrator(ctx, "release-operator", token, left); results <- e })
	wg.Go(func() { _, e := s.ManageAdministrator(ctx, second.ID, secondToken, right); results <- e })
	wg.Wait()
	close(results)
	success, denied := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, ErrDenied) {
			denied++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || denied != 1 {
		t.Fatal("mutual disable race", success, denied)
	}
	var survivor string
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT min(id),count(*) FROM platform_admin_accounts WHERE enabled AND role='operator'`).Scan(&survivor, &count); err != nil || count != 1 {
		t.Fatal("no operator remains", err)
	}
	survivorToken := token
	if survivor == second.ID {
		survivorToken = secondToken
	}
	if _, err = s.pool.Exec(ctx, `CREATE FUNCTION reject_admin_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='administrator.updated' THEN RAISE EXCEPTION 'isolated audit outage'; END IF; RETURN NEW; END $$; CREATE TRIGGER admin_audit_failure BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION reject_admin_audit();`); err != nil {
		t.Fatal(err)
	}
	reset := AdminOperation{RequestID: "audit-failed-reset", Confirmed: true, Password: "NeverCommittedPassword123!", AdminOperationInput: AdminOperationInput{Action: "password", TargetID: survivor, ExpectedVersion: 1, Reason: "audit rollback"}}
	// Give the fixture a real known hash; no production account is affected.
	reset.CurrentPassword = "BeforeAdminPassword123!"
	hash, _ := bcrypt.GenerateFromPassword([]byte(reset.CurrentPassword), 12)
	if _, err = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET password_hash=$2 WHERE id=$1`, survivor, string(hash)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ManageAdministrator(ctx, survivor, survivorToken, reset); err == nil {
		t.Fatal("audit outage accepted")
	}
	var currentHash string
	var version int64
	if err = s.pool.QueryRow(ctx, `SELECT password_hash,auth_version FROM platform_admin_accounts WHERE id=$1`, survivor).Scan(&currentHash, &version); err != nil || version != 1 || bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(reset.CurrentPassword)) != nil {
		t.Fatal("partial credential write", err)
	}
	if status, _ := adminTestCall(t, &API{Store: s, Limiter: testLimit{}}, "GET", "/platform/admin/auth/me", survivorToken, ""); status != 200 {
		t.Fatal("rollback revoked session", status)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_admin_operations WHERE request_id='audit-failed-reset'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial replay record", err)
	}
}

func TestPlatformPostgresAdministratorHTTPBoundary(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	token := releaseTestAdmin(t, s)
	api := &API{Store: s, Limiter: testLimit{}}
	input := adminCreateInput("http-create", "HTTPReader", "reader")
	body, _ := json.Marshal(input)
	for _, credential := range []string{"", strings.Repeat("x", 43)} {
		if status, _ := adminTestCall(t, api, "POST", "/platform/admin/administrators/operations", credential, string(body)); status != 401 {
			t.Fatal("unauthenticated write", status)
		}
	}
	status, data := adminTestCall(t, api, "POST", "/platform/admin/administrators/operations", token, string(body))
	if status != 200 {
		t.Fatal("valid creation", status)
	}
	id := data["id"].(string)
	readerToken := adminSessionForTest(t, s, id)
	for _, route := range []string{"/platform/admin/administrators", "/platform/admin/administrators?q=http&state=enabled"} {
		if status, _ = adminTestCall(t, api, "GET", route, readerToken, ""); status != 200 {
			t.Fatal("reader list", status)
		}
	}
	if status, _ = adminTestCall(t, api, "GET", "/platform/admin/administrators/operations/http-create", readerToken, ""); status != 404 {
		t.Fatal("other actor operation read", status)
	}
	if status, _ = adminTestCall(t, api, "POST", "/platform/admin/administrators/operations", readerToken, string(body)); status != 401 {
		t.Fatal("reader operator write", status)
	}
	if status, _ = adminTestCall(t, api, "GET", "/platform/admin/administrators/operations/http-create", token, ""); status != 200 {
		t.Fatal("own request lookup", status)
	}
	wrong := AdminOperation{RequestID: "wrong-current", Confirmed: true, Password: "ChangedReaderPassword123!", CurrentPassword: "IncorrectCurrentPassword", AdminOperationInput: AdminOperationInput{Action: "password", TargetID: id, ExpectedVersion: 1, Reason: "verify current password"}}
	raw, _ := json.Marshal(wrong)
	if status, data = adminTestCall(t, api, "POST", "/platform/admin/administrators/operations", readerToken, string(raw)); status != 400 || data["error"].(map[string]any)["code"] != "ADMIN_CURRENT_PASSWORD_INVALID" {
		t.Fatal("wrong current password", status)
	}
	if status, _ = adminTestCall(t, api, "GET", "/platform/admin/auth/me", readerToken, ""); status != 200 {
		t.Fatal("wrong password revoked valid session", status)
	}
	for i, change := range []func(*AdminOperation){func(p *AdminOperation) { p.Confirmed = false }, func(p *AdminOperation) { p.Reason = "" }, func(p *AdminOperation) { p.Role = "superadmin" }, func(p *AdminOperation) { p.Password = "short" }, func(p *AdminOperation) { p.Password = strings.Repeat("密", 25) }, func(p *AdminOperation) { p.Username = "bad/name" }} {
		invalid := input
		invalid.RequestID = "invalid-input"
		change(&invalid)
		if _, err := s.ManageAdministrator(ctx, "release-operator", token, invalid); !errors.Is(err, tenancy.ErrInvalid) {
			t.Fatal("invalid accepted", i, err)
		}
	}
	if _, err := s.pool.Exec(ctx, `UPDATE platform_admin_sessions SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, tenancy.Hash(token)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ManageAdministrator(ctx, "release-operator", token, input); !errors.Is(err, ErrDenied) {
		t.Fatal("expired session replay", err)
	}
	var leaked bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_admin_operations WHERE input::text LIKE '%'||$1||'%' OR snapshot::text LIKE '%'||$1||'%')`, input.Password).Scan(&leaked); err != nil || leaked {
		t.Fatal("operation stored plaintext", err)
	}
}
