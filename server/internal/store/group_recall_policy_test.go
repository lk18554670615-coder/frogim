package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/linli/im/server/internal/wukong"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestGroupRecallRolesWindowAndPushPresentation(t *testing.T) {
	p := newIsolatedWukongStore(t)
	ctx := t.Context()
	at := time.Now().UTC().Truncate(time.Second)
	for _, uid := range []string{"owner", "admin", "member", "peer", "outsider"} {
		if _, err := p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$1,$1,$2,$2)`, uid, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.CreateGroupRecord(ctx, "g", "owner", "Test", []string{"admin", "member", "peer"}, at.Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.pool.Exec(ctx, `UPDATE im_members SET role='admin' WHERE user_id='admin'`); err != nil {
		t.Fatal(err)
	}
	var seq int64
	makeMessage := func(sender string, age time.Duration, contentType int, expired bool) string {
		t.Helper()
		seq++
		var expiry *time.Time
		if expired {
			v := at.Add(-time.Second)
			expiry = &v
		}
		m := insertTestWukongMessage(t, p, ctx, 700000+seq, fmt.Sprintf("client-%d", seq), "g", sender, seq, contentType, expiry, "", at.Add(-age))
		return m.ID
	}
	for _, actor := range []string{"owner", "admin", "member", "peer", "outsider"} {
		for _, sender := range []string{"owner", "admin", "member", "peer"} {
			for _, age := range []time.Duration{time.Hour, 24 * time.Hour, 24*time.Hour + time.Microsecond} {
				mid := makeMessage(sender, age, 1, false)
				_, _, _, err := p.RecallAuthorized(ctx, actor, mid, at, 2*time.Minute)
				allowed := actor != "outsider" && (actor == sender || actor == "owner" || actor == "admin") && age <= 24*time.Hour
				if (err == nil) != allowed {
					t.Fatalf("actor=%s sender=%s age=%s err=%v", actor, sender, age, err)
				}
				if allowed {
					var before, after int
					if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox`).Scan(&before); err != nil {
						t.Fatal(err)
					}
					if _, _, _, err = p.RecallAuthorized(ctx, actor, mid, at.Add(8*24*time.Hour), 2*time.Minute); err != nil {
						t.Fatal("idempotent recall", err)
					}
					if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox`).Scan(&after); err != nil {
						t.Fatal(err)
					}
					if before != after {
						t.Fatal("duplicate recall enqueued another event")
					}
				}
			}
		}
	}
	for _, tc := range []struct {
		minutes int
		age     time.Duration
		allowed bool
	}{{1, time.Minute, true}, {1, 2 * time.Minute, false}, {10080, 7 * 24 * time.Hour, true}, {10080, 7*24*time.Hour + time.Second, false}} {
		if _, err := p.pool.Exec(ctx, `INSERT INTO im_settings(key,value) VALUES('groupRecallMinutes',$1::jsonb) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprint(tc.minutes)); err != nil {
			t.Fatal(err)
		}
		_, _, _, err := p.RecallAuthorized(ctx, "admin", makeMessage("owner", tc.age, 1, false), at, 2*time.Minute)
		if (err == nil) != tc.allowed {
			t.Fatalf("custom window %+v err=%v", tc, err)
		}
	}
	for _, mid := range []string{makeMessage("member", 0, wukong.ContentTypeSystemEvent, false), makeMessage("member", time.Minute, 1, true)} {
		if _, _, _, err := p.RecallAuthorized(ctx, "owner", mid, at, 2*time.Minute); !errors.Is(err, ErrForbidden) {
			t.Fatalf("invalid recall=%v", err)
		}
	}
	mid := makeMessage("owner", time.Minute, 1, false)
	if _, err := p.pool.Exec(ctx, `UPDATE im_members SET role='member' WHERE user_id='admin'`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := p.RecallAuthorized(ctx, "admin", mid, at, 2*time.Minute); !errors.Is(err, ErrForbidden) {
		t.Fatal("demoted manager retained recall permission", err)
	}
	if _, err := p.pool.Exec(ctx, `UPDATE im_members SET role='admin',history_after_seq=$1 WHERE user_id='admin'`, seq); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := p.RecallAuthorized(ctx, "admin", mid, at, 2*time.Minute); !errors.Is(err, ErrForbidden) {
		t.Fatal("pre-join recall", err)
	}
	if _, err := p.pool.Exec(ctx, `UPDATE im_members SET history_after_seq=0 WHERE user_id='admin'`); err != nil {
		t.Fatal(err)
	}
	// A generous group setting must not extend ordinary editing or direct recall.
	if _, _, err := p.EditMessage(ctx, "owner", makeMessage("owner", 3*time.Minute, 1, false), "edit-window-test", map[string]any{"text": "edit"}, map[string]any{"text": "original"}, at, 2*time.Minute); !errors.Is(err, ErrForbidden) {
		t.Fatal("edit window changed", err)
	}
	if _, _, err := p.GetOrCreateDirectConversation(ctx, "owner", "member", "direct-test", at); err != nil {
		t.Fatal(err)
	}
	direct := insertTestWukongMessage(t, p, ctx, 800001, "direct-client", "direct-test", "owner", 1, 1, nil, "", at.Add(-3*time.Minute))
	if _, _, _, err := p.RecallAuthorized(ctx, "owner", direct.ID, at, 2*time.Minute); !errors.Is(err, ErrForbidden) {
		t.Fatal("direct window changed", err)
	}
	// Exercise the production offline webhook enqueue path, not just the last-mile gate.
	for _, event := range []string{"group.members.added", "group.announcement.updated", "screenshot.taken"} {
		contentType := wukong.ContentTypeSystemEvent
		if event == "screenshot.taken" {
			contentType = wukong.ContentTypeScreenshot
		}
		body, _ := json.Marshal(map[string]any{"type": contentType, "event": event, "schemaVersion": 1})
		hook := wukongOfflineNotification{ToUIDs: []string{"owner", "admin", "member", "peer"}, wukongMessageNotification: wukongMessageNotification{MessageID: 900001, FromUID: "owner", ChannelID: "g", ChannelType: 2, Payload: body}}
		hook.Header.RedDot = 1
		raw, _ := json.Marshal(hook)
		tx, err := p.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(ctx, `DELETE FROM im_push_outbox`); err != nil {
			t.Fatal(err)
		}
		if err = enqueueWukongOfflinePush(ctx, tx, wukong.WebhookEvent{Payload: raw}); err != nil {
			t.Fatal(err)
		}
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM im_push_outbox`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		want := 3 // Offline delivery excludes the sender.
		if IsPrivateGroupNoticeEvent(event) {
			want = 1
		}
		if count != want {
			t.Fatalf("offline %s count=%d", event, count)
		}
		if err = tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// Recheck current roles even for old screenshot pushes marked as public.
	for _, classification := range []any{nil, false, true} {
		for _, uid := range []string{"owner", "admin", "member", "peer", "outsider"} {
			message := map[string]any{"id": mid, "conversationId": "g", "type": "screenshot"}
			if classification != nil {
				message["managementOnly"] = classification
			}
			ok, err := p.CanPresentPush(ctx, OutboxItem{UserID: uid, Payload: map[string]any{"message": message}})
			if err != nil || ok != (uid == "owner" || uid == "admin") {
				t.Fatalf("screenshot push uid=%s classification=%v allowed=%v err=%v", uid, classification, ok, err)
			}
		}
	}
	for _, uid := range []string{"owner", "member"} {
		ok, err := p.CanPresentPush(ctx, OutboxItem{UserID: uid, Payload: map[string]any{"message": map[string]any{"id": direct.ID, "conversationId": "direct-test", "type": "screenshot"}}})
		if err != nil || !ok {
			t.Fatal("direct screenshot suppressed", uid, err)
		}
	}
	for _, event := range []string{"group.member.joined", "group.members.added", "group.member_added", "group.member.leave", "group.member.remove", "group.blacklist.added", "group.invite.accepted", "group.invite.rejected", "group.invite.cancelled", "group.announcement.updated", "group.member.role"} {
		for _, uid := range []string{"owner", "admin", "member", "peer", "outsider"} {
			restricted := IsPrivateGroupNoticeEvent(event)
			ok, err := p.CanPresentPush(ctx, OutboxItem{UserID: uid, Payload: map[string]any{"message": map[string]any{"id": mid, "conversationId": "g", "type": "system", "managementOnly": restricted}}})
			want := uid != "outsider" && (!restricted || uid == "owner" || uid == "admin")
			if err != nil || ok != want {
				t.Fatalf("push %s %s => %v %v", uid, event, ok, err)
			}
		}
	}
	if _, _, _, err := p.RecallAuthorized(ctx, "owner", mid, at, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	if ok, err := p.CanPresentPush(ctx, OutboxItem{UserID: "owner", Payload: map[string]any{"message": map[string]any{"id": mid, "conversationId": "g", "managementOnly": false}}}); err != nil || ok {
		t.Fatal("recalled original push", ok, err)
	}
	if ok, err := p.CanPresentPush(ctx, OutboxItem{UserID: "outsider", Payload: map[string]any{"invitation": map[string]any{"id": "invite"}}}); err != nil || !ok {
		t.Fatal("personal invitation suppressed", err)
	}
	oldID := makeMessage("member", 8*24*time.Hour, 1, false)
	if _, _, _, err := p.AdminRecallGroupWukongMessage(ctx, "g", oldID, "platform-admin", "historical moderation", at); err != nil {
		t.Fatal("backend moderation must retain unlimited recall", err)
	}
	ownID := makeMessage("peer", time.Minute, 1, false)
	if _, err := p.pool.Exec(ctx, `DELETE FROM im_members WHERE conversation_id='g' AND user_id='peer'`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := p.RecallAuthorized(ctx, "peer", ownID, at, 2*time.Minute); !errors.Is(err, ErrForbidden) {
		t.Fatal("former member retained recall permission", err)
	}
}

func TestGroupRecallWithPinnedWuKong(t *testing.T) {
	api := os.Getenv("IM_TEST_WUKONG_PATCH_URL")
	if api == "" {
		t.Skip("pinned WuKongIM is required")
	}
	p := newIsolatedWukongStore(t)
	ctx := t.Context()
	client, err := wukong.NewClient(wukong.Config{APIURL: api, ManagerURL: api, ManagerToken: os.Getenv("IM_TEST_WUKONG_MANAGER_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	owner, member, cid := "recall-owner-"+suffix, "recall-member-"+suffix, "recall-group-"+suffix
	for _, uid := range []string{owner, member} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$1,$1,now(),now())`, uid); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.CreateGroupRecord(ctx, cid, owner, "Recall integration", []string{member}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = client.UpsertChannel(ctx, wukong.ChannelRequest{ChannelID: cid, ChannelType: 2, Subscribers: []string{owner, member}, Reset: 1}); err != nil {
		t.Fatal(err)
	}
	sent, err := client.SendStoredMessage(ctx, wukong.StoredMessageRequest{ClientMsgNo: "recall-body-" + suffix, FromUID: member, ChannelID: cid, ChannelType: 2, Payload: map[string]any{"type": 1, "content": "recall integration body"}})
	if err != nil {
		t.Fatal(err)
	}
	var wire wukong.SyncedMessage
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		rows, e := client.SearchMessages(ctx, wukong.MessageSearchRequest{LoginUID: owner, ChannelID: cid, ChannelType: 2, MessageIDs: []int64{sent.MessageID}})
		if e != nil {
			t.Fatal(e)
		}
		if len(rows) > 0 {
			wire = rows[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if wire == nil {
		t.Fatal("message not persisted by pinned engine")
	}
	body, _ := json.Marshal(wire["payload"])
	wire["payload"], wire["message_id"] = body, sent.MessageID
	encoded, _ := json.Marshal(wire)
	if _, err = p.PutWukongWebhookEvent(ctx, wukong.WebhookEvent{ID: suffix, EventType: wukong.EventMessageNotify, Payload: encoded, ReceivedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	mid := strconv.FormatInt(sent.MessageID, 10)
	if _, _, _, err = p.RecallAuthorized(ctx, owner, mid, time.Now(), 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{owner, member} {
		extras, e := p.LoadWukongMessageExtensions(ctx, uid, []string{mid})
		if e != nil || extras[mid]["recalledAt"] == nil || extras[mid]["revoker"] != owner {
			t.Fatalf("recipient %s extension=%v err=%v", uid, extras, e)
		}
	}
	var commandRaw []byte
	if err = p.pool.QueryRow(ctx, `SELECT payload FROM im_wukong_outbox WHERE operation='business.event' AND payload->>'event'='message.recalled' AND payload->'param'->'payload'->>'messageId'=$1`, mid).Scan(&commandRaw); err != nil {
		t.Fatal(err)
	}
	var command wukong.CommandPayload
	if err = json.Unmarshal(commandRaw, &command); err != nil {
		t.Fatal(err)
	}
	if len(command.Recipients) != 2 {
		t.Fatalf("CMD must still reach both roles: %v", command.Recipients)
	}
	if err = client.SendCommand(ctx, command.Recipients, command.Event, command.Param); err != nil {
		t.Fatal("real CMD send", err)
	}
	// Recall changes only the business extension, never physically destroys the engine body.
	rows, err := client.SearchMessages(ctx, wukong.MessageSearchRequest{LoginUID: member, ChannelID: cid, ChannelType: 2, MessageIDs: []int64{sent.MessageID}})
	if err != nil || len(rows) != 1 {
		t.Fatal("raw original was deleted", err)
	}
	raw, _ := json.Marshal(rows[0]["payload"])
	var content map[string]any
	if err = json.Unmarshal(raw, &content); err != nil || content["content"] != "recall integration body" {
		t.Fatal("raw body changed", err)
	}
}
