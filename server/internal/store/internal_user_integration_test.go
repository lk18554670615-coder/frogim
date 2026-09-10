package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestInternalUserMigrationAndPermission(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := t.Context()
	schema := fmt.Sprintf("internal_user_%d", time.Now().UnixNano())
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err = conn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
	separator := "?"
	if strings.Contains(url, "?") {
		separator = "&"
	}
	schemaURL := url + separator + "search_path=" + schema + ",public"
	legacy, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(ctx, `
		CREATE TABLE im_schema_migrations(version integer PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now());
		INSERT INTO im_schema_migrations(version) VALUES(67);
		CREATE TABLE im_users(
			id text PRIMARY KEY,phone text UNIQUE NOT NULL,name text NOT NULL,handle text UNIQUE,
			signature text NOT NULL DEFAULT '',avatar_media_id text,avatar_url text NOT NULL DEFAULT '',
			banned boolean NOT NULL DEFAULT false,created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL DEFAULT now(),
			can_delete_messages_for_everyone boolean NOT NULL DEFAULT false,
			can_view_friend_login_ip boolean NOT NULL DEFAULT false
		)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id       string
		deletion bool
		ip       bool
	}{
		{"regular", false, false},
		{"delete_only", true, false},
		{"ip_only", false, true},
		{"both", true, true},
	} {
		if _, err = legacy.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,can_delete_messages_for_everyone,can_view_friend_login_ip)
			VALUES($1,$1,$1,now(),$2,$3)`, row.id, row.deletion, row.ip); err != nil {
			t.Fatal(err)
		}
	}
	if err = legacy.Close(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := NewPostgres(ctx, schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.migrate(ctx); err != nil {
		t.Fatal("repeat migrate", err)
	}
	for _, row := range []struct {
		id       string
		internal bool
	}{{"regular", false}, {"delete_only", true}, {"ip_only", true}, {"both", true}} {
		var internal bool
		if err = p.pool.QueryRow(ctx, `SELECT is_internal_user FROM im_users WHERE id=$1`, row.id).Scan(&internal); err != nil {
			t.Fatal(err)
		}
		if internal != row.internal {
			t.Fatalf("user %s migrated to internal=%v", row.id, internal)
		}
	}
	var oldColumns int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='im_users' AND column_name IN ('can_delete_messages_for_everyone','can_view_friend_login_ip')`).Scan(&oldColumns); err != nil || oldColumns != 0 {
		t.Fatalf("legacy permission columns remain: count=%d err=%v", oldColumns, err)
	}
	result, err := p.SetInternalUser(ctx, "operator", "regular", true, "internal operations", "127.0.0.1")
	if err != nil || !result.Changed || !result.IsInternalUser {
		t.Fatalf("set internal user: result=%+v err=%v", result, err)
	}
	var audits, events int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE action='user.internal_status.updated' AND target_id='regular'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit count=%d err=%v", audits, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox WHERE payload->>'event'='user.internal_status.updated'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("event count=%d err=%v", events, err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES('new_user','new_user','new_user',now())`); err != nil {
		t.Fatal(err)
	}
	internal, err := p.InternalUser(ctx, "new_user")
	if err != nil || internal {
		t.Fatalf("new user default internal=%v err=%v", internal, err)
	}
}
