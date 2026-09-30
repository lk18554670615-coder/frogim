package platform

import (
	"errors"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestPlatformTenantDirectoryLifecycle(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	hash, e := bcrypt.GenerateFromPassword([]byte("DirectoryPassword123!"), 12)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.BootstrapAdmin(ctx, "operator", string(hash)); e != nil {
		t.Fatal(e)
	}
	a := &API{Store: s, Limiter: testLimit{}}
	code, login := adminTestCall(t, a, "POST", "/platform/admin/auth/login", "", `{"username":"operator","password":"DirectoryPassword123!"}`)
	if code != 200 {
		t.Fatal(code, login)
	}
	token := login["accessToken"].(string)
	code, detail := adminTestCall(t, a, "GET", "/platform/admin/tenants/a", token, "")
	if code != 200 || detail["directoryVersion"] != float64(1) || detail["currentDefaultId"] != "a" {
		t.Fatal(code, detail)
	}
	code, _ = adminTestCall(t, a, "PATCH", "/platform/admin/tenants/a", token, `{"displayName":"New name","note":"internal note","expectedDirectoryVersion":1,"reason":"directory correction","confirmed":false}`)
	if code != 400 {
		t.Fatal("unconfirmed edit", code)
	}
	code, detail = adminTestCall(t, a, "PATCH", "/platform/admin/tenants/a", token, `{"displayName":"New name","note":"internal note","expectedDirectoryVersion":1,"reason":"directory correction","confirmed":true}`)
	if code != 200 || detail["displayName"] != "New name" || detail["note"] != "internal note" || detail["directoryVersion"] != float64(2) || detail["configVersion"] != float64(1) || detail["accessVersion"] != float64(1) {
		t.Fatal("edit changed service binding", code, detail)
	}
	code, _ = adminTestCall(t, a, "PATCH", "/platform/admin/tenants/a", token, `{"displayName":"stale","note":"","expectedDirectoryVersion":1,"reason":"stale edit","confirmed":true}`)
	if code != 409 {
		t.Fatal("stale edit", code)
	}
	code, _ = adminTestCall(t, a, "POST", "/platform/admin/tenants/a/archive", token, `{"expectedDirectoryVersion":2,"reason":"archive active","confirmed":true}`)
	if code != 409 {
		t.Fatal("active/default archive", code)
	}
	code, _ = adminTestCall(t, a, "POST", "/platform/admin/tenants/b/make-default", token, `{"expectedCurrentDefaultId":"a","expectedDirectoryVersion":1,"reason":"switch registration target","confirmed":true}`)
	if code != 200 {
		t.Fatal("default switch", code)
	}
	var isDefault bool
	if e = s.pool.QueryRow(ctx, `SELECT is_default FROM platform_tenants WHERE id='b'`).Scan(&isDefault); e != nil || !isDefault {
		t.Fatal("new default not active", e)
	}
	if e = s.pool.QueryRow(ctx, `SELECT is_default FROM platform_tenants WHERE id='a'`).Scan(&isDefault); e != nil || isDefault {
		t.Fatal("old default retained", e)
	}
	reserved, e := s.Reserve(ctx, Registration{Phone: "13800000981", Name: "new", Password: "Password123!", Method: "password"})
	var chosen string
	if e == nil {
		e = s.pool.QueryRow(ctx, `SELECT target_tenant_id FROM platform_jobs WHERE id=$1`, reserved.JobID).Scan(&chosen)
	}
	if e != nil || chosen != "b" {
		t.Fatal("registration did not use new default", chosen, e)
	}
	code, invite := adminTestCall(t, a, "POST", "/platform/admin/tenants/a/codes", token, `{"reason":"existing invite","confirmed":true}`)
	if code != 201 || invite["id"] == "" {
		t.Fatal(code, invite)
	}
	if _, e = s.pool.Exec(ctx, `INSERT INTO platform_backup_schedules(tenant_id,enabled,start_minute_utc,window_minutes,version,actor_id,reason) VALUES('a',true,120,60,1,'bootstrap','test')`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(ctx, `UPDATE platform_tenants SET status='suspended' WHERE id='a'`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(ctx, `INSERT INTO platform_realm_jobs(id,request_id,actor_id,tenant_id,enabled,expected_version,access_version,reason) VALUES('pending','pending','bootstrap','a',false,1,2,'test')`); e != nil {
		t.Fatal(e)
	}
	code, _ = adminTestCall(t, a, "POST", "/platform/admin/tenants/a/archive", token, `{"expectedDirectoryVersion":3,"reason":"archive requested","confirmed":true}`)
	if code != 409 {
		t.Fatal("pending job archive", code)
	}
	if _, e = s.pool.Exec(ctx, `UPDATE platform_realm_jobs SET state='completed' WHERE id='pending'`); e != nil {
		t.Fatal(e)
	}
	code, detail = adminTestCall(t, a, "POST", "/platform/admin/tenants/a/archive", token, `{"expectedDirectoryVersion":3,"reason":"archive requested","confirmed":true}`)
	if code != 200 || detail["archivedAt"] == nil || detail["maintenanceEnabled"] != false || detail["enabledCodeCount"] != float64(0) || detail["status"] != "suspended" {
		t.Fatal("archive was partial", code, detail)
	}
	code, _ = adminTestCall(t, a, "PUT", "/platform/admin/codes/"+invite["id"].(string)+"/status", token, `{"enabled":true,"reason":"should stay archived","confirmed":true}`)
	if code != 409 {
		t.Fatal("archived code re-enabled", code)
	}
	code, listing := adminTestCall(t, a, "GET", "/platform/admin/tenants", token, "")
	if code != 200 || listing["total"] != float64(1) {
		t.Fatal("archived tenant remained in default list", code, listing)
	}
	code, listing = adminTestCall(t, a, "GET", "/platform/admin/tenants?archive=archived", token, "")
	if code != 200 || listing["total"] != float64(1) {
		t.Fatal("archive filter", code, listing)
	}
	code, detail = adminTestCall(t, a, "POST", "/platform/admin/tenants/a/unarchive", token, `{"expectedDirectoryVersion":4,"reason":"return to directory","confirmed":true}`)
	if code != 200 || detail["archivedAt"] != nil || detail["status"] != "suspended" || detail["maintenanceEnabled"] != false || detail["enabledCodeCount"] != float64(0) {
		t.Fatal("unarchive reopened services", code, detail)
	}
	if _, e = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET role='reader' WHERE id='bootstrap'`); e != nil {
		t.Fatal(e)
	}
	code, _ = adminTestCall(t, a, "PATCH", "/platform/admin/tenants/a", token, `{"displayName":"forbidden","expectedDirectoryVersion":5,"reason":"reader write","confirmed":true}`)
	if code != 401 {
		t.Fatal("reader edit", code)
	}
	code, _ = adminTestCall(t, a, "GET", "/platform/admin/tenants/a", token, "")
	if code != 200 {
		t.Fatal("reader detail", code)
	}
}

func TestPlatformTenantDefaultSwitchConcurrent(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	if e := s.PutTenant(ctx, "c", "c", "https://c.example", "test", "third tenant", false); e != nil {
		t.Fatal(e)
	}
	if _, e := s.pool.Exec(ctx, `UPDATE platform_tenants SET status='active' WHERE id='c'`); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"b", "c"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			results <- s.makeDefaultTenant(ctx, id, "test", defaultTenantInput{ExpectedCurrentDefaultID: "a", ExpectedDirectoryVersion: 1, Reason: "concurrent switch", Confirmed: true})
		}(id)
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else if errors.Is(e, ErrTenantDirectoryChanged) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	var count int
	if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_tenants WHERE is_default`).Scan(&count); e != nil || ok != 1 || conflict != 1 || count != 1 {
		t.Fatal("default uniqueness", ok, conflict, count, e)
	}
}

func TestPlatformTenantDirectoryUpgrade(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	if _, e := s.pool.Exec(ctx, `ALTER TABLE platform_tenants DROP COLUMN note,DROP COLUMN directory_version,DROP COLUMN archived_at,DROP COLUMN archived_by; DELETE FROM platform_schema_migrations WHERE version=22`); e != nil {
		t.Fatal(e)
	}
	if e := s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	var name, address, note string
	var version int64
	if e := s.pool.QueryRow(ctx, `SELECT display_name,http_base_url,note,directory_version FROM platform_tenants WHERE id='a'`).Scan(&name, &address, &note, &version); e != nil || name != "a" || address != "https://a.example" || note != "" || version != 1 {
		t.Fatal("upgrade changed existing tenant", name, address, note, version, e)
	}
}
