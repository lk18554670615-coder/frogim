package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgresTypingRecipients(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("isolated PostgreSQL required (IM_TEST_DATABASE_URL)")
	}
	ctx := t.Context()
	schema := fmt.Sprintf("typing_%d", time.Now().UnixNano())
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
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := p.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	users := []string{"sender", "owner", "admin", "internal", "ordinary", "banned", "deleted", "expired", "outsider"}
	for i, id := range users {
		exec(`INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$2,$1,now(),now())`, id, fmt.Sprintf("138%08d", i))
	}
	if _, err := p.CreateGroupRecord(ctx, "group", "owner", "Typing test", []string{"sender", "admin", "internal", "ordinary", "banned", "deleted", "expired"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE im_users SET is_internal_user=true WHERE id IN ('internal','banned','deleted','expired','outsider')`)
	exec(`UPDATE im_members SET role='admin' WHERE user_id='admin'`)
	exec(`UPDATE im_users SET banned=true WHERE id='banned'`)
	exec(`UPDATE im_users SET deleted_at=now() WHERE id='deleted'`)
	exec(`UPDATE im_members SET expires_at=now()-interval '1 second' WHERE user_id='expired'`)
	check := func(actor, cid string, want ...string) {
		t.Helper()
		got, err := p.TypingRecipients(ctx, actor, cid)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("actor=%s cid=%s recipients=%v want=%v err=%v", actor, cid, got, want, err)
		}
	}
	check("sender", "group", "admin", "internal", "owner")
	check("owner", "group", "admin", "internal")
	for _, actor := range []string{"outsider", "banned", "deleted", "expired", "missing"} {
		if got, err := p.TypingRecipients(ctx, actor, "group"); !errors.Is(err, ErrForbidden) || len(got) != 0 {
			t.Fatalf("ineligible sender %s got=%v err=%v", actor, got, err)
		}
	}
	exec(`UPDATE im_users SET is_internal_user=false WHERE id='internal'`)
	check("sender", "group", "admin", "owner")
	exec(`UPDATE im_members SET role='member' WHERE user_id='admin'`)
	check("sender", "group", "owner")
	exec(`UPDATE im_members SET role='admin' WHERE user_id='ordinary'`)
	check("sender", "group", "ordinary", "owner")
	exec(`UPDATE im_users SET is_internal_user=true WHERE id='ordinary'`)
	exec(`UPDATE im_members SET role='member' WHERE user_id='ordinary'`)
	check("sender", "group", "ordinary", "owner")
	// Revoking internal status does not take away an independent manager grant.
	exec(`UPDATE im_members SET role='admin' WHERE user_id='ordinary'`)
	exec(`UPDATE im_users SET is_internal_user=false WHERE id='ordinary'`)
	check("sender", "group", "ordinary", "owner")
	exec(`DELETE FROM im_members WHERE user_id='ordinary'`)
	check("sender", "group", "owner")
	exec(`UPDATE im_members SET role='member' WHERE user_id='owner'`)
	exec(`UPDATE im_members SET role='owner' WHERE user_id='admin'`)
	check("sender", "group", "admin")
	exec(`UPDATE im_users SET banned=true WHERE id='admin'`)
	check("sender", "group")
	exec(`UPDATE im_users SET banned=false WHERE id='admin'`)
	check("sender", "group", "admin")
	// Direct and business conversations retain the previous recipient rules.
	for _, kind := range []string{"direct", "service"} {
		exec(`INSERT INTO im_conversations(id,kind,created_at,updated_at) VALUES($1,$1,now(),now())`, kind)
		exec(`INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,'sender','member',now()),($1,'ordinary','member',now())`, kind)
		check("sender", kind, "ordinary")
	}
	// Group size is measured before removing unprivileged/unavailable viewers.
	exec(`INSERT INTO im_users(id,phone,name,created_at,updated_at)
		SELECT 'bulk-'||i,'bulk-phone-'||i,'Member',now(),now() FROM generate_series(1,493) i`)
	exec(`INSERT INTO im_members(conversation_id,user_id,role,joined_at)
		SELECT 'group','bulk-'||i,'member',now() FROM generate_series(1,493) i`)
	check("sender", "group", "admin") // 7 existing members + 493 = 500.
	exec(`INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES('group','outsider','member',now())`)
	check("sender", "group")
	exec(`DELETE FROM im_members WHERE conversation_id='group' AND user_id='outsider'`)
	exec(`UPDATE im_groups SET dissolved_at=now() WHERE conversation_id='group'`)
	if got, err := p.TypingRecipients(ctx, "sender", "group"); !errors.Is(err, ErrForbidden) || len(got) != 0 {
		t.Fatalf("dissolved group recipients=%v err=%v", got, err)
	}
	if got, err := p.TypingRecipients(ctx, "sender", "missing"); !errors.Is(err, ErrForbidden) || len(got) != 0 {
		t.Fatalf("missing group recipients=%v err=%v", got, err)
	}
	// A failed permission query must return an error, never an unfiltered list.
	exec(`ALTER TABLE im_users RENAME COLUMN is_internal_user TO unavailable_permission`)
	if got, err := p.TypingRecipients(ctx, "sender", "group"); err == nil || len(got) != 0 {
		t.Fatalf("unavailable permission recipients=%v err=%v", got, err)
	}
}
