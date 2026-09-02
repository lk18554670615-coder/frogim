package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/wukong"
)

func TestPostgresGroupHistoryWithRealWuKong(t *testing.T) {
	databaseURL, apiURL := os.Getenv("IM_TEST_DATABASE_URL"), os.Getenv("IM_TEST_WUKONG_PATCH_URL")
	if databaseURL == "" || apiURL == "" {
		t.Skip("isolated PostgreSQL and pinned WuKongIM required")
	}
	ctx := t.Context()
	schema := fmt.Sprintf("group_history_%d", time.Now().UnixNano())
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	if _, err = connection.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, err := connection.Exec(context.Background(), `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
		if err != nil {
			t.Error(err)
		}
	}()
	separator := "?"
	if strings.Contains(databaseURL, "?") {
		separator = "&"
	}
	p, err := NewPostgres(ctx, databaseURL+separator+"search_path="+schema+",public")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	client, err := wukong.NewClient(wukong.Config{APIURL: apiURL, ManagerURL: apiURL, ManagerToken: os.Getenv("IM_TEST_WUKONG_MANAGER_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	reader := func(ctx context.Context, uid, cid string) (uint64, error) {
		return client.ChannelMaxMessageSeq(ctx, uid, cid, 2)
	}
	p.SetGroupHistoryBoundaryReader(reader)
	owner, admin, member, qrMember, inviteMember, legacy := schema+"_owner", schema+"_admin", schema+"_member", schema+"_qr", schema+"_invite", schema+"_legacy"
	for i, uid := range []string{owner, admin, member, qrMember, inviteMember, legacy} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$2,'History test',now(),now())`, uid, fmt.Sprintf("138%08d", i)); err != nil {
			t.Fatal(err)
		}
	}
	// Reconstruct a schema-57 group in this disposable schema, then run the real
	// startup migration. Existing membership timestamps must survive unchanged.
	oldCID := schema + "_legacy_group"
	oldJoin := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if _, err = p.CreateGroupRecord(ctx, oldCID, owner, "Legacy group", nil, oldJoin); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `DROP FUNCTION im_can_read_group_message(text,text,bigint,timestamptz);
ALTER TABLE im_groups DROP COLUMN history_visible_to_new_members, DROP COLUMN history_policy_version;
ALTER TABLE im_members DROP COLUMN history_after_seq;
DELETE FROM im_schema_migrations WHERE version>=58;
INSERT INTO im_schema_migrations(version) VALUES(57) ON CONFLICT DO NOTHING;`); err != nil {
		t.Fatal(err)
	}
	if err = p.migrate(ctx); err != nil {
		t.Fatal("upgrade schema 57", err)
	}
	if err = p.migrate(ctx); err != nil {
		t.Fatal("repeat startup migration", err)
	}
	oldProfile, e := p.GetGroupProfile(ctx, owner, oldCID)
	if e != nil || oldProfile.HistoryVisibleToNewMembers || oldProfile.HistoryAccess.AfterSeq != nil || oldProfile.HistoryAccess.AfterTimestamp == nil || *oldProfile.HistoryAccess.AfterTimestamp != oldJoin.Unix() {
		t.Fatalf("legacy migration=%+v %v", oldProfile, e)
	}
	var actualVersion int
	if err = p.pool.QueryRow(ctx, `SELECT max(version) FROM im_schema_migrations`).Scan(&actualVersion); err != nil || actualVersion != schemaVersion {
		t.Fatalf("schema version=%d %v", actualVersion, err)
	}
	cid := schema + "_group"
	now := time.Now().UTC()
	if _, err = p.CreateGroupRecord(ctx, cid, owner, "History test", []string{admin}, now); err != nil {
		t.Fatal(err)
	}
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: owner, ConversationID: cid, TargetID: admin, Action: "role", Role: "admin", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err = client.UpsertChannel(ctx, wukong.ChannelRequest{ChannelID: cid, ChannelType: 2, Subscribers: []string{owner, admin}, Reset: 1}); err != nil {
		t.Fatal(err)
	}
	profile, err := p.GetGroupProfile(ctx, owner, cid)
	if err != nil || profile.HistoryVisibleToNewMembers || profile.HistoryAccess.AfterSeq == nil || *profile.HistoryAccess.AfterSeq != 0 {
		t.Fatalf("initial profile=%+v err=%v", profile, err)
	}
	if _, err = p.pool.Exec(ctx, normalizedSchema); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	// Send to the real pinned engine, then feed its actual wire fields through
	// the production webhook indexer. Neither index nor completed hook stores body.
	send := func(text string) wukongMessageNotification {
		t.Helper()
		result, e := client.SendStoredMessage(ctx, wukong.StoredMessageRequest{ClientMsgNo: schema + "_" + text, FromUID: owner, ChannelID: cid, ChannelType: 2, Payload: map[string]any{"type": 1, "content": text}})
		if e != nil {
			t.Fatal(e)
		}
		var raw wukong.SyncedMessage
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			items, e := client.SearchMessages(ctx, wukong.MessageSearchRequest{LoginUID: owner, ChannelID: cid, ChannelType: 2, MessageIDs: []int64{result.MessageID}})
			if e != nil {
				t.Fatal(e)
			}
			if len(items) > 0 {
				raw = items[0]
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if raw == nil {
			t.Fatal("WuKong message not persisted")
		}
		payload, e := json.Marshal(raw["payload"])
		if e != nil {
			t.Fatal(e)
		}
		raw["payload"] = payload
		raw["message_id"] = result.MessageID
		encoded, e := json.Marshal(raw)
		if e != nil {
			t.Fatal(e)
		}
		var message wukongMessageNotification
		if e = json.Unmarshal(encoded, &message); e != nil {
			t.Fatal(e)
		}
		event := wukong.WebhookEvent{ID: schema + "_hook_" + text, EventType: wukong.EventMessageNotify, Payload: encoded, ReceivedAt: time.Now()}
		if _, e = p.PutWukongWebhookEvent(ctx, event); e != nil {
			t.Fatal(e)
		}
		if duplicate, e := p.PutWukongWebhookEvent(ctx, event); e != nil || duplicate {
			t.Fatalf("webhook dedup=%v err=%v", duplicate, e)
		}
		return message
	}
	before := send("before-join")
	// The authoritative max-sequence snapshot is the linearization point for
	// joining. A concurrent send after that snapshot belongs to visible history.
	snapshotRead, releaseJoin := make(chan struct{}), make(chan struct{})
	p.SetGroupHistoryBoundaryReader(func(ctx context.Context, uid, cid string) (uint64, error) {
		seq, e := reader(ctx, uid, cid)
		close(snapshotRead)
		select {
		case <-releaseJoin:
			return seq, e
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
	joined := make(chan error, 1)
	go func() { joined <- p.AddGroupMembers(ctx, owner, cid, []string{member}, time.Now()) }()
	select {
	case <-snapshotRead:
	case <-time.After(5 * time.Second):
		t.Fatal("join did not request max sequence")
	}
	after := send("after-join")
	close(releaseJoin)
	if err = <-joined; err != nil {
		t.Fatal(err)
	}
	p.SetGroupHistoryBoundaryReader(reader)
	h, err := p.GroupHistoryAccess(ctx, member, cid)
	if err != nil || h.AfterSeq == nil || uint64(*h.AfterSeq) != before.MessageSeq {
		t.Fatalf("join cutoff=%+v err=%v", h, err)
	}
	if h.Allows(int64(before.MessageSeq), time.Unix(before.Timestamp, 0)) || !h.Allows(int64(after.MessageSeq), time.Unix(after.Timestamp, 0)) {
		t.Fatal("real messages crossed cutoff")
	}
	beforeID := strconv.FormatInt(before.MessageID, 10)
	afterID := strconv.FormatInt(after.MessageID, 10)
	mediaID := schema + "_attachment"
	if err = p.CreateMedia(ctx, Media{ID: mediaID, OwnerID: owner, ObjectKey: mediaID, MIME: "image/png", Size: 10, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if err = p.BindMediaChannel(ctx, MediaChannelBinding{MediaID: mediaID, ChannelID: cid, ChannelType: 2, SenderID: owner}); err != nil {
		t.Fatal(err)
	}
	// Model the attachment index associated with the pre-join stored message.
	if _, err = p.pool.Exec(ctx, `UPDATE im_wukong_message_index SET media_id=$2 WHERE message_id=$1`, before.MessageID, mediaID); err != nil {
		t.Fatal(err)
	}
	if allowed, e := p.CanAccessMedia(ctx, member, mediaID); e != nil || allowed {
		t.Fatalf("hidden attachment=%v %v", allowed, e)
	}
	if _, err = p.ListWukongForwardMessageRefs(ctx, member, []string{beforeID}); err != ErrForbidden {
		t.Fatalf("forward hidden=%v", err)
	}
	if _, err = p.ListWukongForwardMessageRefs(ctx, member, []string{afterID}); err != nil {
		t.Fatal(err)
	}
	on, off := true, false
	if _, err = p.UpdateGroupProfile(ctx, member, cid, GroupProfileUpdate{HistoryVisibleToNewMembers: &on}, time.Now()); err != ErrForbidden {
		t.Fatalf("member write=%v", err)
	}
	if _, err = p.UpdateGroupProfile(ctx, admin, cid, GroupProfileUpdate{HistoryVisibleToNewMembers: &on}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = p.SetFavorite(ctx, member, beforeID, true); err != nil {
		t.Fatal(err)
	}
	if allowed, e := p.CanAccessMedia(ctx, member, mediaID); e != nil || !allowed {
		t.Fatalf("opened attachment=%v %v", allowed, e)
	}
	if _, _, err = p.SetGroupMessagePin(ctx, owner, cid, beforeID, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = p.UpdateGroupProfile(ctx, owner, cid, GroupProfileUpdate{HistoryVisibleToNewMembers: &off}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: owner, ConversationID: cid, TargetID: member, Action: "role", Role: "admin", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ListWukongForwardMessageRefs(ctx, member, []string{beforeID}); err != ErrForbidden {
		t.Fatalf("promotion/favorite bypass=%v", err)
	}
	if items, err := p.ListFavorites(ctx, member, 50); err != nil || len(items) != 0 {
		t.Fatalf("hidden favorite=%v %v", items, err)
	}
	if items, err := p.ListGroupMessagePins(ctx, member, cid, 0, 50); err != nil || len(items) != 0 {
		t.Fatalf("hidden pins=%v %v", items, err)
	}
	if items, err := p.LoadWukongMessageExtensions(ctx, member, []string{beforeID}); err != nil || len(items) != 0 {
		t.Fatalf("hidden extensions=%v %v", items, err)
	}
	if allowed, e := p.CanAccessMedia(ctx, member, mediaID); e != nil || allowed {
		t.Fatalf("closed attachment=%v %v", allowed, e)
	}
	if _, e := p.ListMessageEdits(ctx, member, beforeID); e != ErrForbidden {
		t.Fatalf("hidden edit history=%v", e)
	}
	if extras, e := p.SyncWukongMessageExtras(ctx, member, cid, 2, 0, 100); e != nil || len(extras) != 0 {
		t.Fatalf("hidden SDK extras=%+v %v", extras, e)
	}
	if members, _, e := p.ListConversationMembersPage(ctx, member, cid, "", 100); e != nil || len(members) != 3 {
		t.Fatalf("members=%v %v", members, e)
	}
	if items, err := p.LoadAdminGroupMessageExtensions(ctx, cid, []string{beforeID}); err != nil || len(items) != 1 {
		t.Fatalf("admin audit history=%v %v", items, err)
	}
	if items, err := p.ListConversations(ctx, member, 100); err != nil || len(items) != 1 || items[0]["historyAccess"] == nil {
		t.Fatalf("conversation policy=%v %v", items, err)
	}
	if _, err = p.AdminGroupOverview(ctx, cid); err != nil {
		t.Fatal(err)
	}
	// A failure after the setting write but before commit must roll back policy,
	// audit and durable notifications together.
	if _, err = p.pool.Exec(ctx, `CREATE FUNCTION reject_history_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected outbox failure'; END $$;
CREATE TRIGGER fail_history_outbox BEFORE INSERT ON im_wukong_outbox FOR EACH ROW EXECUTE FUNCTION reject_history_outbox()`); err != nil {
		t.Fatal(err)
	}
	if e := p.SetAdminGroupHistoryVisibility(ctx, "test-admin", cid, true, "rollback test", time.Now()); e == nil {
		t.Fatal("outbox failure accepted")
	}
	if _, err = p.pool.Exec(ctx, `DROP TRIGGER fail_history_outbox ON im_wukong_outbox; DROP FUNCTION reject_history_outbox()`); err != nil {
		t.Fatal(err)
	}
	rolledBack, e := p.GroupHistoryAccess(ctx, member, cid)
	if e != nil || rolledBack.VisibleAll || rolledBack.Version != 3 {
		t.Fatalf("rolled back policy=%+v %v", rolledBack, e)
	}
	if err = p.AddGroupMembers(ctx, owner, cid, []string{member}, time.Now()); err != nil {
		t.Fatal(err)
	}
	h, _ = p.GroupHistoryAccess(ctx, member, cid)
	if uint64(*h.AfterSeq) != before.MessageSeq {
		t.Fatal("duplicate join replaced cutoff")
	}
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: member, ConversationID: cid, TargetID: member, Action: "leave", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err = p.AddGroupMembers(ctx, owner, cid, []string{member}, time.Now()); err != nil {
		t.Fatal(err)
	}
	h, _ = p.GroupHistoryAccess(ctx, member, cid)
	if uint64(*h.AfterSeq) != after.MessageSeq {
		t.Fatal("rejoin failed to reset cutoff")
	}
	// Missing WuKong cannot create a zero-boundary membership or invitation accept.
	p.SetGroupHistoryBoundaryReader(func(context.Context, string, string) (uint64, error) { return 0, errors.New("offline") })
	if err = p.AddGroupMembers(ctx, owner, cid, []string{qrMember}, time.Now()); err != ErrUnsupported {
		t.Fatalf("offline=%v", err)
	}
	var count int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_members WHERE conversation_id=$1 AND user_id=$2`, cid, qrMember).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback count=%d err=%v", count, err)
	}
	p.SetGroupHistoryBoundaryReader(reader)
	qr := "qr"
	profile, err = p.UpdateGroupProfile(ctx, owner, cid, GroupProfileUpdate{JoinPolicy: &qr, RotateQR: true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p.SetGroupHistoryBoundaryReader(func(context.Context, string, string) (uint64, error) { return 0, errors.New("offline") })
	if e := p.JoinGroupByQR(ctx, qrMember, profile.QRToken, time.Now()); e != ErrUnsupported {
		t.Fatalf("QR offline=%v", e)
	}
	p.SetGroupHistoryBoundaryReader(reader)
	if err = p.JoinGroupByQR(ctx, qrMember, profile.QRToken, time.Now()); err != nil {
		t.Fatal(err)
	}
	invite := &model.GroupInvite{ID: schema + "_inv", ConversationID: cid, InviterID: owner, InviteeID: inviteMember, Source: "invite", Status: "pending", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if _, _, err = p.CreateGroupInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	p.SetGroupHistoryBoundaryReader(func(context.Context, string, string) (uint64, error) { return 0, errors.New("offline") })
	if _, _, e := p.TransitionGroupInvite(ctx, invite.ID, inviteMember, "accept", time.Now()); e != ErrUnsupported {
		t.Fatalf("invite offline=%v", e)
	}
	p.SetGroupHistoryBoundaryReader(reader)
	if _, _, err = p.TransitionGroupInvite(ctx, invite.ID, inviteMember, "accept", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{qrMember, inviteMember} {
		h, e := p.GroupHistoryAccess(ctx, uid, cid)
		if e != nil || h.AfterSeq == nil || uint64(*h.AfterSeq) != after.MessageSeq {
			t.Fatalf("join path %s %+v %v", uid, h, e)
		}
	}
	// Legacy rows preserve joined_at and hide the entire ambiguous second.
	legacyJoined := time.Unix(after.Timestamp, 500000000)
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'admin',$3)`, cid, legacy, legacyJoined); err != nil {
		t.Fatal(err)
	}
	h, err = p.GroupHistoryAccess(ctx, legacy, cid)
	if err != nil || h.AfterSeq != nil || h.Allows(int64(after.MessageSeq), time.Unix(after.Timestamp, 999999999)) {
		t.Fatalf("legacy=%+v %v", h, err)
	}
	var sqlVisible bool
	if err = p.pool.QueryRow(ctx, `SELECT im_can_read_group_message($1,$2,$3,$4)`, legacy, cid, int64(after.MessageSeq), time.Unix(after.Timestamp, 999999999)).Scan(&sqlVisible); err != nil || sqlVisible {
		t.Fatal("legacy SQL same-second leak", err)
	}
	if err = p.SetAdminGroupHistoryVisibility(ctx, "test-admin", cid, true, "contract validation", time.Now()); err != nil {
		t.Fatal(err)
	}
	opened, _ := p.GetGroupProfile(ctx, member, cid)
	if err = p.SetAdminGroupHistoryVisibility(ctx, "test-admin", cid, true, "duplicate retry", time.Now()); err != nil {
		t.Fatal(err)
	}
	repeated, _ := p.GetGroupProfile(ctx, member, cid)
	if opened.HistoryPolicyVersion != repeated.HistoryPolicyVersion {
		t.Fatal("duplicate notification changed version")
	}
	if _, err = p.ListWukongForwardMessageRefs(ctx, member, []string{beforeID}); err != nil {
		t.Fatal("reopen failed", err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE action='group.history.updated' AND target_id=$1`, cid).Scan(&count); err != nil || count != 3 {
		t.Fatalf("policy audit count=%d err=%v", count, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_webhook_events WHERE payload<>'{}'::jsonb`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("webhook retained message bodies: %d %v", count, err)
	}
	t.Logf("real WuKong seq=%d/%d, joins, permissions, reopen, legacy, projections, audit and webhook idempotency passed", before.MessageSeq, after.MessageSeq)
}
