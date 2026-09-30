package platform

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestPlatformOverviewAndIdentitySearch(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	hash, err := bcrypt.GenerateFromPassword([]byte("OverviewPassword123!"), 12)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BootstrapAdmin(ctx, "operator", string(hash)); err != nil {
		t.Fatal(err)
	}
	a := &API{Store: s, Limiter: testLimit{}}
	status, login := adminTestCall(t, a, "POST", "/platform/admin/auth/login", "", `{"username":"operator","password":"OverviewPassword123!"}`)
	if status != 200 {
		t.Fatal(status)
	}
	token := login["accessToken"].(string)
	if status, _ = adminTestCall(t, a, "GET", "/platform/admin/overview", "enterprise.jwt", ""); status != 401 {
		t.Fatal("enterprise token accepted", status)
	}
	status, overview := adminTestCall(t, a, "GET", "/platform/admin/overview", token, "")
	if status != 200 || overview["generatedAt"] == nil || overview["work"].(map[string]any)["attention"] != float64(0) {
		t.Fatal(status, overview)
	}
	reservation, err := s.Reserve(ctx, Registration{Phone: "13800000991", Name: "overview", Password: "Password123!", Method: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE platform_jobs SET blocked=true,error_code='INVITE_INVALID' WHERE id=$1`, reservation.JobID); err != nil {
		t.Fatal(err)
	}
	status, overview = adminTestCall(t, a, "GET", "/platform/admin/overview", token, "")
	if status != 200 || overview["work"].(map[string]any)["attention"] != float64(1) {
		t.Fatal(status, overview)
	}
	items := overview["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != reservation.JobID {
		t.Fatal(items)
	}
	var accountID string
	if err = s.pool.QueryRow(ctx, `SELECT account_id FROM platform_jobs WHERE id=$1`, reservation.JobID).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{reservation.JobID, accountID} {
		status, jobs := adminTestCall(t, a, "GET", "/platform/admin/jobs?q="+q, token, "")
		if status != 200 || jobs["total"] != float64(1) {
			t.Fatal(q, status, jobs)
		}
	}
	status, jobs := adminTestCall(t, a, "GET", "/platform/admin/jobs?q=missing", token, "")
	if status != 200 || jobs["total"] != float64(0) {
		t.Fatal(status, jobs)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET role='reader' WHERE id='bootstrap'`); err != nil {
		t.Fatal(err)
	}
	if status, _ = adminTestCall(t, a, "GET", "/platform/admin/overview", token, ""); status != 200 {
		t.Fatal("reader overview", status)
	}
}
