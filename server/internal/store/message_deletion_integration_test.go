package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMessageDeletionPostgres(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := t.Context()
	schema := fmt.Sprintf("deletion_%d", time.Now().UnixNano())
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
	if schemaVersion != 63 {
		t.Fatal("version", schemaVersion)
	}
	if err = p.migrate(ctx); err != nil {
		t.Fatal("repeat migration", err)
	}
	for _, invalidID := range []string{"", "local-draft", "9999999999999999999", "-1"} {
		var deleted bool
		if err = p.pool.QueryRow(ctx, `SELECT im_message_is_deleted($1)`, invalidID).Scan(&deleted); err != nil || deleted {
			t.Fatal("invalid message ID deletion lookup", invalidID, deleted, err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := p.pool.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	for _, uid := range []string{"owner", "admin", "member", "peer", "outside"} {
		exec(`INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$1,$1,now())`, uid)
	}
	for _, cid := range []string{"direct", "other"} {
		exec(`INSERT INTO im_conversations(id,kind,title,created_at,updated_at) VALUES($1,'direct',$1,now(),now())`, cid)
		for _, uid := range []string{"owner", "peer"} {
			exec(`INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'member',now())`, cid, uid)
		}
	}
	if _, err = p.CreateGroupRecord(ctx, "group", "owner", "Delete test", []string{"admin", "member"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE im_members SET role='admin' WHERE conversation_id='group' AND user_id='admin'`)
	add := func(id int64, cid, sender string, seq int64, typ int) {
		t.Helper()
		ct := 1
		ch := "peer"
		if cid == "group" {
			ct = 2
			ch = cid
		}
		exec(`INSERT INTO im_wukong_message_index(message_id,client_msg_no,conversation_id,sender_id,channel_id,channel_type,message_seq,content_type,payload_sha256,message_timestamp) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'hash',now()-interval '30 days')`, id, fmt.Sprint(id), cid, sender, ch, ct, seq, typ)
	}
	for _, r := range []struct {
		id       int64
		cid, uid string
		seq      int64
		typ      int
	}{{101, "direct", "owner", 1, 1}, {102, "direct", "peer", 2, 1}, {103, "direct", "owner", 3, 5}, {104, "other", "owner", 1, 1}, {201, "group", "owner", 1, 1}, {202, "group", "member", 2, 1}, {203, "group", "admin", 3, 1}, {204, "group", "member", 4, 1002}, {205, "group", "member", 5, 1}, {206, "group", "member", 6, 1}} {
		add(r.id, r.cid, r.uid, r.seq, r.typ)
	}
	deny := func(uid, cid string, ids ...string) {
		t.Helper()
		if _, e := p.DeleteMessagesForEveryone(ctx, uid, cid, ids, "127.0.0.1"); e == nil {
			t.Fatalf("allowed %s %s %v", uid, cid, ids)
		}
	}
	for _, uid := range []string{"owner", "admin", "member"} {
		if ok, e := p.MessageDeletionPermission(ctx, uid); e != nil || ok {
			t.Fatal("default", uid, ok, e)
		}
		deny(uid, "group", "201")
	}
	for _, uid := range []string{"owner", "admin", "member", "outside"} {
		if e := p.SetMessageDeletionPermission(ctx, "admin", uid, true, "test grant", "127.0.0.1"); e != nil {
			t.Fatal(e)
		}
	}
	deny("member", "group", "202")
	deny("outside", "group", "201")
	deny("owner", "group", "204")
	deny("owner", "direct", "101", "104")
	deny("owner", "direct", "101", "999")
	var deleted bool
	p.pool.QueryRow(ctx, `SELECT im_message_is_deleted('101')`).Scan(&deleted)
	if deleted {
		t.Fatal("partial commit")
	}
	first, e := p.DeleteMessagesForEveryone(ctx, "owner", "direct", []string{"101", "102"}, "127.0.0.1")
	if e != nil || len(first.MessageIDs) != 2 || first.Version <= 0 {
		t.Fatal("direct old messages", first, e)
	}
	again, e := p.DeleteMessagesForEveryone(ctx, "owner", "direct", []string{"101", "102"}, "")
	if e != nil || again.Version != first.Version {
		t.Fatal("idempotency", again, e)
	}
	for _, r := range []struct{ uid, id string }{{"admin", "201"}, {"owner", "203"}, {"admin", "202"}} {
		if _, e = p.DeleteMessagesForEveryone(ctx, r.uid, "group", []string{r.id}, ""); e != nil {
			t.Fatal("group manager", r, e)
		}
	}
	exec(`UPDATE im_members SET history_after_seq=6 WHERE conversation_id='group' AND user_id='admin'`)
	deny("admin", "group", "205")
	exec(`UPDATE im_members SET role='member' WHERE conversation_id='group' AND user_id='admin'`)
	deny("admin", "group", "206")
	exec(`UPDATE im_wukong_message_index SET expires_at=now()-interval '1 minute' WHERE message_id=205`)
	deny("owner", "group", "205")
	if e = p.SetMessageDeletionPermission(ctx, "admin", "owner", false, "test revoke", ""); e != nil {
		t.Fatal(e)
	}
	deny("owner", "direct", "103")
	if e = p.SetMessageDeletionPermission(ctx, "admin", "owner", true, "test grant", ""); e != nil {
		t.Fatal(e)
	}
	// Grant -> revoke -> grant must produce three distinct refresh events.
	// Repeating the current value does not create another event.
	if e = p.SetMessageDeletionPermission(ctx, "admin", "owner", true, "unchanged", ""); e != nil {
		t.Fatal(e)
	}
	var permissionEvents int
	if e = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox WHERE payload->>'event'='user.message_permissions.updated' AND payload->'param'->'payload'->>'userId'='owner'`).Scan(&permissionEvents); e != nil || permissionEvents != 3 {
		t.Fatal("permission transition notifications", permissionEvents, e)
	}
	for _, id := range []string{"video", "cover"} {
		mime := "video/mp4"
		if id == "cover" {
			mime = "image/jpeg"
		}
		if e = p.CreateMedia(ctx, Media{ID: id, OwnerID: "owner", MIME: mime, Status: "ready", Size: 12, ObjectKey: id}); e != nil {
			t.Fatal(e)
		}
	}
	exec(`UPDATE im_media SET cover_media_id='cover' WHERE id='video'`)
	exec(`UPDATE im_wukong_message_index SET media_id='video' WHERE message_id=103`)
	if e = p.BindMediaChannel(ctx, MediaChannelBinding{MediaID: "video", ChannelID: "peer", ChannelType: 1, SenderID: "owner"}); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"video", "cover"} {
		if ok, e := p.CanAccessMedia(ctx, "peer", id); e != nil || !ok {
			t.Fatal("live attachment", id, ok, e)
		}
	}
	if _, e = p.DeleteMessagesForEveryone(ctx, "owner", "direct", []string{"103"}, ""); e != nil {
		t.Fatal(e)
	}
	for _, uid := range []string{"owner", "peer"} {
		for _, id := range []string{"video", "cover"} {
			if ok, e := p.CanAccessMedia(ctx, uid, id); e != nil || ok {
				t.Fatal("deleted attachment", uid, id, ok, e)
			}
		}
	}
	// A separately forwarded, independently indexed copy retains its media.
	add(105, "other", "owner", 2, 5)
	exec(`UPDATE im_wukong_message_index SET media_id='video' WHERE message_id=105`)
	if ok, e := p.CanAccessMedia(ctx, "peer", "cover"); e != nil || !ok {
		t.Fatal("independent copy", ok, e)
	}
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, _, e = loadWukongMutationMeta(ctx, tx, "owner", "101")
	tx.Rollback(ctx)
	if !errors.Is(e, ErrForbidden) {
		t.Fatal("mutation after delete", e)
	}
	if ok, e := p.CanPresentPush(ctx, OutboxItem{UserID: "peer", Payload: map[string]any{"message": map[string]any{"id": "101", "conversationId": "direct"}}}); e != nil || ok {
		t.Fatal("queued push", ok, e)
	}
	extras, e := p.LoadWukongMessageExtensions(ctx, "peer", []string{"101"})
	if e != nil || extras["101"]["deletedForEveryoneAt"] == nil || extras["101"]["recalledAt"] != nil {
		t.Fatal("independent tombstone", extras, e)
	}
	var audits int
	if e = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE action='messages.deleted_for_everyone'`).Scan(&audits); e != nil || audits != 5 {
		t.Fatal("audit/idempotency", audits, e)
	}
	if count, err := p.DeletedUnreadCount(ctx, "peer", "owner", 1, 0, 3); err != nil || count != 2 {
		t.Fatal("deleted unread excludes own sends", count, err)
	}
	// A request already in flight must observe a committed permission/role
	// change after waiting for the same row lock, not its original snapshot.
	for _, change := range []struct{ uid, cid, id, sql string }{
		{"owner", "direct", "101", `UPDATE im_users SET can_delete_messages_for_everyone=false WHERE id='owner'`},
		{"admin", "group", "206", `UPDATE im_members SET role='member' WHERE user_id='admin' AND conversation_id='group'`},
	} {
		exec(`UPDATE im_members SET role='admin',history_after_seq=0 WHERE user_id='admin' AND conversation_id='group'`)
		locked, err := p.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = locked.Exec(ctx, change.sql); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			_, err := p.DeleteMessagesForEveryone(requestCtx, change.uid, change.cid, []string{change.id}, "")
			result <- err
		}()
		select {
		case err := <-result:
			locked.Rollback(ctx)
			t.Fatalf("authorization did not serialize: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
		if err = locked.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err = <-result; !errors.Is(err, ErrForbidden) {
			t.Fatal("stale authorization", err)
		}
	}
	if err = p.SetMessageDeletionPermission(ctx, "admin", "owner", true, "batch test", ""); err != nil {
		t.Fatal(err)
	}
	batch := make([]string, 100)
	for i := range batch {
		id := int64(1000 + i)
		add(id, "direct", "peer", id, 1)
		batch[i] = fmt.Sprint(id)
	}
	deny("owner", "direct", append(batch, "2000")...)
	deny("owner", "direct")
	results := make(chan MessageDeletionResult, 2)
	errorsCh := make(chan error, 2)
	for range 2 {
		go func() {
			result, err := p.DeleteMessagesForEveryone(ctx, "owner", "direct", batch, "")
			results <- result
			errorsCh <- err
		}()
	}
	a, b := <-results, <-results
	if err = <-errorsCh; err != nil {
		t.Fatal(err)
	}
	if err = <-errorsCh; err != nil {
		t.Fatal(err)
	}
	if len(a.MessageIDs) != 100 || a.Version != b.Version {
		t.Fatal("concurrent batch idempotency", a.Version, b.Version)
	}
}
