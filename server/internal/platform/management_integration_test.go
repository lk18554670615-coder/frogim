package platform

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

func adminTestCall(t *testing.T, api *API, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	api.PublicHandler().ServeHTTP(w, r)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("non JSON response %d", w.Code)
	}
	return w.Code, result
}

func TestPlatformPostgresManagementRealmCodesPaginationAndAudit(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	hash, err := bcrypt.GenerateFromPassword([]byte("TestPlatformPassword!"), 12)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BootstrapAdmin(ctx, "operator", string(hash)); err != nil {
		t.Fatal(err)
	}
	a := &API{Store: s, Limiter: testLimit{}}
	status, result := adminTestCall(t, a, "POST", "/platform/admin/auth/login", "", `{"username":"operator","password":"TestPlatformPassword!"}`)
	if status != 200 {
		t.Fatal(status)
	}
	token := result["accessToken"].(string)
	status, result = adminTestCall(t, a, "GET", "/platform/admin/auth/me", token, "")
	if status != 200 || result["role"] != "operator" {
		t.Fatal(status, result)
	}
	for _, route := range []string{"tenants", "accounts", "codes", "jobs", "audits"} {
		status, _ = adminTestCall(t, a, "GET", "/platform/admin/"+route, "enterprise.jwt", "")
		if status != 401 {
			t.Fatal("enterprise realm accepted", route, status)
		}
		status, result = adminTestCall(t, a, "GET", "/platform/admin/"+route+"?page=1&pageSize=1", token, "")
		if status != 200 || result["pageSize"] != float64(1) {
			t.Fatal(route, status, result)
		}
	}
	status, result = adminTestCall(t, a, "GET", "/platform/admin/tenants?page=9&pageSize=1", token, "")
	if status != 200 || result["total"] != float64(2) || len(result["items"].([]any)) != 0 {
		t.Fatal(status, result)
	}
	status, _ = adminTestCall(t, a, "GET", "/platform/admin/tenants?page=-1", token, "")
	if status != 400 {
		t.Fatal(status)
	}
	status, result = adminTestCall(t, a, "POST", "/platform/admin/tenants/a/codes", token, `{"reason":"test operation","confirmed":true}`)
	if status != 201 {
		t.Fatal(status, result)
	}
	id := result["id"].(string)
	code := result["code"].(string)
	if len(code) != 43 {
		t.Fatal("invalid code")
	}
	status, result = adminTestCall(t, a, "GET", "/platform/admin/codes?tenantId=a", token, "")
	encoded, _ := json.Marshal(result)
	if status != 200 || strings.Contains(string(encoded), code) || strings.Contains(string(encoded), "code_hash") {
		t.Fatal("code disclosure")
	}
	status, _ = adminTestCall(t, a, "PUT", "/platform/admin/codes/"+id+"/status", token, `{"enabled":false,"reason":"停用测试","confirmed":false}`)
	if status != 400 {
		t.Fatal(status)
	}
	for range 2 {
		status, _ = adminTestCall(t, a, "PUT", "/platform/admin/codes/"+id+"/status", token, `{"enabled":false,"reason":"停用测试","confirmed":true}`)
		if status != 200 {
			t.Fatal(status)
		}
	}
	var auditCount int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_audits WHERE action='tenant.code.status.updated'`).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatal(auditCount, err)
	}
	_, err = s.Reserve(ctx, Registration{Phone: "13800000001", Name: "new", Password: "Password123!", Method: "password", EnterpriseCode: code})
	if !errors.Is(err, ErrDenied) {
		t.Fatal("disabled code usable", err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET role='reader' WHERE id='bootstrap'`); err != nil {
		t.Fatal(err)
	}
	status, _ = adminTestCall(t, a, "POST", "/platform/admin/tenants/a/codes", token, `{"reason":"reader attempt","confirmed":true}`)
	if status != 401 {
		t.Fatal("reader write", status)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET enabled=false WHERE id='bootstrap'`); err != nil {
		t.Fatal(err)
	}
	status, _ = adminTestCall(t, a, "GET", "/platform/admin/accounts", token, "")
	if status != 401 {
		t.Fatal("disabled operator", status)
	}
}

func TestPlatformPostgresRepairCannotSkipOrRaceIdentityWork(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	reservation, err := s.Reserve(ctx, Registration{Phone: "13800000003", Name: "repair", Password: "Password123!", Method: "password"})
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.Claim(ctx)
	if err != nil || j == nil {
		t.Fatal(err)
	}
	newCode := "REFERRAL"
	in := JobRepair{ExpectedStep: "prepare_target", Reason: "repair test", Confirmed: true, PersonalInviteCode: &newCode}
	if err = s.RepairJob(ctx, reservation.JobID, "operator", in, true); !errors.Is(err, ErrConflict) {
		t.Fatal("changed leased task", err)
	}
	if err = s.Block(ctx, j, "INVITE_INVALID"); err != nil {
		t.Fatal(err)
	}
	if candidate, err := s.Claim(ctx); err != nil || candidate != nil {
		t.Fatal("blocked task retried automatically", err)
	}
	if err = s.RepairJob(ctx, reservation.JobID, "operator", in, true); err != nil {
		t.Fatal(err)
	}
	j, err = s.Claim(ctx)
	if err != nil || j.Input.PersonalInviteCode != newCode || j.Step != "prepare_target" {
		t.Fatal(j, err)
	}
	if err = s.Retry(ctx, j, "ENTERPRISE_OPERATION_UNCONFIRMED"); err != nil {
		t.Fatal(err)
	}
	if err = s.RepairJob(ctx, reservation.JobID, "operator", in, true); !errors.Is(err, ErrConflict) {
		t.Fatal("rewrote uncertain preparation", err)
	}
	in.ExpectedStep = "activate"
	if err = s.RepairJob(ctx, reservation.JobID, "operator", in, false); !errors.Is(err, ErrConflict) {
		t.Fatal("skipped prepare", err)
	}
	in.ExpectedStep = "prepare_target"
	if err = s.RepairJob(ctx, reservation.JobID, "operator", in, false); err != nil {
		t.Fatal(err)
	}
	var audit []byte
	if err = s.pool.QueryRow(ctx, `SELECT jsonb_agg(metadata) FROM platform_audits`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(audit), newCode) || strings.Contains(string(audit), "Password123") {
		t.Fatal("secret in audit")
	}
	if !tenancy.RepairableCode("INVITE_INVALID") || tenancy.RepairableCode("password=secret") {
		t.Fatal("unsafe code allowlist")
	}
}
