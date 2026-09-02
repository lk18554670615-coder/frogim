package store

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresPresencePermissions(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := t.Context()
	schema := fmt.Sprintf("presence_%d", time.Now().UnixNano())
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err = conn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.Exec(context.Background(), `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	p, err := NewPostgres(ctx, url+sep+"search_path="+schema+",public")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for i, id := range []string{"owner", "admin", "member", "friend", "outsider"} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$2,$1,now(),now())`, id, fmt.Sprintf("138%08d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.CreateGroupRecord(ctx, "group", "owner", "Presence group", []string{"admin", "member"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := p.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE im_members SET role='admin' WHERE conversation_id='group' AND user_id='admin'`)
	exec(`INSERT INTO im_friendships(user_id,friend_user_id) VALUES('member','friend')`)
	check := func(actor, group string, want ...string) {
		t.Helper()
		got, err := p.AllowedPresenceTargets(ctx, actor, []string{"owner", "admin", "member", "friend", "outsider", "unknown"}, group)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s %s got=%v want=%v", actor, group, got, want)
		}
		for _, id := range want {
			if !got[id] {
				t.Fatalf("missing %s in %v", id, got)
			}
		}
	}
	check("member", "", "member", "friend")
	check("owner", "", "owner")
	check("owner", "group", "owner", "admin", "member")
	check("admin", "group", "owner", "admin", "member")
	check("member", "group", "member", "friend")
	check("outsider", "group", "outsider")
	check("admin", "wrong-group", "admin")
	exec(`UPDATE im_members SET role='member' WHERE user_id='admin'`)
	check("admin", "group", "admin")
	exec(`DELETE FROM im_members WHERE user_id='member'`)
	check("owner", "group", "owner", "admin")
	exec(`INSERT INTO im_blocks(user_id,blocked_user_id) VALUES('friend','member')`)
	check("member", "", "member")
	exec(`DELETE FROM im_blocks`)
	exec(`DELETE FROM im_friendships`)
	check("member", "", "member")
	exec(`UPDATE im_groups SET dissolved_at=now()`)
	check("owner", "group", "owner")
	exec(`UPDATE im_users SET deleted_at=now() WHERE id='friend'`)
	check("friend", "group")
}
