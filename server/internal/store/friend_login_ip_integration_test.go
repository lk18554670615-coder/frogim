package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestFriendLoginIPPermissionPostgres(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := t.Context()
	schema := fmt.Sprintf("friend_login_ip_%d", time.Now().UnixNano())
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
	p, err := NewPostgres(ctx, url+separator+"search_path="+schema+",public")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := p.pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	for _, uid := range []string{"viewer", "peer", "other"} {
		exec(`INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$1,$1,now())`, uid)
	}
	exec(`INSERT INTO im_conversations(id,kind,title,created_at,updated_at) VALUES('direct','direct','',now(),now())`)
	exec(`INSERT INTO im_direct_index(pair_key,conversation_id) VALUES('peer:viewer','direct')`)
	for _, uid := range []string{"viewer", "peer"} {
		exec(`INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES('direct',$1,'member',now())`, uid)
	}
	exec(`INSERT INTO im_friendships(user_id,friend_user_id) VALUES('viewer','peer'),('peer','viewer')`)
	exec(`INSERT INTO im_user_access_profiles(user_id,last_login_ip,last_login_at,last_login_event_id) VALUES('peer','2001:4860:4860::8888',now(),'login')`)

	allowed, err := p.FriendLoginIPPermission(ctx, "viewer")
	if err != nil || allowed {
		t.Fatalf("permission must default off: allowed=%v err=%v", allowed, err)
	}
	if _, _, err = p.ReadFriendLoginIP(ctx, "viewer", "direct", "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ungranted viewer must be denied: %v", err)
	}
	result, err := p.SetFriendLoginIPPermission(ctx, "operator", []string{"viewer", "peer"}, true, "support case", "127.0.0.1")
	if err != nil || result.Changed != 2 || result.Unchanged != 0 || result.Requested != 2 {
		t.Fatalf("grant failed: result=%+v err=%v", result, err)
	}
	granted, total, _, err := p.ListAdminUsersByIP(ctx, "", "", "", 20, "", "any", "allowed")
	if err != nil || total != 2 || len(granted) != 2 || !granted[0].CanViewFriendLoginIP || !granted[1].CanViewFriendLoginIP {
		t.Fatalf("allowed admin filter failed: total=%d users=%+v err=%v", total, granted, err)
	}
	denied, total, _, err := p.ListAdminUsersByIP(ctx, "", "", "", 20, "", "any", "denied")
	if err != nil || total != 1 || len(denied) != 1 || denied[0].ID != "other" || denied[0].CanViewFriendLoginIP {
		t.Fatalf("denied admin filter failed: total=%d users=%+v err=%v", total, denied, err)
	}
	repeated, err := p.SetFriendLoginIPPermission(ctx, "operator", []string{"viewer"}, true, "idempotent review", "")
	if err != nil || repeated.Changed != 0 || repeated.Unchanged != 1 {
		t.Fatalf("repeated grant must be an audited no-op: result=%+v err=%v", repeated, err)
	}
	var permissionAudits, permissionEvents int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE action='user.friend_login_ip_permission.updated' AND target_id='viewer'`).Scan(&permissionAudits); err != nil || permissionAudits != 2 {
		t.Fatalf("initial and no-op permission decisions must both be audited: count=%d err=%v", permissionAudits, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox WHERE payload->>'event'='user.friend_login_ip_permission.updated'`).Scan(&permissionEvents); err != nil || permissionEvents != 2 {
		t.Fatalf("only real permission transitions should notify clients: count=%d err=%v", permissionEvents, err)
	}
	peerID, loginIP, err := p.ReadFriendLoginIP(ctx, "viewer", "direct", "203.0.113.9")
	if err != nil || peerID != "peer" || loginIP != "2001:4860:4860::8888" {
		t.Fatalf("authorized read failed: peer=%q ip=%q err=%v", peerID, loginIP, err)
	}
	if _, _, err = p.ReadFriendLoginIP(ctx, "viewer", "direct", "203.0.113.9"); err != nil {
		t.Fatal("idempotent hourly audit read", err)
	}
	var viewed int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE action='user.friend_login_ip.viewed' AND actor_id='viewer' AND target_id='peer'`).Scan(&viewed); err != nil || viewed != 1 {
		t.Fatalf("view audit must be de-duplicated per hour: count=%d err=%v", viewed, err)
	}
	var metadata string
	if err = p.pool.QueryRow(ctx, `SELECT metadata::text FROM im_audits WHERE action='user.friend_login_ip.viewed' AND actor_id='viewer'`).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(metadata, loginIP) || !strings.Contains(metadata, "conversationId") {
		t.Fatalf("audit must identify the conversation without copying the target IP: %s", metadata)
	}

	exec(`DELETE FROM im_friendships WHERE user_id='peer' AND friend_user_id='viewer'`)
	if _, _, err = p.ReadFriendLoginIP(ctx, "viewer", "direct", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("one-sided/deleted friendship must hide the peer: %v", err)
	}
	exec(`INSERT INTO im_friendships(user_id,friend_user_id) VALUES('peer','viewer')`)
	exec(`UPDATE im_users SET banned=true WHERE id='viewer'`)
	if _, _, err = p.ReadFriendLoginIP(ctx, "viewer", "direct", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("banned viewer must be denied: %v", err)
	}
	exec(`UPDATE im_users SET banned=false WHERE id='viewer'`)
	if _, _, err = p.ReadFriendLoginIP(ctx, "viewer", "direct", ""); err != nil {
		t.Fatal("unban must restore the persisted grant", err)
	}

	if _, err = p.SetFriendLoginIPPermission(ctx, "operator", []string{"viewer", "missing"}, false, "invalid batch", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("batch with missing user must fail atomically: %v", err)
	}
	allowed, err = p.FriendLoginIPPermission(ctx, "viewer")
	if err != nil || !allowed {
		t.Fatalf("failed batch must not partially revoke: allowed=%v err=%v", allowed, err)
	}
}
