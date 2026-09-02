package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func accessTestDatabase(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("IM_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	connection, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("user_access_%d", time.Now().UnixNano())
	if _, err = connection.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		connection.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer connection.Close(ctx)
		if _, err := connection.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	v := u.Query()
	v.Set("search_path", schema)
	u.RawQuery = v.Encode()
	return u.String()
}

func TestUserAccessSchema(t *testing.T) {
	if schemaVersion != 60 {
		t.Fatalf("version %d", schemaVersion)
	}
	for _, v := range []string{"im_user_access_profiles", "im_user_access_logs", "registration_ip inet", "last_login_ip inet", "im_user_access_logs_ip_time_idx", "im_user_access_logs_time_idx"} {
		if !strings.Contains(normalizedSchema, v) {
			t.Fatalf("missing %s", v)
		}
	}
}
func TestPostgresUserAccess(t *testing.T) {
	url := accessTestDatabase(t)
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	uid := "access_a_" + suffix
	peer := "access_b_" + suffix
	for _, id := range []string{uid, peer} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$1,$1,now())`, id); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		p.pool.Exec(ctx, `DELETE FROM im_user_access_logs WHERE id LIKE $1`, suffix+"%")
		p.pool.Exec(ctx, `DELETE FROM im_users WHERE id=ANY($1)`, []string{uid, peer})
	}()
	now := time.Now().UTC().Truncate(time.Microsecond)
	put := func(e UserAccessLog) {
		t.Helper()
		if err := p.RecordUserAccess(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	reg := UserAccessLog{ID: suffix + "reg", UserID: uid, Event: "register", Method: "password", Result: "success", IP: "::ffff:203.0.113.1", Platform: "android", OccurredAt: now.Add(-time.Hour)}
	put(reg)
	put(reg)
	e := reg
	e.ID = suffix + "new"
	e.Event = "login"
	e.IP = "2001:db8::1"
	e.OccurredAt = now
	put(e)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			old := e
			old.ID = suffix + strconv.Itoa(i)
			old.OccurredAt = now.Add(-time.Duration(i+1) * time.Second)
			old.IP = "203.0.113.2"
			if err := p.RecordUserAccess(ctx, old); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	failed := e
	failed.ID = suffix + "failed"
	failed.UserID = ""
	failed.LookupPhone = peer
	failed.Result = "failed"
	failed.FailureCode = "INVALID_CREDENTIALS"
	put(failed)
	profiles, err := p.UserAccessProfiles(ctx, []string{uid, peer}, "2001:db8::1")
	if err != nil {
		t.Fatal(err)
	}
	if profiles[uid].RegistrationIP != "203.0.113.1" || profiles[uid].LastLoginIP != "2001:db8::1" || profiles[peer].LastLoginIP != "" {
		t.Fatalf("%+v", profiles)
	}
	users, total, _, err := p.ListAdminUsersByIP(ctx, suffix, "", "", 20, "2001:db8::1", "any")
	if err != nil || total != 1 || len(users) != 1 || users[0].ID != uid {
		t.Fatalf("users=%+v total=%d err=%v", users, total, err)
	}
	q := UserAccessQuery{UserID: uid, From: now.Add(-24 * time.Hour), To: now.Add(time.Second), Limit: 3}
	seen := map[string]bool{}
	for {
		page, err := p.ListUserAccessLogs(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range page.Items {
			if seen[v.ID] {
				t.Fatal("duplicate pagination")
			}
			seen[v.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	if len(seen) != 10 {
		t.Fatalf("events=%d", len(seen))
	}
	q.UserID = peer
	q.Cursor = ""
	page, err := p.ListUserAccessLogs(ctx, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].UserID != peer {
		t.Fatalf("failed lookup: %+v %v", page, err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "LookupPhone") {
		t.Fatal("private field leaked")
	}
	old := reg
	old.ID = suffix + "expired"
	old.IP = "192.0.2.66"
	old.OccurredAt = now.Add(-181 * 24 * time.Hour)
	put(old)
	q = UserAccessQuery{IP: old.IP, From: now.Add(-365 * 24 * time.Hour), To: now, Limit: 20}
	page, err = p.ListUserAccessLogs(ctx, q)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("retention query leak", err)
	}
	if _, err = p.CleanupRuntimeData(ctx, RetentionPolicy{}, 1000); err != nil {
		t.Fatal(err)
	}
	var count int
	p.pool.QueryRow(ctx, `SELECT count(*) FROM im_user_access_logs WHERE id=$1`, old.ID).Scan(&count)
	if count != 0 {
		t.Fatal("retention cleanup failed")
	}
	profiles, _ = p.UserAccessProfiles(ctx, []string{uid}, "")
	if profiles[uid].RegistrationIP != reg.IP && profiles[uid].RegistrationIP != "203.0.113.1" {
		t.Fatal("registration overwritten")
	}
	// Re-opening applies the additive schema safely and preserves the summaries.
	again, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}

func TestPostgresUserAccessMigrationFrom59(t *testing.T) {
	url := accessTestDatabase(t)
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,password_hash,created_at) VALUES('legacy-ip-user','13900009999','Legacy user','unchanged-hash',now()); DROP TABLE im_user_access_logs; DROP TABLE im_user_access_profiles; DELETE FROM im_schema_migrations; INSERT INTO im_schema_migrations(version) VALUES(59)`)
	p.Close()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		p, err = NewPostgres(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		var version int
		var hash string
		err = p.pool.QueryRow(ctx, `SELECT max(version) FROM im_schema_migrations`).Scan(&version)
		if err != nil || version != 60 {
			t.Fatal(version, err)
		}
		err = p.pool.QueryRow(ctx, `SELECT password_hash FROM im_users WHERE id='legacy-ip-user'`).Scan(&hash)
		if err != nil || hash != "unchanged-hash" {
			t.Fatal(hash, err)
		}
		profile, err := p.UserAccessProfiles(ctx, []string{"legacy-ip-user"}, "")
		if err != nil || profile["legacy-ip-user"].RegistrationIP != "" || profile["legacy-ip-user"].RegistrationSource != "unknown" {
			t.Fatal(profile, err)
		}
		p.Close()
	}
}
