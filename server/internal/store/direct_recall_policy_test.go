package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/linli/im/server/internal/wukong"
)

func TestDirectRecallWindowAuthorizationAndRecipientSync(t *testing.T) {
	p := newIsolatedWukongStore(t)
	ctx := t.Context()
	at := time.Now().UTC().Truncate(time.Second)
	for _, uid := range []string{"sender", "recipient", "outsider"} {
		if _, err := p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$1,$1,$2,$2)`, uid, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := p.GetOrCreateDirectConversation(ctx, "sender", "recipient", "direct-recall-test", at.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A role string on a direct conversation cannot grant group privileges.
	if _, err := p.pool.Exec(ctx, `UPDATE im_members SET role='admin' WHERE conversation_id='direct-recall-test' AND user_id='recipient'`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.pool.Exec(ctx, `INSERT INTO im_settings(key,value) VALUES('groupRecallMinutes','10080') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		t.Fatal(err)
	}
	var seq int64
	makeMessage := func(age time.Duration, contentType int, expires *time.Time) string {
		t.Helper()
		seq++
		return insertTestWukongMessage(t, p, ctx, 910000+seq, fmt.Sprintf("direct-client-%d", seq), "direct-recall-test", "sender", seq, contentType, expires, "", at.Add(-age)).ID
	}
	for _, window := range []time.Duration{time.Minute, time.Hour, 24 * time.Hour, 7 * 24 * time.Hour} {
		for _, age := range []time.Duration{window - time.Microsecond, window, window + time.Microsecond} {
			for _, actor := range []string{"sender", "recipient", "outsider"} {
				mid := makeMessage(age, 1, nil)
				_, _, _, err := p.RecallAuthorized(ctx, actor, mid, at, window)
				allowed := actor == "sender" && age <= window
				if (err == nil) != allowed {
					t.Fatalf("window=%v age=%v actor=%s err=%v", window, age, actor, err)
				}
				if !allowed {
					continue
				}
				for _, uid := range []string{"sender", "recipient"} {
					extra, err := p.LoadWukongMessageExtensions(ctx, uid, []string{mid})
					if err != nil || extra[mid]["recalledAt"] == nil || extra[mid]["revoker"] != "sender" {
						t.Fatalf("%s extension=%v err=%v", uid, extra, err)
					}
				}
				var raw []byte
				if err = p.pool.QueryRow(ctx, `SELECT payload FROM im_wukong_outbox WHERE operation='business.event' AND payload->>'event'='message.recalled' AND payload->'param'->'payload'->>'messageId'=$1`, mid).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var cmd wukong.CommandPayload
				if err = json.Unmarshal(raw, &cmd); err != nil {
					t.Fatal(err)
				}
				slices.Sort(cmd.Recipients)
				if !slices.Equal(cmd.Recipients, []string{"recipient", "sender"}) {
					t.Fatalf("recall CMD recipients=%v", cmd.Recipients)
				}
				var before, after int
				if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox`).Scan(&before); err != nil {
					t.Fatal(err)
				}
				if _, _, _, err = p.RecallAuthorized(ctx, actor, mid, at.Add(8*24*time.Hour), time.Minute); err != nil {
					t.Fatal("repeat must be idempotent even after policy shrinks", err)
				}
				if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox`).Scan(&after); err != nil || before != after {
					t.Fatalf("duplicate event: %d -> %d (%v)", before, after, err)
				}
			}
		}
	}
	expired := at.Add(-time.Second)
	for _, mid := range []string{makeMessage(time.Hour, 1, &expired), makeMessage(time.Minute, wukong.ContentTypeSystemEvent, nil)} {
		if _, _, _, err := p.RecallAuthorized(ctx, "sender", mid, at, 24*time.Hour); !errors.Is(err, ErrForbidden) {
			t.Fatalf("expired/system recall=%v", err)
		}
	}
	editableID := makeMessage(3*time.Minute, 1, nil)
	if _, _, err := p.EditMessage(ctx, "sender", editableID, "direct-edit", map[string]any{"text": "edit"}, map[string]any{"text": "original"}, at, 2*time.Minute); !errors.Is(err, ErrForbidden) {
		t.Fatal("edit window extended", err)
	}
	if _, _, _, err := p.RecallAuthorized(ctx, "sender", editableID, at, 24*time.Hour); err != nil {
		t.Fatal("recall must not inherit edit limit", err)
	}
	if _, _, _, _, err := p.AdminRecallWukongMessage(ctx, "sender", "recipient", makeMessage(8*24*time.Hour, 1, nil), "platform-admin", "old message moderation", at); err != nil {
		t.Fatal("backend moderation unexpectedly limited", err)
	}
}
