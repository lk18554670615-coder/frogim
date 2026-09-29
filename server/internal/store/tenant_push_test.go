package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/wukong"
)

func tenantPushDB(t *testing.T, managed bool) *Postgres {
	t.Helper()
	raw := os.Getenv("IM_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("isolated enterprise PostgreSQL required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() != "127.0.0.1" {
		t.Fatal("explicit loopback test database required")
	}
	c, err := pgx.Connect(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("tenant_push_%d", time.Now().UnixNano())
	if _, err = c.Exec(t.Context(), `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = c.Exec(context.Background(), `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
		_ = c.Close(context.Background())
	})
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	opts := PostgresOptions{}
	if managed {
		opts.TenantID = "a"
	}
	p, err := NewPostgresWithOptions(t.Context(), u.String(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	pushExec(t, p, `INSERT INTO im_users(id,phone,name,platform_account_id,created_at) VALUES('receiver','19900008201','Receiver','account',now()),('sender','19900008202','Sender','sender-account',now())`)
	pushExec(t, p, `INSERT INTO im_conversations(id,kind,created_at,updated_at) VALUES('group','group',now(),now()),('direct','direct',now(),now())`)
	pushExec(t, p, `INSERT INTO im_groups(conversation_id,owner_id,history_visible_to_new_members) VALUES('group','sender',true)`)
	pushExec(t, p, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) SELECT c.id,u.id,CASE WHEN u.id='sender' THEN 'owner' ELSE 'member' END,now()-interval '1 hour' FROM im_conversations c CROSS JOIN im_users u`)
	pushExec(t, p, `INSERT INTO im_friendships(user_id,friend_user_id) VALUES('receiver','sender'),('sender','receiver')`)
	pushExec(t, p, `INSERT INTO im_devices(id,user_id,platform,provider,push_token) VALUES('legacy-device','receiver','android','getui','legacy-token-sentinel')`)
	return p
}
func pushExec(t *testing.T, p *Postgres, sql string, args ...any) {
	t.Helper()
	if _, e := p.pool.Exec(t.Context(), sql, args...); e != nil {
		t.Fatal(e)
	}
}
func pushInsert(t *testing.T, p *Postgres, event, payload string) OutboxItem {
	t.Helper()
	var item OutboxItem
	var raw []byte
	err := p.pool.QueryRow(t.Context(), `INSERT INTO im_push_outbox(user_id,event_type,payload,tenant_push) VALUES('receiver',$1,$2,'{"tenantId":"forged"}') RETURNING id,tenant_push`, event, payload).Scan(&item.ID, &raw)
	if err != nil {
		t.Fatal(err)
	}
	item.UserID, item.EventType, item.TenantManaged = "receiver", event, true
	if len(raw) > 0 && json.Unmarshal(raw, &item.TenantPush) != nil {
		t.Fatal("invalid snapshot")
	}
	if json.Unmarshal([]byte(payload), &item.Payload) != nil {
		t.Fatal("bad test payload")
	}
	return item
}
func pushIndex(t *testing.T, p *Postgres, id int64, cid string) {
	t.Helper()
	pushExec(t, p, `INSERT INTO im_wukong_message_index(message_id,client_msg_no,conversation_id,sender_id,channel_id,channel_type,message_seq,content_type,payload_sha256,message_timestamp) VALUES($1,'',$2,'sender',$2,2,$1,1,'fixture',now())`, id, cid)
}
func assertPushVisibility(t *testing.T, p *Postgres, item OutboxItem, expected bool) {
	t.Helper()
	allowed, err := p.CanPresentPush(t.Context(), item)
	if err != nil || allowed != expected {
		t.Fatalf("visibility=%v want=%v error=%v", allowed, expected, err)
	}
}

func TestTenantPostgresPushProvenanceMigrationAndClaims(t *testing.T) {
	p := tenantPushDB(t, false)
	legacy := pushInsert(t, p, "message.created", `{"message":{"id":"101","conversationId":"group","type":"text"}}`)
	if legacy.TenantPush != nil {
		t.Fatal("standalone caller-supplied provenance trusted")
	}
	items, err := p.ClaimPush(t.Context(), 10)
	if err != nil || len(items) != 1 || items[0].TenantManaged || len(items[0].Devices) != 1 {
		t.Fatal("standalone path changed", err)
	}
	pushExec(t, p, `UPDATE im_push_outbox SET status='pending'`)
	// Simulate 77 -> 78 upgrade with a pre-existing notification. Never backfill.
	pushExec(t, p, `DROP TRIGGER im_push_provenance ON im_push_outbox; ALTER TABLE im_push_outbox DROP COLUMN tenant_push; DELETE FROM im_schema_migrations WHERE version>=78; INSERT INTO im_schema_migrations(version) VALUES(77) ON CONFLICT DO NOTHING`)
	if err = p.migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = p.migrate(t.Context()); err != nil {
		t.Fatal("repeat migration", err)
	}
	if err = p.BindTenant(t.Context(), "a", true); err != nil {
		t.Fatal(err)
	}
	items, err = p.ClaimPush(t.Context(), 10)
	if err != nil || len(items) != 1 || !items[0].TenantManaged || items[0].TenantPush != nil || len(items[0].Devices) != 0 {
		t.Fatal("adopted legacy row guessed current ownership", err)
	}
	assertPushVisibility(t, p, items[0], false)
	pushExec(t, p, `UPDATE im_push_outbox SET status='sent'`)
	pushIndex(t, p, 101, "group")
	item := pushInsert(t, p, "message.created", `{"message":{"id":"101","conversationId":"group","type":"text","content":"private-text"}}`)
	if !item.ValidTenantPush() || item.TenantPush.TenantID != "a" || item.TenantPush.AccountID != "account" {
		t.Fatal("snapshot incorrect")
	}
	before, _ := json.Marshal(item.TenantPush)
	if strings.Contains(string(before), "private-text") {
		t.Fatal("snapshot copied body")
	}
	if _, e := p.pool.Exec(t.Context(), `UPDATE im_push_outbox SET tenant_push='{}' WHERE id=$1`, item.ID); e == nil {
		t.Fatal("provenance mutable")
	}
	assertPushVisibility(t, p, item, true)
	pushExec(t, p, `UPDATE im_users SET platform_auth_version=2 WHERE id='receiver'`)
	items, err = p.ClaimPush(t.Context(), 10)
	if err != nil || len(items) != 1 {
		t.Fatal(err)
	}
	after, _ := json.Marshal(items[0].TenantPush)
	if string(before) != string(after) || len(items[0].Devices) != 0 {
		t.Fatal("claim refreshed snapshot or loaded local tokens")
	}
	assertPushVisibility(t, p, items[0], false)
	if err = p.CompletePush(t.Context(), item.ID, fmt.Errorf("retry fixture")); err != nil {
		t.Fatal(err)
	}
	pushExec(t, p, `UPDATE im_push_outbox SET available_at=now() WHERE id=$1`, item.ID)
	items, err = p.ClaimPush(t.Context(), 10)
	if err != nil || len(items) != 1 || items[0].TenantPush.AuthVersion != 1 {
		t.Fatal("retry upgraded auth", err)
	}
	pushExec(t, p, `UPDATE im_push_outbox SET status='sent'`)
	for range 12 {
		pushInsert(t, p, "message.created", `{"message":{"id":"101","conversationId":"group","type":"text"}}`)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[int64]bool{}
	for range 3 {
		wg.Go(func() {
			batch, e := p.ClaimPush(t.Context(), 4)
			if e != nil {
				t.Error(e)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, i := range batch {
				if ids[i.ID] {
					t.Error("duplicate claim")
				}
				ids[i.ID] = true
				if len(i.Devices) != 0 {
					t.Error("loaded local devices")
				}
			}
		})
	}
	wg.Wait()
	if len(ids) != 12 {
		t.Fatal("lost parallel claims")
	}
}

func TestTenantPostgresPushIdentityAndBusinessFilters(t *testing.T) {
	p := tenantPushDB(t, true)
	item := pushInsert(t, p, "message.created", `{"message":{"id":"101","conversationId":"group","type":"image"}}`)
	if ok, e := p.CanPresentPush(t.Context(), item); ok || e == nil {
		t.Fatal("missing index must wait, not leak or silently drop")
	}
	pushIndex(t, p, 101, "group")
	assertPushVisibility(t, p, item, true)
	for _, tc := range []struct{ name, set, reset string }{
		{"auth", `UPDATE im_users SET platform_auth_version=2 WHERE id='receiver'`, `UPDATE im_users SET platform_auth_version=1 WHERE id='receiver'`},
		{"assignment", `UPDATE im_users SET assignment_version=2 WHERE id='receiver'`, `UPDATE im_users SET assignment_version=1 WHERE id='receiver'`},
		{"blocked", `UPDATE im_users SET local_identity_state='platform_blocked' WHERE id='receiver'`, `UPDATE im_users SET local_identity_state='active' WHERE id='receiver'`},
		{"retired", `UPDATE im_users SET local_identity_state='retired' WHERE id='receiver'`, `UPDATE im_users SET local_identity_state='active' WHERE id='receiver'`},
		{"banned", `UPDATE im_users SET banned=true WHERE id='receiver'`, `UPDATE im_users SET banned=false WHERE id='receiver'`},
		{"realm", `UPDATE im_tenant_identity SET access_version=2`, `UPDATE im_tenant_identity SET access_version=1`},
		{"suspended", `UPDATE im_tenant_identity SET access_enabled=false`, `UPDATE im_tenant_identity SET access_enabled=true`},
		{"muted", `UPDATE im_members SET notifications_muted=true WHERE user_id='receiver'`, `UPDATE im_members SET notifications_muted=false WHERE user_id='receiver'`},
		{"membership", `UPDATE im_members SET expires_at=now()-interval '1 minute' WHERE user_id='receiver'`, `UPDATE im_members SET expires_at=NULL WHERE user_id='receiver'`},
		{"history", `UPDATE im_groups SET history_visible_to_new_members=false; UPDATE im_members SET history_after_seq=101 WHERE user_id='receiver'`, `UPDATE im_groups SET history_visible_to_new_members=true`},
		{"group banned", `UPDATE im_groups SET banned=true`, `UPDATE im_groups SET banned=false`},
		{"dissolved", `UPDATE im_groups SET dissolved_at=now()`, `UPDATE im_groups SET dissolved_at=NULL`},
		{"expired", `UPDATE im_wukong_message_index SET expired_at=now()`, `UPDATE im_wukong_message_index SET expired_at=NULL`},
		{"blocked sender", `INSERT INTO im_blocks(user_id,blocked_user_id) VALUES('receiver','sender')`, `DELETE FROM im_blocks`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pushExec(t, p, tc.set)
			assertPushVisibility(t, p, item, false)
			pushExec(t, p, tc.reset)
			assertPushVisibility(t, p, item, true)
		})
	}
	pushExec(t, p, `INSERT INTO im_wukong_message_extensions(message_id,channel_id,channel_type,version,payload,updated_by,updated_at) VALUES(101,'group',2,1,'{"recalledAt":"2026-09-28T00:00:00Z"}','sender',now())`)
	assertPushVisibility(t, p, item, false)
	pushExec(t, p, `UPDATE im_wukong_message_extensions SET payload='{"deletedForEveryoneAt":"2026-09-28T00:00:00Z"}' WHERE message_id=101`)
	assertPushVisibility(t, p, item, false)
	pushExec(t, p, `DELETE FROM im_wukong_message_extensions WHERE message_id=101`)
	pushIndex(t, p, 102, "direct")
	direct := pushInsert(t, p, "message.created", `{"message":{"id":"102","conversationId":"direct","type":"text"}}`)
	assertPushVisibility(t, p, direct, true)
	pushExec(t, p, `DELETE FROM im_friendships WHERE user_id='receiver'`)
	assertPushVisibility(t, p, direct, false)
	for _, kind := range []string{"system", "screenshot", "unknown"} {
		i := pushInsert(t, p, "message.created", fmt.Sprintf(`{"message":{"id":"101","conversationId":"group","type":%q}}`, kind))
		assertPushVisibility(t, p, i, false)
	}
	assertPushVisibility(t, p, pushInsert(t, p, "messages.deleted", `{}`), false)
	pushExec(t, p, `UPDATE im_users SET banned=true WHERE id='receiver'`)
	if i := pushInsert(t, p, "friend.request", `{}`); i.TenantPush != nil {
		t.Fatal("banned enqueue took active snapshot")
	}
}

func TestTenantPostgresPushCallAndPrivateNotifications(t *testing.T) {
	p := tenantPushDB(t, true)
	pushExec(t, p, `INSERT INTO im_call_sessions(id,conversation_id,caller_id,callee_id,participant_ids,media_type,status,invited_at,expires_at,updated_at) VALUES('call','direct','sender','receiver',ARRAY['sender','receiver'],'video','invited',now(),now()+interval '40 seconds',now())`)
	call := pushInsert(t, p, "call.invited", `{"callId":"call","conversationId":"direct","mediaType":"video"}`)
	if call.TenantPush == nil || time.Until(call.TenantPush.ExpiresAt) > 40*time.Second {
		t.Fatal("call expiry not bounded by invitation")
	}
	assertPushVisibility(t, p, call, true)
	pushExec(t, p, `UPDATE im_call_sessions SET declined_user_ids=ARRAY['receiver']`)
	assertPushVisibility(t, p, call, false)
	pushExec(t, p, `UPDATE im_call_sessions SET declined_user_ids='{}'`)
	assertPushVisibility(t, p, call, true)
	pushExec(t, p, `UPDATE im_call_sessions SET status='cancelled'`)
	assertPushVisibility(t, p, call, false)
	pushExec(t, p, `INSERT INTO im_friend_requests(id,from_user_id,to_user_id,status,created_at,updated_at,expires_at) VALUES('request','sender','receiver','pending',now(),now(),now()+interval '1 day')`)
	request := pushInsert(t, p, "friend.request", `{"requestId":"request","message":"private-verification"}`)
	assertPushVisibility(t, p, request, true)
	pushExec(t, p, `UPDATE im_friend_requests SET status='accepted'`)
	assertPushVisibility(t, p, request, false)
	assertPushVisibility(t, p, pushInsert(t, p, "friend.request.updated", `{"requestId":"request"}`), true)
	pushExec(t, p, `INSERT INTO im_group_invites(id,conversation_id,inviter_id,invitee_id,source,status,created_at,expires_at,updated_at) VALUES('invite','group','sender','receiver','member','pending',now(),now()+interval '1 hour',now())`)
	invite := pushInsert(t, p, "group.invite", `{"inviteId":"invite"}`)
	assertPushVisibility(t, p, invite, false)
	pushExec(t, p, `DELETE FROM im_members WHERE conversation_id='group' AND user_id='receiver'`)
	assertPushVisibility(t, p, invite, true)
	pushExec(t, p, `UPDATE im_group_invites SET status='cancelled'`)
	assertPushVisibility(t, p, invite, false)
	pushExec(t, p, `INSERT INTO im_announcements(id,title,content,status,target_type,created_by,created_at,updated_at) VALUES('announcement','private-title','private-content','published','all','fixture',now(),now())`)
	tx, e := p.pool.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if e = enqueueAnnouncementPush(t.Context(), tx, &model.Announcement{ID: "announcement", TargetType: "all", Title: "private-title", Content: "private-content"}, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = p.pool.QueryRow(t.Context(), `SELECT count(*) FROM im_push_outbox WHERE event_type='announcement.published' AND tenant_push IS NOT NULL`).Scan(&count); e != nil || count != 2 {
		t.Fatal("bulk enqueue missing provenance", e)
	}
	announcement := pushInsert(t, p, "announcement.published", `{"announcementId":"announcement"}`)
	assertPushVisibility(t, p, announcement, true)
	pushExec(t, p, `UPDATE im_announcements SET status='withdrawn'`)
	assertPushVisibility(t, p, announcement, false)
	pushExec(t, p, `INSERT INTO im_push_outbox(user_id,event_type,payload,created_at) VALUES('receiver','message.created','{"message":{"id":"101","conversationId":"group","type":"text"}}',now()-interval '25 hours')`)
	items, e := p.ClaimPush(t.Context(), 100)
	if e != nil {
		t.Fatal(e)
	}
	for _, i := range items {
		if len(i.Devices) != 0 {
			t.Fatal("managed claim fetched legacy tokens")
		}
		if i.EventType == "message.created" {
			assertPushVisibility(t, p, i, false)
		}
	}
}

func TestTenantPostgresPushOfflineHooks(t *testing.T) {
	p := tenantPushDB(t, true)
	for index, kind := range []uint8{wukong.ChannelGroup, wukong.ChannelPerson} {
		cid := "group"
		if kind == wukong.ChannelPerson {
			cid = "direct"
		}
		id := int64(201 + index)
		pushIndex(t, p, id, cid)
		m := wukongOfflineNotification{ToUIDs: []string{"receiver"}}
		m.MessageID = id
		m.FromUID = "sender"
		m.ChannelID = cid
		m.ChannelType = kind
		m.Header.RedDot = 1
		m.Payload = []byte(`{"type":2,"content":"private-hook-body"}`)
		raw, _ := json.Marshal(m)
		tx, err := p.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err = enqueueWukongOfflinePush(t.Context(), tx, wukong.WebhookEvent{Payload: raw}); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		items, err := p.ClaimPush(t.Context(), 100)
		if err != nil || len(items) != 1 || !items[0].ValidTenantPush() || items[0].TenantPush.ConversationID != cid || items[0].TenantPush.MessageType != "image" {
			t.Fatal("offline hook provenance", err)
		}
		assertPushVisibility(t, p, items[0], true)
		encoded, _ := json.Marshal(items[0].TenantPush)
		if strings.Contains(string(encoded), "private-hook-body") || len(items[0].Devices) != 0 {
			t.Fatal("offline hook leaked local data")
		}
		if err = p.CompletePush(t.Context(), items[0].ID, nil); err != nil {
			t.Fatal(err)
		}
	}
}
