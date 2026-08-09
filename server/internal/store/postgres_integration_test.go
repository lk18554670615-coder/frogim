package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linli/im/server/internal/model"
)

func drainMessageFanout(t *testing.T, repository *Postgres, ctx context.Context) {
	t.Helper()
	for range 10000 {
		_, worked, err := repository.ProcessMessageFanout(ctx, 500)
		if err != nil {
			t.Fatalf("process message fanout: %v", err)
		}
		if !worked {
			return
		}
	}
	t.Fatal("message fanout did not drain")
}

func TestPostgresConcurrentMessageSequenceAndIdempotency(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	a, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u1 := "test_a_" + suffix
	u2 := "test_b_" + suffix
	cid := "test_conv_" + suffix
	now := time.Now()
	state, err := a.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.Users[u1] = &model.User{ID: u1, Phone: "1" + suffix, Name: "A", CreatedAt: now}
	state.Users[u2] = &model.User{ID: u2, Phone: "2" + suffix, Name: "B", CreatedAt: now}
	state.Conversations[cid] = &model.Conversation{ID: cid, Type: "direct", CreatedAt: now, UpdatedAt: now}
	state.Members[cid] = map[string]*model.ConversationMember{u1: {ConversationID: cid, UserID: u1, Role: "member", JoinedAt: now}, u2: {ConversationID: cid, UserID: u2, Role: "member", JoinedAt: now}}
	if err = a.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	const n = 32
	seqs := make(chan int64, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			repo := a
			if i%2 == 1 {
				repo = b
			}
			m, dup, _, e := repo.SendMessage(ctx, MessageInput{UserID: u1, ConversationID: cid, ClientMsgID: fmt.Sprintf("c-%d", i), Type: "text", Body: map[string]any{"text": "x"}, MessageID: fmt.Sprintf("m_%s_%d", suffix, i), CreatedAt: time.Now().UnixMilli()})
			if e != nil {
				errs <- e
				return
			}
			if dup {
				errs <- fmt.Errorf("unexpected duplicate")
				return
			}
			seqs <- m.Seq
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	close(seqs)
	got := make([]int, 0, n)
	for s := range seqs {
		got = append(got, int(s))
	}
	sort.Ints(got)
	for i, v := range got {
		if v != i+1 {
			t.Fatalf("sequence gap at %d: %v", i, got)
		}
	}
	type dupResult struct {
		duplicate bool
		err       error
	}
	var dupWG sync.WaitGroup
	results := make(chan dupResult, 2)
	for i, repo := range []*Postgres{a, b} {
		dupWG.Add(1)
		go func(i int, repo *Postgres) {
			defer dupWG.Done()
			_, dup, _, e := repo.SendMessage(ctx, MessageInput{UserID: u1, ConversationID: cid, ClientMsgID: "same", Type: "text", Body: map[string]any{"text": "same"}, MessageID: fmt.Sprintf("same_%s_%d", suffix, i), CreatedAt: time.Now().UnixMilli()})
			results <- dupResult{duplicate: dup, err: e}
		}(i, repo)
	}
	dupWG.Wait()
	close(results)
	duplicateCount := 0
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.duplicate {
			duplicateCount++
		}
	}
	if duplicateCount != 1 {
		t.Fatalf("expected one duplicate ACK, got %d", duplicateCount)
	}
}

func TestPostgresConversationLifecycleScheduledAndExpiry(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u1, u2, cid := "life_a_"+suffix, "life_b_"+suffix, "life_c_"+suffix
	now := time.Now().UTC().Truncate(time.Microsecond)
	state, err := p.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.Users[u1] = &model.User{ID: u1, Phone: "31" + suffix, Name: "A", CreatedAt: now}
	state.Users[u2] = &model.User{ID: u2, Phone: "32" + suffix, Name: "B", CreatedAt: now}
	state.Conversations[cid] = &model.Conversation{ID: cid, Type: "direct", CreatedAt: now, UpdatedAt: now}
	state.Members[cid] = map[string]*model.ConversationMember{
		u1: {ConversationID: cid, UserID: u1, Role: "member", JoinedAt: now},
		u2: {ConversationID: cid, UserID: u2, Role: "member", JoinedAt: now},
	}
	if err = p.Save(ctx, state); err != nil {
		t.Fatal(err)
	}

	archived := true
	if err = p.UpdateConversationPreferences(ctx, u1, cid, ConversationPreferences{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	conversations, err := p.ListConversations(ctx, u1, 10)
	if err != nil || len(conversations) != 1 || conversations[0]["membership"].(*model.ConversationMember).Archived != true {
		t.Fatalf("archive preference was not persisted: %#v %v", conversations, err)
	}

	expires := now.Add(time.Minute)
	message, _, _, err := p.SendMessage(ctx, MessageInput{UserID: u1, ConversationID: cid, ClientMsgID: "life-msg-" + suffix, Type: "text", Body: map[string]any{"text": "secret"}, MessageID: "life_m_" + suffix, CreatedAt: now.UnixMilli(), ExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	adminMessages, total, _, err := p.ListAdminMessages(ctx, "secret", "", "", 10)
	if err != nil || total != 0 || len(adminMessages) != 0 {
		t.Fatalf("admin message search must not inspect private body: total=%d items=%#v err=%v", total, adminMessages, err)
	}
	adminMessages, total, _, err = p.ListAdminMessages(ctx, message.ID, "", "", 10)
	if err != nil || total != 1 || len(adminMessages) != 1 || len(adminMessages[0].Body) != 0 {
		t.Fatalf("admin metadata result leaked body: total=%d items=%#v err=%v", total, adminMessages, err)
	}
	pastBan := now.Add(-time.Minute)
	if err = p.SetUserBanRecord(ctx, "admin", u2, true, &pastBan, "temporary test ban", "aud_ban_"+suffix, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	expiredUsers, err := p.ExpireUserBans(ctx, now, 10)
	if err != nil || len(expiredUsers) != 1 || expiredUsers[0] != u2 {
		t.Fatalf("expire timed ban: ids=%v err=%v", expiredUsers, err)
	}
	if err = p.RecordAdminAudit(ctx, &model.AuditEntry{ID: "aud_failed_" + suffix, ActorID: "moderator", Action: "admin.request", TargetType: "admin_request", TargetID: "/v1/admin/groups/x/disband", Metadata: map[string]any{"status": 403}, Result: "failed", IP: "203.0.113.9", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	audits, auditTotal, _, err := p.ListAdminAudits(ctx, "groups/x", "failed", "", 10)
	foundAudit := false
	for _, item := range audits {
		if item.ID == "aud_failed_"+suffix && item.IP == "203.0.113.9" && item.Result == "failed" {
			foundAudit = true
		}
	}
	if err != nil || auditTotal < 1 || !foundAudit {
		t.Fatalf("admin failed audit: total=%d items=%#v err=%v", auditTotal, audits, err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_friendships(user_id,friend_user_id,created_at,updated_at) VALUES($1,$2,$3,$3),($2,$1,$3,$3)`, u1, u2, now); err != nil {
		t.Fatal(err)
	}
	friends, friendTotal, _, err := p.ListAdminFriendships(ctx, u1, "", 10)
	if err != nil || friendTotal != 1 || len(friends) != 1 {
		t.Fatalf("admin friendships: total=%d items=%#v err=%v", friendTotal, friends, err)
	}
	feedbackContent := "cannot upload " + suffix
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_feedback(id,user_id,category,content,contact,created_at) VALUES($1,$2,'bug',$3,'contact@example.test',$4)`, `feedback_`+suffix, u1, feedbackContent, now); err != nil {
		t.Fatal(err)
	}
	feedback, feedbackTotal, _, err := p.ListAdminFeedback(ctx, feedbackContent, "bug", "", 10)
	if err != nil || feedbackTotal != 1 || len(feedback) != 1 {
		t.Fatalf("admin feedback: total=%d items=%#v err=%v", feedbackTotal, feedback, err)
	}
	groupID := "life_group_" + suffix
	groupTitle := "Operations Group " + suffix
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,created_at,updated_at) VALUES($1,'group',$2,$3,$3)`, groupID, groupTitle, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_groups(conversation_id,owner_id,announcement,announcement_version,join_policy,allow_member_add_friend,updated_at) VALUES($1,$2,'Current announcement',1,'invite',true,$3)`, groupID, u1, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'owner',$4),($1,$3,'member',$4)`, groupID, u1, u2, now); err != nil {
		t.Fatal(err)
	}
	adminGroups, groupTotal, _, err := p.ListAdminGroups(ctx, groupTitle, "active", "", 10)
	if err != nil || groupTotal != 1 || len(adminGroups) != 1 {
		t.Fatalf("admin groups: total=%d items=%#v err=%v", groupTotal, adminGroups, err)
	}
	groupOverview, err := p.AdminGroupOverview(ctx, groupID)
	if err != nil || groupOverview["announcement"] != "Current announcement" {
		t.Fatalf("group overview: %#v err=%v", groupOverview, err)
	}
	groupMembers, memberTotal, _, err := p.ListAdminGroupMembers(ctx, groupID, "", "", 10)
	if err != nil || memberTotal != 2 || len(groupMembers) != 2 {
		t.Fatalf("group members: total=%d items=%#v err=%v", memberTotal, groupMembers, err)
	}
	actual, recipients, err := p.MarkDelivered(ctx, u2, cid, message.Seq+100, now)
	if err != nil || actual != message.Seq || len(recipients) != 2 {
		t.Fatalf("delivered cursor: actual=%d recipients=%v err=%v", actual, recipients, err)
	}
	_, recipients, err = p.MarkDelivered(ctx, u2, cid, 0, now)
	if err != nil || len(recipients) != 0 {
		t.Fatalf("delivered cursor must be monotonic and idempotent: recipients=%v err=%v", recipients, err)
	}
	if _, _, err = p.MarkDelivered(ctx, "outsider", cid, 1, now); err != ErrForbidden {
		t.Fatalf("outsider delivered cursor error=%v", err)
	}

	scheduledAt := now.Add(10 * time.Second)
	scheduled := &model.ScheduledMessage{ID: "life_s_" + suffix, UserID: u1, ConversationID: cid, ClientMsgID: "life-scheduled-" + suffix, Type: "text", Body: map[string]any{"text": "later"}, ScheduledAt: scheduledAt, Status: "pending", CreatedAt: now, UpdatedAt: now}
	created, duplicate, err := p.CreateScheduledMessage(ctx, scheduled)
	if err != nil || duplicate || created.Status != "pending" {
		t.Fatalf("create scheduled: %#v duplicate=%v err=%v", created, duplicate, err)
	}
	if _, duplicate, err = p.CreateScheduledMessage(ctx, scheduled); err != nil || !duplicate {
		t.Fatalf("scheduled idempotency duplicate=%v err=%v", duplicate, err)
	}
	leased, err := p.LeaseScheduledMessages(ctx, scheduledAt.Add(time.Second), 2*time.Minute, 10)
	if err != nil || len(leased) != 1 || leased[0].Attempts != 1 {
		t.Fatalf("lease scheduled: %#v err=%v", leased, err)
	}
	if err = p.CompleteScheduledMessage(ctx, scheduled.ID, message.ID, nil, scheduledAt.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}

	expired, err := p.ExpireMessages(ctx, expires.Add(time.Second), 10)
	foundExpired := false
	for _, item := range expired {
		if item.MessageID == message.ID && len(item.MemberIDs) == 2 {
			foundExpired = true
		}
	}
	if err != nil || !foundExpired {
		t.Fatalf("expire message: %#v err=%v", expired, err)
	}
	history, err := p.ListMessages(ctx, u1, cid, 0, 10)
	if err != nil || len(history) != 1 || history[0].ExpiredAt == nil || len(history[0].Body) != 0 {
		t.Fatalf("expired message was not redacted: %#v err=%v", history, err)
	}

	mediaID := "life_media_" + suffix
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_media(id,owner_id,object_key,mime,size,status,created_at) VALUES($1,$2,$3,'image/png',10,'pending',$4)`, mediaID, u1, "users/"+u1+"/abandoned.png", now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	cleanup, err := p.LeaseMediaCleanup(ctx, now, time.Hour, 24*time.Hour, 10*time.Minute, 10)
	if err != nil || len(cleanup) != 1 || cleanup[0].ID != mediaID {
		t.Fatalf("lease media cleanup: %#v err=%v", cleanup, err)
	}
	if err = p.CompleteMediaCleanup(ctx, mediaID, errors.New("object storage unavailable"), now); err != nil {
		t.Fatal(err)
	}
	status, err := p.MediaCleanupStatus(ctx, now, time.Hour, 24*time.Hour)
	if err != nil || status.PendingCandidates < 1 || status.FailedAttempts < 1 {
		t.Fatalf("media cleanup status: %#v err=%v", status, err)
	}
	cleanup, err = p.LeaseMediaCleanup(ctx, now.Add(time.Second), time.Hour, 24*time.Hour, 10*time.Minute, 10)
	if err != nil || len(cleanup) != 1 {
		t.Fatalf("retry media cleanup: %#v err=%v", cleanup, err)
	}
	if err = p.CompleteMediaCleanup(ctx, mediaID, nil, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresPolicyDeviceOwnershipAndRevision(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u1, u2, cid := "policy_a_"+suffix, "policy_b_"+suffix, "policy_c_"+suffix
	now := time.Now()
	for _, u := range []struct{ id, phone string }{{u1, "31" + suffix}, {u2, "32" + suffix}} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$2,$3,$4)`, u.id, u.phone, u.id, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,created_at,updated_at) VALUES($1,'direct',$2,$2)`, cid, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'member',$4),($1,$3,'member',$4)`, cid, u1, u2, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_blocks(user_id,blocked_user_id) VALUES($1,$2)`, u2, u1); err != nil {
		t.Fatal(err)
	}
	input := MessageInput{UserID: u1, ConversationID: cid, ClientMsgID: "blocked", Type: "text", Body: map[string]any{"text": "hello"}, MessageID: "policy_m1_" + suffix, CreatedAt: now.UnixMilli()}
	if _, _, _, err = p.SendMessage(ctx, input); err != ErrForbidden {
		t.Fatalf("blocked send err=%v", err)
	}
	if _, err = p.pool.Exec(ctx, `DELETE FROM im_blocks WHERE user_id=$1 AND blocked_user_id=$2`, u2, u1); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_sensitive_words(id,value) VALUES($1,'forbidden|test')`, `policy_w_`+suffix); err != nil {
		t.Fatal(err)
	}
	input.ClientMsgID = "sensitive"
	input.MessageID = "policy_m2_" + suffix
	input.Body = map[string]any{"text": "contains forbidden content"}
	if _, _, _, err = p.SendMessage(ctx, input); err != ErrForbidden {
		t.Fatalf("sensitive send err=%v", err)
	}
	if err = p.RegisterDevice(ctx, u1, Device{ID: "shared_device_" + suffix, Platform: "ios", Provider: "apns", PushToken: "token-a-" + suffix}); err != nil {
		t.Fatal(err)
	}
	if err = p.RegisterDevice(ctx, u2, Device{ID: "shared_device_" + suffix, Platform: "ios", Provider: "apns", PushToken: "token-b-" + suffix}); err != ErrForbidden {
		t.Fatalf("device ownership err=%v", err)
	}

	s1, err := p.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := p.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s1.Users[u1].Name = "updated"
	if err = p.Save(ctx, s1); err != nil {
		t.Fatal(err)
	}
	s2.Users[u2].Name = "stale"
	if err = p.Save(ctx, s2); err != ErrConflict {
		t.Fatalf("stale snapshot err=%v", err)
	}
	sid, newID := "refresh_old_"+suffix, "refresh_new_"+suffix
	if err = p.CreateRefreshSession(ctx, sid, u1, []byte("old"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = p.RotateRefreshSession(ctx, sid, newID, []byte("new"), time.Now().Add(time.Hour), u1); err != nil {
		t.Fatal(err)
	}
	if err = p.RotateRefreshSession(ctx, sid, "reuse_"+suffix, []byte("reuse"), time.Now().Add(time.Hour), u1); err != ErrForbidden {
		t.Fatalf("refresh reuse err=%v", err)
	}
	if err = p.RevokeRefreshSession(ctx, newID, u1); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRuntimeModerationReceiptsAndOutboxRecovery(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u1, u2 := "runtime_a_"+suffix, "runtime_b_"+suffix
	cid := "runtime_conv_" + suffix
	now := time.Now()
	for _, uid := range []string{u1, u2} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$2,$1,$3)`, uid, uid, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,created_at,updated_at) VALUES($1,'direct',$2,$2)`, cid, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'member',$4),($1,$3,'member',$4)`, cid, u1, u2, now); err != nil {
		t.Fatal(err)
	}

	msgID := "runtime_msg_" + suffix
	msg, duplicate, events, err := p.SendMessage(ctx, MessageInput{UserID: u1, ConversationID: cid, ClientMsgID: "runtime-client", Type: "text", Body: map[string]any{"text": "moderate me"}, MessageID: msgID, CreatedAt: now.UnixMilli()})
	if err != nil || duplicate || len(events) != 1 {
		t.Fatalf("send err=%v duplicate=%v events=%d", err, duplicate, len(events))
	}
	drainMessageFanout(t, p, ctx)
	if err = p.SetFavorite(ctx, u2, msgID, true); err != nil {
		t.Fatalf("favorite create: %v", err)
	}
	favorites, favoriteErr := p.ListFavorites(ctx, u2, 10)
	if favoriteErr != nil || len(favorites) != 1 || favorites[0].ID != msgID {
		t.Fatalf("favorites=%v err=%v", favorites, favoriteErr)
	}
	if err = p.SetFavorite(ctx, "not_a_member_"+suffix, msgID, true); err != ErrForbidden {
		t.Fatalf("non-member favorite err=%v", err)
	}
	if err = p.SetFavorite(ctx, u2, msgID, false); err != nil {
		t.Fatalf("favorite delete: %v", err)
	}
	if favorites, favoriteErr = p.ListFavorites(ctx, u2, 10); favoriteErr != nil || len(favorites) != 0 {
		t.Fatalf("favorites after delete=%v err=%v", favorites, favoriteErr)
	}
	var messagePush string
	if err = p.pool.QueryRow(ctx, `SELECT payload::text FROM im_push_outbox WHERE user_id=$1 AND event_type='message.created' ORDER BY id DESC LIMIT 1`, u2).Scan(&messagePush); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(messagePush, "moderate me") || strings.Contains(messagePush, `"body"`) {
		t.Fatalf("message body leaked to push outbox: %s", messagePush)
	}
	pinned, mutedNotifications, manualUnread := true, true, true
	if err = p.UpdateConversationPreferences(ctx, u2, cid, ConversationPreferences{Pinned: &pinned, NotificationsMuted: &mutedNotifications, ManualUnread: &manualUnread}); err != nil {
		t.Fatal(err)
	}
	conversations, err := p.ListConversations(ctx, u2, 10)
	if err != nil || len(conversations) != 1 {
		t.Fatalf("preferences list=%v err=%v", conversations, err)
	}
	membership := conversations[0]["membership"].(*model.ConversationMember)
	if !membership.Pinned || !membership.NotificationsMuted || !membership.ManualUnread {
		t.Fatalf("preferences not persisted: %+v", membership)
	}
	conversationMembers, ok := conversations[0]["members"].([]*model.ConversationMember)
	if !ok || len(conversationMembers) != 2 {
		t.Fatalf("conversation member projection=%T %+v", conversations[0]["members"], conversations[0]["members"])
	}
	for _, member := range conversationMembers {
		rawMember, _ := json.Marshal(member)
		if member.UserID == "" || member.Name == "" || strings.Contains(string(rawMember), `"phone"`) {
			t.Fatalf("unsafe conversation member=%+v", member)
		}
	}
	readSeq, users, err := p.MarkRead(ctx, u2, cid, msg.Seq, time.Now())
	if err != nil || readSeq != msg.Seq || len(users) != 2 {
		t.Fatalf("read seq=%d users=%v err=%v", readSeq, users, err)
	}
	var readEvents int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_sync_events WHERE event_type='message.read' AND payload->>'conversationId'=$1`, cid).Scan(&readEvents); err != nil || readEvents != 2 {
		t.Fatalf("read sync count=%d err=%v", readEvents, err)
	}
	if _, repeatedUsers, repeatErr := p.MarkRead(ctx, u2, cid, msg.Seq, time.Now()); repeatErr != nil || len(repeatedUsers) != 0 {
		t.Fatalf("idempotent read users=%v err=%v", repeatedUsers, repeatErr)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_sync_events WHERE event_type='message.read' AND payload->>'conversationId'=$1`, cid).Scan(&readEvents); err != nil || readEvents != 2 {
		t.Fatalf("duplicate read sync count=%d err=%v", readEvents, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT manual_unread FROM im_members WHERE conversation_id=$1 AND user_id=$2`, cid, u2).Scan(&manualUnread); err != nil || manualUnread {
		t.Fatalf("manual unread was not cleared: value=%v err=%v", manualUnread, err)
	}
	if err = p.HideConversation(ctx, u2, cid); err != nil {
		t.Fatal(err)
	}
	if conversations, err = p.ListConversations(ctx, u2, 10); err != nil || conversations == nil || len(conversations) != 0 {
		t.Fatalf("hidden list=%v err=%v", conversations, err)
	}
	if _, _, _, err = p.SendMessage(ctx, MessageInput{UserID: u1, ConversationID: cid, ClientMsgID: "runtime-client-next", Type: "text", Body: map[string]any{"text": "visible again"}, MessageID: "runtime_msg_next_" + suffix, CreatedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if conversations, err = p.ListConversations(ctx, u2, 10); err != nil || len(conversations) != 1 {
		t.Fatalf("reappeared list=%v err=%v", conversations, err)
	}

	recalledCID, recalledSeq, recallUsers, err := p.RecallAuthorized(ctx, u1, msgID, time.Now(), 2*time.Minute)
	if err != nil || recalledCID != cid || recalledSeq != msg.Seq || len(recallUsers) != 2 {
		t.Fatalf("recall cid=%s seq=%d users=%v err=%v", recalledCID, recalledSeq, recallUsers, err)
	}
	var recalled bool
	if err = p.pool.QueryRow(ctx, `SELECT recalled_at IS NOT NULL AND body='{}'::jsonb FROM im_messages WHERE id=$1`, msgID).Scan(&recalled); err != nil || !recalled {
		t.Fatalf("recalled=%v err=%v", recalled, err)
	}

	reportID := "runtime_report_" + suffix
	report := &model.Report{ID: reportID, ReporterID: u2, TargetType: "message", TargetID: msgID, Reason: "abuse", Status: "pending", CreatedAt: now, UpdatedAt: now}
	audit := &model.AuditEntry{ID: "runtime_audit_create_" + suffix, ActorID: u2, Action: "report.created", TargetType: "message", TargetID: msgID, Metadata: map[string]any{"reportId": reportID}, CreatedAt: now}
	if err = p.CreateReportRecord(ctx, report, audit); err != nil {
		t.Fatal(err)
	}
	status, err := p.ResolveReportRecord(ctx, "admin", reportID, "delete_message", "confirmed", "runtime_audit_resolve_"+suffix, time.Now())
	if err != nil || status != "resolved" {
		t.Fatalf("resolve status=%s err=%v", status, err)
	}

	refreshID := "runtime_refresh_" + suffix
	if err = p.CreateRefreshSession(ctx, refreshID, u1, []byte("hash"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	banReportID := "runtime_ban_report_" + suffix
	banReport := &model.Report{ID: banReportID, ReporterID: u2, TargetType: "user", TargetID: u1, Reason: "repeat abuse", Status: "pending", CreatedAt: now, UpdatedAt: now}
	banAudit := &model.AuditEntry{ID: "runtime_audit_ban_create_" + suffix, ActorID: u2, Action: "report.created", TargetType: "user", TargetID: u1, Metadata: map[string]any{"reportId": banReportID}, CreatedAt: now}
	if err = p.CreateReportRecord(ctx, banReport, banAudit); err != nil {
		t.Fatal(err)
	}
	if status, err = p.ResolveReportRecord(ctx, "admin", banReportID, "ban_user", "confirmed", "runtime_audit_ban_"+suffix, time.Now()); err != nil || status != "resolved" {
		t.Fatalf("ban resolve status=%s err=%v", status, err)
	}
	var banned, revoked bool
	if err = p.pool.QueryRow(ctx, `SELECT banned FROM im_users WHERE id=$1`, u1).Scan(&banned); err != nil || !banned {
		t.Fatalf("banned=%v err=%v", banned, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM im_refresh_sessions WHERE id=$1`, refreshID).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("revoked=%v err=%v", revoked, err)
	}

	usersPage, total, next, err := p.ListAdminUsers(ctx, suffix, "", "", 1)
	if err != nil || len(usersPage) != 1 || total != 2 || next == "" {
		t.Fatalf("admin page len=%d total=%d next=%q err=%v", len(usersPage), total, next, err)
	}

	var outboxID int64
	if err = p.pool.QueryRow(ctx, `INSERT INTO im_event_outbox(event_type,aggregate_id,payload) VALUES('runtime.recovered',$1,'{"recovered":true}') RETURNING id`, cid).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	delivered := make(chan struct{}, 1)
	runErr := make(chan error, 1)
	go func() {
		runErr <- p.RunEvents(runCtx, func(_ []string, typ string, payload map[string]any) {
			if typ == "runtime.recovered" && payload["recovered"] == true {
				select {
				case delivered <- struct{}{}:
				default:
				}
			}
		})
	}()
	select {
	case <-delivered:
	case err = <-runErr:
		cancel()
		t.Fatalf("event subscriber stopped: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("pending outbox event was not recovered")
	}
	var published string
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = p.pool.QueryRow(ctx, `SELECT status FROM im_event_outbox WHERE id=$1`, outboxID).Scan(&published)
		if err == nil && published == "published" {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("outbox status=%q err=%v", published, err)
		}
		select {
		case err = <-runErr:
			cancel()
			t.Fatalf("event subscriber stopped before publishing: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
}

func TestPostgresCallLifecycleTimeoutAndAdminMetadata(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u1, u2, cid := "call_a_"+suffix, "call_b_"+suffix, "call_conv_"+suffix
	now := time.Now()
	t.Cleanup(func() {
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_conversations WHERE id=$1`, cid)
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_users WHERE id=ANY($1::text[])`, []string{u1, u2})
	})
	for _, uid := range []string{u1, u2} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$1,$1,$2)`, uid, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,created_at,updated_at) VALUES($1,'direct',$2,$2)`, cid, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'member',$4),($1,$3,'member',$4)`, cid, u1, u2, now); err != nil {
		t.Fatal(err)
	}

	callID := "pg-call-" + suffix
	invite := CallInvite{ID: callID, ConversationID: cid, CallerID: u1, CalleeID: u2, MediaType: "video", InvitedAt: now, ExpiresAt: now.Add(30 * time.Second)}
	call, duplicate, err := p.InviteCall(ctx, invite)
	if err != nil || duplicate || call.Status != "invited" {
		t.Fatalf("invite call=%+v duplicate=%v err=%v", call, duplicate, err)
	}
	var invitePush string
	if err = p.pool.QueryRow(ctx, `SELECT payload::text FROM im_push_outbox WHERE user_id=$1 AND event_type='call.invited' ORDER BY id DESC LIMIT 1`, u2).Scan(&invitePush); err != nil || !strings.Contains(invitePush, callID) || !strings.Contains(invitePush, cid) || !strings.Contains(invitePush, "video") {
		t.Fatalf("call invite push=%s err=%v", invitePush, err)
	}
	var inviteSync int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_sync_events WHERE user_id=$1 AND event_type='call.invited' AND payload->>'callId'=$2`, u2, callID).Scan(&inviteSync); err != nil || inviteSync != 1 {
		t.Fatalf("call invite sync=%d err=%v", inviteSync, err)
	}
	_, duplicate, err = p.InviteCall(ctx, invite)
	if err != nil || !duplicate {
		t.Fatalf("invite retry duplicate=%v err=%v", duplicate, err)
	}
	if _, _, err = p.TransitionCall(ctx, callID, u1, "accept", "", now.Add(time.Second)); err != ErrConflict {
		t.Fatalf("caller accepted err=%v", err)
	}
	call, duplicate, err = p.TransitionCall(ctx, callID, u2, "accept", "", now.Add(time.Second))
	if err != nil || duplicate || call.Status != "accepted" || call.AcceptedAt == nil {
		t.Fatalf("accept call=%+v duplicate=%v err=%v", call, duplicate, err)
	}
	_, duplicate, err = p.TransitionCall(ctx, callID, u2, "accept", "", now.Add(2*time.Second))
	if err != nil || !duplicate {
		t.Fatalf("accept retry duplicate=%v err=%v", duplicate, err)
	}
	call, _, err = p.TransitionCall(ctx, callID, u1, "hangup", "completed", now.Add(4*time.Second))
	if err != nil || call.Status != "ended" || call.EndReason != "completed" || call.DurationSeconds < 3 {
		t.Fatalf("hangup call=%+v err=%v", call, err)
	}

	expiredID := "pg-expired-call-" + suffix
	_, _, err = p.InviteCall(ctx, CallInvite{ID: expiredID, ConversationID: cid, CallerID: u1, CalleeID: u2, MediaType: "audio", InvitedAt: now.Add(-time.Minute), ExpiresAt: now.Add(-30 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := p.ExpireCalls(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	foundMissed := false
	for _, item := range expired {
		if item.ID == expiredID && item.Status == "missed" && item.EndReason == "timeout" {
			foundMissed = true
		}
	}
	if !foundMissed {
		t.Fatalf("expired call missing: %+v", expired)
	}
	lazyExpiredID := "pg-lazy-expired-call-" + suffix
	_, _, err = p.InviteCall(ctx, CallInvite{ID: lazyExpiredID, ConversationID: cid, CallerID: u1, CalleeID: u2, MediaType: "audio", InvitedAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(-90 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	lazyExpired, err := p.GetCall(ctx, lazyExpiredID, u1, now)
	if err != nil || lazyExpired.Status != "missed" || lazyExpired.EndReason != "timeout" {
		t.Fatalf("lazy expired call=%+v err=%v", lazyExpired, err)
	}
	for _, userID := range []string{u1, u2} {
		for _, expected := range []struct {
			event  string
			callID string
		}{
			{event: "call.accepted", callID: callID},
			{event: "call.ended", callID: callID},
			{event: "call.timeout", callID: expiredID},
			{event: "call.timeout", callID: lazyExpiredID},
		} {
			var count, unsafeCount int
			if err = p.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE payload::text ILIKE '%sdp%' OR payload::text ILIKE '%candidate%') FROM im_sync_events WHERE user_id=$1 AND event_type=$2 AND payload->>'callId'=$3`, userID, expected.event, expected.callID).Scan(&count, &unsafeCount); err != nil || count != 1 || unsafeCount != 0 {
				t.Fatalf("durable call state user=%s event=%s call=%s count=%d unsafe=%d err=%v", userID, expected.event, expected.callID, count, unsafeCount, err)
			}
		}
		events, cursor, more, syncErr := p.ListSync(ctx, userID, 0, 100)
		if syncErr != nil || len(events) == 0 || cursor == 0 || more {
			t.Fatalf("reconnect sync user=%s events=%d cursor=%d more=%v err=%v", userID, len(events), cursor, more, syncErr)
		}
		seen := map[string]bool{}
		for _, event := range events {
			if value, _ := event.Payload["callId"].(string); value == callID || value == expiredID || value == lazyExpiredID {
				seen[event.Type+":"+value] = true
			}
		}
		for _, key := range []string{"call.accepted:" + callID, "call.ended:" + callID, "call.timeout:" + expiredID, "call.timeout:" + lazyExpiredID} {
			if !seen[key] {
				t.Fatalf("reconnect sync user=%s missing %s in %+v", userID, key, seen)
			}
		}
	}
	items, total, _, err := p.ListAdminCalls(ctx, suffix, "", "", 10)
	if err != nil || total != 3 || len(items) != 3 {
		t.Fatalf("admin calls len=%d total=%d err=%v", len(items), total, err)
	}
}

func TestPostgresFriendStateMachineSyncPushPrivacyAndMetadata(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u1, u2 := "friend_a_"+suffix, "friend_b_"+suffix
	now := time.Now()
	for _, uid := range []string{u1, u2} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$1,$1,$2)`, uid, now); err != nil {
			t.Fatal(err)
		}
	}
	request := &model.FriendRequest{ID: "friend_req_" + suffix, FromUserID: u1, ToUserID: u2, Message: "private verification text", Source: "search", Status: "pending", CreatedAt: now, ExpiresAt: now.Add(time.Hour), UpdatedAt: now}
	created, duplicate, err := p.CreateFriendRequest(ctx, request)
	if err != nil || duplicate || created.Source != "search" {
		t.Fatalf("create=%+v duplicate=%v err=%v", created, duplicate, err)
	}
	retry := *request
	retry.ID = "friend_retry_" + suffix
	created, duplicate, err = p.CreateFriendRequest(ctx, &retry)
	if err != nil || !duplicate || created.ID != request.ID {
		t.Fatalf("retry=%+v duplicate=%v err=%v", created, duplicate, err)
	}
	var syncA, syncB int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_sync_events WHERE user_id=$1 AND event_type LIKE 'friend.%'`, u1).Scan(&syncA); err != nil {
		t.Fatal(err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_sync_events WHERE user_id=$1 AND event_type LIKE 'friend.%'`, u2).Scan(&syncB); err != nil {
		t.Fatal(err)
	}
	if syncA != 1 || syncB != 1 {
		t.Fatalf("initial sync a=%d b=%d", syncA, syncB)
	}
	var pushText string
	if err = p.pool.QueryRow(ctx, `SELECT payload::text FROM im_push_outbox WHERE user_id=$1 AND event_type='friend.request' ORDER BY id DESC LIMIT 1`, u2).Scan(&pushText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pushText, "private verification") || strings.Contains(pushText, "search") {
		t.Fatalf("private push payload=%s", pushText)
	}
	if _, _, err = p.TransitionFriendRequest(ctx, request.ID, u1, "accept", now.Add(time.Second)); err != ErrForbidden {
		t.Fatalf("sender accept err=%v", err)
	}
	resolved, duplicate, err := p.TransitionFriendRequest(ctx, request.ID, u2, "reject", now.Add(time.Second))
	if err != nil || duplicate || resolved.Status != "rejected" {
		t.Fatalf("reject=%+v duplicate=%v err=%v", resolved, duplicate, err)
	}
	_, duplicate, err = p.TransitionFriendRequest(ctx, request.ID, u2, "reject", now.Add(2*time.Second))
	if err != nil || !duplicate {
		t.Fatalf("reject retry duplicate=%v err=%v", duplicate, err)
	}
	second := &model.FriendRequest{ID: "friend_accept_" + suffix, FromUserID: u1, ToUserID: u2, Message: "again", Source: "search", Status: "pending", CreatedAt: now.Add(3 * time.Second), ExpiresAt: now.Add(time.Hour), UpdatedAt: now.Add(3 * time.Second)}
	if _, _, err = p.CreateFriendRequest(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.TransitionFriendRequest(ctx, second.ID, u2, "accept", now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = p.UpdateFriendMetadata(ctx, u1, u2, FriendMetadata{Remark: "Neighbor", Tags: []string{"local", "photo"}}, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	friends, err := p.ListFriends(ctx, u1)
	if err != nil || len(friends) != 1 || friends[0].Remark != "Neighbor" || len(friends[0].Tags) != 2 {
		t.Fatalf("friends=%+v err=%v", friends, err)
	}
	if err = p.DeleteFriend(ctx, u1, u2, now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	friends, err = p.ListFriends(ctx, u2)
	if err != nil || len(friends) != 0 {
		t.Fatalf("deleted friends=%+v err=%v", friends, err)
	}
	expired := &model.FriendRequest{ID: "friend_expired_" + suffix, FromUserID: u1, ToUserID: u2, Source: "qr", Status: "pending", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Hour)}
	if _, _, err = p.CreateFriendRequest(ctx, expired); err != nil {
		t.Fatal(err)
	}
	expiredItems, err := p.ExpireFriendRequests(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range expiredItems {
		if item.ID == expired.ID && item.Status == "expired" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expired missing: %+v", expiredItems)
	}
	third := &model.FriendRequest{ID: "friend_block_" + suffix, FromUserID: u1, ToUserID: u2, Source: "card", Status: "pending", CreatedAt: now.Add(7 * time.Second), ExpiresAt: now.Add(time.Hour), UpdatedAt: now.Add(7 * time.Second)}
	if _, _, err = p.CreateFriendRequest(ctx, third); err != nil {
		t.Fatal(err)
	}
	if err = p.SetFriendBlock(ctx, u2, u1, true, now.Add(8*time.Second)); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = p.pool.QueryRow(ctx, `SELECT status FROM im_friend_requests WHERE id=$1`, third.ID).Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("blocked request status=%s err=%v", status, err)
	}
	blockedReq := *third
	blockedReq.ID = "friend_blocked_again_" + suffix
	if _, _, err = p.CreateFriendRequest(ctx, &blockedReq); err != ErrForbidden {
		t.Fatalf("blocked create err=%v", err)
	}
}

func TestPostgresUserHandlePolicyAndExactIdentifierSearch(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	uid, phone := "usr_handle_"+suffix, "phone_"+suffix
	u, err := p.RegisterPasswordUser(ctx, phone, "Handle User", uid, "hash", time.Now())
	if err != nil || !strings.HasPrefix(u.Handle, "ll_") || u.HandleChangeCount != 0 {
		t.Fatalf("registered=%+v err=%v", u, err)
	}
	first, second := "neighbor_"+suffix[len(suffix)-6:], "resident_"+suffix[len(suffix)-6:]
	u, err = p.UpdateUserProfile(ctx, uid, UserProfileUpdate{Handle: &first})
	if err != nil || u.HandleChangeCount != 1 {
		t.Fatalf("first update=%+v err=%v", u, err)
	}
	u, err = p.UpdateUserProfile(ctx, uid, UserProfileUpdate{Handle: &first})
	if err != nil || u.HandleChangeCount != 1 {
		t.Fatalf("idempotent update=%+v err=%v", u, err)
	}
	u, err = p.UpdateUserProfile(ctx, uid, UserProfileUpdate{Handle: &second})
	if err != nil || u.HandleChangeCount != 2 {
		t.Fatalf("second update=%+v err=%v", u, err)
	}
	third := "blocked_" + suffix[len(suffix)-6:]
	if _, err = p.UpdateUserProfile(ctx, uid, UserProfileUpdate{Handle: &third}); err != ErrForbidden {
		t.Fatalf("third update err=%v", err)
	}
	items, err := p.SearchUsersByIdentifier(ctx, second, "handle", 20)
	if err != nil || len(items) != 1 || items[0].ID != uid || items[0].Phone != "" {
		t.Fatalf("handle search=%+v err=%v", items, err)
	}
	items, err = p.SearchUsersByIdentifier(ctx, phone, "phone", 20)
	if err != nil || len(items) != 1 || items[0].ID != uid || items[0].Phone != "" {
		t.Fatalf("phone search=%+v err=%v", items, err)
	}
}

func TestPostgresMediaAccessRequiresOwnerOrCurrentReferencedConversationMember(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner, member, outsider := "media_owner_"+suffix, "media_member_"+suffix, "media_out_"+suffix
	now := time.Now()
	for _, uid := range []string{owner, member, outsider} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$1,$1,$2)`, uid, now); err != nil {
			t.Fatal(err)
		}
	}
	cid, mediaID := "media_group_"+suffix, "media_"+suffix
	if _, err = p.CreateGroupRecord(ctx, cid, owner, "Media Group", []string{member}, now); err != nil {
		t.Fatal(err)
	}
	if err = p.CreateMedia(ctx, Media{ID: mediaID, OwnerID: owner, ObjectKey: "objects/" + mediaID, MIME: "image/jpeg", Size: 10, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = p.SendMessage(ctx, MessageInput{UserID: owner, ConversationID: cid, ClientMsgID: "media-reference", Type: "image", Body: map[string]any{"mediaId": mediaID}, MessageID: "media_message_" + suffix, CreatedAt: now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	for label, uid := range map[string]string{"owner": owner, "member": member} {
		allowed, accessErr := p.CanAccessMedia(ctx, uid, mediaID)
		if accessErr != nil || !allowed {
			t.Fatalf("%s allowed=%v err=%v", label, allowed, accessErr)
		}
	}
	if allowed, accessErr := p.CanAccessMedia(ctx, outsider, mediaID); accessErr != nil || allowed {
		t.Fatalf("outsider allowed=%v err=%v", allowed, accessErr)
	}
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: owner, ConversationID: cid, TargetID: member, Action: "remove", At: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if allowed, accessErr := p.CanAccessMedia(ctx, member, mediaID); accessErr != nil || allowed {
		t.Fatalf("removed member allowed=%v err=%v", allowed, accessErr)
	}
}

func TestPostgresAccountDeletionAnonymizesAndPreservesMessageReferences(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner, successor, originalPhone := "delete_owner_"+suffix, "delete_successor_"+suffix, "139"+suffix
	now := time.Now()
	for _, data := range []struct{ id, phone string }{{owner, originalPhone}, {successor, "138" + suffix}} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,handle,created_at) VALUES($1,$2,$1,'ll_'||right(md5($1),20),$3)`, data.id, data.phone, now); err != nil {
			t.Fatal(err)
		}
	}
	cid := "delete_group_" + suffix
	if _, err = p.CreateGroupRecord(ctx, cid, owner, "Delete Test", []string{successor}, now); err != nil {
		t.Fatal(err)
	}
	messageID := "delete_message_" + suffix
	if _, _, _, err = p.SendMessage(ctx, MessageInput{UserID: owner, ConversationID: cid, ClientMsgID: "before-delete", Type: "text", Body: map[string]any{"text": "preserved"}, MessageID: messageID, CreatedAt: now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err = p.CreateRefreshSession(ctx, "delete_refresh_"+suffix, owner, []byte("hash"), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = p.RegisterDevice(ctx, owner, Device{ID: "delete_device_" + suffix, Platform: "ios", Provider: "apns", PushToken: "delete_token_" + suffix}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_friendships(user_id,friend_user_id) VALUES($1,$2),($2,$1)`, owner, successor); err != nil {
		t.Fatal(err)
	}
	if duplicate, deleteErr := p.DeleteAccount(ctx, owner, now.Add(time.Second)); deleteErr != ErrConflict || duplicate {
		t.Fatalf("owner deletion duplicate=%v err=%v", duplicate, deleteErr)
	}
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: owner, ConversationID: cid, TargetID: successor, Action: "transfer", At: now.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if duplicate, deleteErr := p.DeleteAccount(ctx, owner, now.Add(3*time.Second)); deleteErr != nil || duplicate {
		t.Fatalf("delete duplicate=%v err=%v", duplicate, deleteErr)
	}
	if duplicate, deleteErr := p.DeleteAccount(ctx, owner, now.Add(4*time.Second)); deleteErr != nil || !duplicate {
		t.Fatalf("retry duplicate=%v err=%v", duplicate, deleteErr)
	}
	var phone, handle, name string
	var banned bool
	var deletedAt *time.Time
	if err = p.pool.QueryRow(ctx, `SELECT phone,handle,name,banned,deleted_at FROM im_users WHERE id=$1`, owner).Scan(&phone, &handle, &name, &banned, &deletedAt); err != nil || phone == originalPhone || !strings.HasPrefix(handle, "deleted_") || name != "已注销用户" || !banned || deletedAt == nil {
		t.Fatalf("anonymized phone=%q handle=%q name=%q banned=%v deletedAt=%v err=%v", phone, handle, name, banned, deletedAt, err)
	}
	for label, query := range map[string]string{
		"active refresh": `SELECT count(*) FROM im_refresh_sessions WHERE user_id=$1 AND revoked_at IS NULL`,
		"devices":        `SELECT count(*) FROM im_devices WHERE user_id=$1`,
		"friendships":    `SELECT count(*) FROM im_friendships WHERE user_id=$1 OR friend_user_id=$1`,
		"memberships":    `SELECT count(*) FROM im_members WHERE user_id=$1`,
	} {
		var count int
		if err = p.pool.QueryRow(ctx, query, owner).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", label, count, err)
		}
	}
	var senderID string
	if err = p.pool.QueryRow(ctx, `SELECT sender_id FROM im_messages WHERE id=$1`, messageID).Scan(&senderID); err != nil || senderID != owner {
		t.Fatalf("message reference sender=%q err=%v", senderID, err)
	}
	var audits int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE actor_id=$1 AND action='account.deleted'`, owner).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("account deletion audits=%d err=%v", audits, err)
	}
	newUserID := "replacement_" + suffix
	if user, createErr := p.LoginOrCreateUser(ctx, originalPhone, "Replacement", newUserID, now.Add(5*time.Second)); createErr != nil || user.ID != newUserID {
		t.Fatalf("phone reuse user=%+v err=%v", user, createErr)
	}
}

func TestPostgresGroupManagementPermissionsInvitesQRAndAudit(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	users := []string{"grp_owner_" + suffix, "grp_admin_" + suffix, "grp_member_" + suffix, "grp_qr_" + suffix}
	now := time.Now()
	for _, uid := range users {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at)VALUES($1,$1,$1,$2)`, uid, now); err != nil {
			t.Fatal(err)
		}
	}
	cid := "grp_conv_" + suffix
	c, err := p.CreateGroupRecord(ctx, cid, users[0], "Initial", []string{users[1]}, now)
	if err != nil || c.Title != "Initial" {
		t.Fatalf("create=%+v err=%v", c, err)
	}
	g, err := p.GetGroupProfile(ctx, users[1], cid)
	if err != nil || g.OwnerID != users[0] {
		t.Fatalf("profile=%+v err=%v", g, err)
	}
	name, policy, allow := "Renamed", "qr", false
	if _, err = p.UpdateGroupProfile(ctx, users[1], cid, GroupProfileUpdate{Name: &name}, now.Add(time.Second)); err != ErrForbidden {
		t.Fatalf("member update=%v", err)
	}
	avatarID := "grp_avatar_" + suffix
	if err = p.CreateMedia(ctx, Media{ID: avatarID, OwnerID: users[0], ObjectKey: "objects/" + avatarID, MIME: "image/jpeg", Size: 128, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	g, err = p.UpdateGroupProfile(ctx, users[0], cid, GroupProfileUpdate{Name: &name, AvatarMediaID: &avatarID, JoinPolicy: &policy, AllowMemberAddFriend: &allow, RotateQR: true}, now.Add(2*time.Second))
	if err != nil || g.QRToken == "" || g.AllowMemberAddFriend || g.AvatarURL != "/v1/media/"+avatarID {
		t.Fatalf("update=%+v err=%v", g, err)
	}
	foreignAvatarID := "grp_foreign_avatar_" + suffix
	if err = p.CreateMedia(ctx, Media{ID: foreignAvatarID, OwnerID: users[1], ObjectKey: "objects/" + foreignAvatarID, MIME: "image/png", Size: 64, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.UpdateGroupProfile(ctx, users[0], cid, GroupProfileUpdate{AvatarMediaID: &foreignAvatarID}, now.Add(2500*time.Millisecond)); err != ErrForbidden {
		t.Fatalf("foreign group avatar update=%v", err)
	}
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: users[0], ConversationID: cid, TargetID: users[1], Action: "role", Role: "admin", At: now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	g, err = p.SetGroupAnnouncement(ctx, users[1], cid, "Welcome", now.Add(4*time.Second))
	if err != nil || g.AnnouncementVersion != 1 {
		t.Fatalf("announcement=%+v err=%v", g, err)
	}
	if err = p.MarkGroupAnnouncementRead(ctx, users[1], cid, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	invite := &model.GroupInvite{ID: "ginv_" + suffix, ConversationID: cid, InviterID: users[1], InviteeID: users[2], Source: "invite", Status: "pending", CreatedAt: now.Add(6 * time.Second), ExpiresAt: now.Add(time.Hour), UpdatedAt: now.Add(6 * time.Second)}
	created, dup, err := p.CreateGroupInvite(ctx, invite)
	if err != nil || dup || created.Status != "pending" {
		t.Fatalf("invite=%+v dup=%v err=%v", created, dup, err)
	}
	_, dup, err = p.CreateGroupInvite(ctx, invite)
	if err != nil || !dup {
		t.Fatalf("retry dup=%v err=%v", dup, err)
	}
	listedInvites, err := p.ListGroupInvites(ctx, users[2], "pending", 20)
	if err != nil || len(listedInvites) != 1 {
		t.Fatalf("list pending invites=%+v err=%v", listedInvites, err)
	}
	listedInvite, _ := listedInvites[0]["invite"].(*model.GroupInvite)
	listedInviter, _ := listedInvites[0]["inviter"].(*model.User)
	if listedInvite == nil || listedInvite.ID != invite.ID || listedInviter == nil || listedInviter.ID != users[1] || listedInviter.Phone != "" || listedInvites[0]["groupName"] != "Renamed" || listedInvites[0]["outgoing"] != false {
		t.Fatalf("unsafe/incomplete group invite projection=%+v", listedInvites[0])
	}
	accepted, dup, err := p.TransitionGroupInvite(ctx, invite.ID, users[2], "accept", now.Add(7*time.Second))
	if err != nil || dup || accepted.Status != "accepted" {
		t.Fatalf("accept=%+v dup=%v err=%v", accepted, dup, err)
	}
	listedInvites, err = p.ListGroupInvites(ctx, users[2], "pending", 20)
	if err != nil || len(listedInvites) != 0 {
		t.Fatalf("accepted invite remained pending=%+v err=%v", listedInvites, err)
	}
	groupFriendRequest := &model.FriendRequest{ID: "group_friend_" + suffix, FromUserID: users[1], ToUserID: users[2], Source: "group", SourceID: cid, Status: "pending", CreatedAt: now.Add(7 * time.Second), ExpiresAt: now.Add(time.Hour), UpdatedAt: now.Add(7 * time.Second)}
	if _, _, err = p.CreateFriendRequest(ctx, groupFriendRequest); err != ErrForbidden {
		t.Fatalf("disabled group member add friend err=%v", err)
	}
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: users[2], ConversationID: cid, TargetID: users[2], Action: "nickname", Nickname: "小三", At: now.Add(8 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	until := now.Add(time.Hour)
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: users[1], ConversationID: cid, TargetID: users[2], Action: "mute", MutedUntil: &until, At: now.Add(9 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	members, err := p.ListConversationMembers(ctx, users[0], cid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range members {
		if m.UserID == users[2] && m.GroupNickname == "小三" && m.MutedUntil != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("members=%+v", members)
	}
	if err = p.JoinGroupByQR(ctx, users[3], g.QRToken, now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	groupConversations, err := p.ListConversations(ctx, users[0], 20)
	if err != nil {
		t.Fatal(err)
	}
	foundGroupProjection := false
	for _, item := range groupConversations {
		conversation := item["conversation"].(*model.Conversation)
		if conversation.ID != cid {
			continue
		}
		projected, ok := item["members"].([]*model.ConversationMember)
		if !ok || len(projected) != 4 {
			t.Fatalf("group conversation members=%T %+v", item["members"], item["members"])
		}
		for _, member := range projected {
			rawMember, _ := json.Marshal(member)
			if member.UserID == "" || member.Name == "" || strings.Contains(string(rawMember), `"phone"`) {
				t.Fatalf("unsafe group conversation member=%+v", member)
			}
		}
		foundGroupProjection = true
	}
	if !foundGroupProjection {
		t.Fatalf("group conversation projection missing: %+v", groupConversations)
	}
	allMuted := now.Add(time.Hour)
	if _, err = p.UpdateGroupProfile(ctx, users[0], cid, GroupProfileUpdate{AllMutedUntil: &allMuted}, now.Add(11*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = p.SendMessage(ctx, MessageInput{UserID: users[3], ConversationID: cid, ClientMsgID: "muted", Type: "text", Body: map[string]any{"text": "no"}, MessageID: "grp_msg_" + suffix, CreatedAt: now.Add(12 * time.Second).UnixMilli()}); err != ErrForbidden {
		t.Fatalf("mute send=%v", err)
	}
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: users[0], ConversationID: cid, TargetID: users[2], Action: "transfer", At: now.Add(13 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err = p.DisbandGroupRecord(ctx, users[0], cid, "old", now.Add(14*time.Second)); err != ErrForbidden {
		t.Fatalf("old owner=%v", err)
	}
	if err = p.DisbandGroupRecord(ctx, users[2], cid, "closed", now.Add(15*time.Second)); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE target_id=$1 AND action LIKE 'group.%'`, cid).Scan(&audits); err != nil || audits < 8 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
	var systemMessages int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_messages WHERE conversation_id=$1 AND message_type='system'`, cid).Scan(&systemMessages); err != nil || systemMessages < 8 {
		t.Fatalf("system messages=%d err=%v", systemMessages, err)
	}
}

func TestAnnouncementPostgresLifecycleAndPushOutbox(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	uid := "announce_user_" + suffix
	now := time.Now()
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$1,$1,$2)`, uid, now); err != nil {
		t.Fatal(err)
	}
	defer p.pool.Exec(ctx, `DELETE FROM im_users WHERE id=$1`, uid)
	announcementID, scheduledID := "announce_"+suffix, "scheduled_"+suffix
	defer func() {
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_push_outbox WHERE payload->>'announcementId'=ANY($1::text[])`, []string{announcementID, scheduledID})
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_audits WHERE target_type='announcement' AND target_id=ANY($1::text[])`, []string{announcementID, scheduledID})
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_announcements WHERE id=ANY($1::text[])`, []string{announcementID, scheduledID})
	}()
	input := AnnouncementInput{ID: announcementID, Title: "Release", Content: "New version", Status: "draft", Pinned: true, TargetType: "users", TargetUserIDs: []string{uid}, ActorID: "admin"}
	created, err := p.CreateAnnouncement(ctx, input, now)
	if err != nil || created.Status != "draft" {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	items, err := p.ListAnnouncements(ctx, uid, now)
	if err != nil || len(items) != 0 {
		t.Fatalf("draft list=%v err=%v", items, err)
	}
	published, err := p.PublishAnnouncement(ctx, input.ID, "admin", true, now.Add(time.Second))
	if err != nil || published.Status != "published" {
		t.Fatalf("publish=%+v err=%v", published, err)
	}
	var pushes int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_push_outbox WHERE user_id=$1 AND event_type='announcement.published' AND payload->>'announcementId'=$2`, uid, input.ID).Scan(&pushes); err != nil || pushes != 1 {
		t.Fatalf("pushes=%d err=%v", pushes, err)
	}
	if err = p.MarkAnnouncementRead(ctx, uid, input.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	items, err = p.ListAnnouncements(ctx, uid, now.Add(3*time.Second))
	if err != nil || len(items) != 1 || items[0].ReadAt == nil {
		t.Fatalf("published list=%v err=%v", items, err)
	}
	if _, err = p.WithdrawAnnouncement(ctx, input.ID, "admin", now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	items, err = p.ListAnnouncements(ctx, uid, now.Add(5*time.Second))
	if err != nil || len(items) != 0 {
		t.Fatalf("withdrawn list=%v err=%v", items, err)
	}
	if err = p.DeleteAnnouncement(ctx, input.ID, "admin", now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	due := now.Add(10 * time.Second)
	scheduled := AnnouncementInput{ID: scheduledID, Title: "Scheduled", Content: "Scheduled release", Status: "scheduled", TargetType: "users", TargetUserIDs: []string{uid}, ScheduledAt: &due, PushOnPublish: true, ActorID: "admin"}
	if _, err = p.CreateAnnouncement(ctx, scheduled, now); err != nil {
		t.Fatal(err)
	}
	count, err := p.PromoteDueAnnouncements(ctx, due.Add(time.Second))
	if err != nil || count != 1 {
		t.Fatalf("scheduled promotion count=%d err=%v", count, err)
	}
	count, err = p.PromoteDueAnnouncements(ctx, due.Add(2*time.Second))
	if err != nil || count != 0 {
		t.Fatalf("idempotent promotion count=%d err=%v", count, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_push_outbox WHERE user_id=$1 AND event_type='announcement.published' AND payload->>'announcementId'=$2`, uid, scheduled.ID).Scan(&pushes); err != nil || pushes != 1 {
		t.Fatalf("scheduled pushes=%d err=%v", pushes, err)
	}
	if _, err = p.WithdrawAnnouncement(ctx, scheduled.ID, "admin", due.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = p.DeleteAnnouncement(ctx, scheduled.ID, "admin", due.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresMessageCollaborationLifecycle(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner, admin, member, outsider := "collab_owner_"+suffix, "collab_admin_"+suffix, "collab_member_"+suffix, "collab_outsider_"+suffix
	cid, mid := "collab_group_"+suffix, "collab_message_"+suffix
	now := time.Now().UTC()
	users := []string{owner, admin, member, outsider}
	defer func() {
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_audits WHERE actor_id=ANY($1::text[])`, users)
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_conversations WHERE id=$1`, cid)
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_users WHERE id=ANY($1::text[])`, users)
	}()
	for index, userID := range users {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$2,$1,$3)`, userID, fmt.Sprintf("188%08d", index)+suffix[len(suffix)-4:], now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,created_at,updated_at) VALUES($1,'group','Collaboration',$2,$2)`, cid, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_groups(conversation_id,owner_id,updated_at) VALUES($1,$2,$3)`, cid, owner, now); err != nil {
		t.Fatal(err)
	}
	for userID, role := range map[string]string{owner: "owner", admin: "admin", member: "member"} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,$3,$4)`, cid, userID, role, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_devices(id,user_id,platform,provider,push_token,updated_at) VALUES($1,$2,'ios','apns',$3,$4)`, "collab_device_"+suffix, member, "collab_token_"+suffix, now); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = p.SendMessage(ctx, MessageInput{UserID: member, ConversationID: cid, ClientMsgID: "invalid-all-" + suffix, Type: "text", Body: map[string]any{"text": "hello", "mentionAll": true}, MentionAll: true, MessageID: "invalid_all_" + suffix, CreatedAt: now.UnixMilli()}); err != ErrForbidden {
		t.Fatalf("member @all error=%v", err)
	}
	if _, _, _, err = p.SendMessage(ctx, MessageInput{UserID: owner, ConversationID: cid, ClientMsgID: "invalid-user-" + suffix, Type: "text", Body: map[string]any{"text": "hello", "mentions": []string{outsider}}, Mentions: []string{outsider}, MessageID: "invalid_user_" + suffix, CreatedAt: now.UnixMilli()}); err != ErrForbidden {
		t.Fatalf("outside mention error=%v", err)
	}
	message, duplicate, _, err := p.SendMessage(ctx, MessageInput{UserID: owner, ConversationID: cid, ClientMsgID: "valid-" + suffix, Type: "text", Body: map[string]any{"text": "first draft", "mentions": []string{member}}, Mentions: []string{member}, MessageID: mid, CreatedAt: now.UnixMilli()})
	if err != nil || duplicate || message.ID != mid {
		t.Fatalf("send=%+v duplicate=%v err=%v", message, duplicate, err)
	}
	drainMessageFanout(t, p, ctx)
	var pushPayload []byte
	if err = p.pool.QueryRow(ctx, `SELECT payload FROM im_push_outbox WHERE user_id=$1 AND event_type='message.created' ORDER BY id DESC LIMIT 1`, member).Scan(&pushPayload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(pushPayload), "first draft") || strings.Contains(string(pushPayload), member) || !strings.Contains(string(pushPayload), `"mentioned": true`) {
		t.Fatalf("unsafe or incomplete push payload=%s", pushPayload)
	}
	edited, duplicate, err := p.EditMessage(ctx, owner, mid, "edit-1", map[string]any{"text": "final 100%_searchable", "mentions": []string{member}}, now.Add(time.Second), 2*time.Minute)
	if err != nil || duplicate || edited.EditVersion != 1 || edited.EditedAt == nil {
		t.Fatalf("edit=%+v duplicate=%v err=%v", edited, duplicate, err)
	}
	if _, duplicate, err = p.EditMessage(ctx, owner, mid, "edit-1", map[string]any{"text": "final 100%_searchable", "mentions": []string{member}}, now.Add(2*time.Second), 2*time.Minute); err != nil || !duplicate {
		t.Fatalf("edit retry duplicate=%v err=%v", duplicate, err)
	}
	if _, duplicate, err = p.EditMessage(ctx, owner, mid, "noop-1", map[string]any{"text": "final 100%_searchable", "mentions": []string{member}}, now.Add(2*time.Second), 2*time.Minute); err != nil || !duplicate {
		t.Fatalf("no-op edit duplicate=%v err=%v", duplicate, err)
	}
	if _, _, err = p.EditMessage(ctx, owner, mid, "noop-1", map[string]any{"text": "different body"}, now.Add(2*time.Second), 2*time.Minute); err != ErrConflict {
		t.Fatalf("reused edit id error=%v", err)
	}
	if _, _, err = p.EditMessage(ctx, member, mid, "edit-other", map[string]any{"text": "hijack"}, now.Add(3*time.Second), 2*time.Minute); err != ErrForbidden {
		t.Fatalf("non-author edit error=%v", err)
	}
	edits, err := p.ListMessageEdits(ctx, member, mid)
	if err != nil || len(edits) != 2 || edits[0].Version != 0 || edits[1].Version != 1 {
		t.Fatalf("edits=%+v err=%v", edits, err)
	}
	reaction, duplicate, err := p.SetMessageReaction(ctx, member, mid, "👍", true, now.Add(4*time.Second))
	if err != nil || duplicate || reaction.Count != 1 || !reaction.ReactedByMe {
		t.Fatalf("member reaction=%+v duplicate=%v err=%v", reaction, duplicate, err)
	}
	reaction, duplicate, err = p.SetMessageReaction(ctx, admin, mid, "👍", true, now.Add(5*time.Second))
	if err != nil || duplicate || reaction.Count != 2 {
		t.Fatalf("admin reaction=%+v duplicate=%v err=%v", reaction, duplicate, err)
	}
	if _, duplicate, err = p.SetMessageReaction(ctx, admin, mid, "👍", true, now.Add(6*time.Second)); err != nil || !duplicate {
		t.Fatalf("reaction retry duplicate=%v err=%v", duplicate, err)
	}
	start := make(chan struct{})
	concurrentResults := make(chan model.MessageReactionSummary, 2)
	concurrentErrors := make(chan error, 2)
	var reactionWG sync.WaitGroup
	for _, actor := range []string{member, admin} {
		reactionWG.Add(1)
		go func(actorID string) {
			defer reactionWG.Done()
			<-start
			item, wasDuplicate, reactionErr := p.SetMessageReaction(ctx, actorID, mid, "🎉", true, now.Add(6*time.Second))
			if reactionErr != nil {
				concurrentErrors <- reactionErr
				return
			}
			if wasDuplicate {
				concurrentErrors <- fmt.Errorf("unexpected concurrent reaction duplicate for %s", actorID)
				return
			}
			concurrentResults <- item
		}(actor)
	}
	close(start)
	reactionWG.Wait()
	close(concurrentErrors)
	for reactionErr := range concurrentErrors {
		t.Fatal(reactionErr)
	}
	close(concurrentResults)
	counts := []int{}
	for item := range concurrentResults {
		counts = append(counts, item.Count)
	}
	sort.Ints(counts)
	if fmt.Sprint(counts) != "[1 2]" {
		t.Fatalf("concurrent reaction counts=%v want [1 2]", counts)
	}
	var persistedReactionCount int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_message_reactions WHERE message_id=$1 AND emoji='🎉'`, mid).Scan(&persistedReactionCount); err != nil || persistedReactionCount != 2 {
		t.Fatalf("persisted concurrent reactions=%d err=%v", persistedReactionCount, err)
	}
	if _, _, err = p.SetGroupMessagePin(ctx, member, cid, mid, true, now.Add(7*time.Second)); err != ErrForbidden {
		t.Fatalf("member pin error=%v", err)
	}
	if _, duplicate, err = p.SetGroupMessagePin(ctx, admin, cid, mid, true, now.Add(8*time.Second)); err != nil || duplicate {
		t.Fatalf("admin pin duplicate=%v err=%v", duplicate, err)
	}
	pins, err := p.ListGroupMessagePins(ctx, member, cid, 0, 10)
	if err != nil || len(pins) != 1 || pins[0].Message.ID != mid || len(pins[0].Message.Reactions) != 2 {
		t.Fatalf("pins=%+v err=%v", pins, err)
	}
	var thumbsUpCount int
	for _, summary := range pins[0].Message.Reactions {
		if summary.Emoji == "👍" {
			thumbsUpCount = summary.Count
		}
	}
	if thumbsUpCount != 2 {
		t.Fatalf("thumbs-up reaction count=%d want 2", thumbsUpCount)
	}
	results, err := p.SearchConversationMessages(ctx, member, cid, "SEARCHABLE", 0, 10)
	if err != nil || len(results) != 1 || results[0].ID != mid || results[0].EditVersion != 1 {
		t.Fatalf("search=%+v err=%v", results, err)
	}
	results, err = p.SearchConversationMessages(ctx, member, cid, "100%_", 0, 10)
	if err != nil || len(results) != 1 || results[0].ID != mid {
		t.Fatalf("literal wildcard search=%+v err=%v", results, err)
	}
	var searchIndex string
	if err = p.pool.QueryRow(ctx, `SELECT to_regclass('im_messages_text_search_trgm_idx')::text`).Scan(&searchIndex); err != nil || searchIndex != "im_messages_text_search_trgm_idx" {
		t.Fatalf("message search index=%q err=%v", searchIndex, err)
	}
	if _, err = p.SearchConversationMessages(ctx, outsider, cid, "searchable", 0, 10); err != ErrForbidden {
		t.Fatalf("outsider search error=%v", err)
	}
	if _, duplicate, err = p.SetGroupMessagePin(ctx, owner, cid, mid, false, now.Add(9*time.Second)); err != nil || duplicate {
		t.Fatalf("owner unpin duplicate=%v err=%v", duplicate, err)
	}
	var systemCount, editAuditCount int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_messages WHERE conversation_id=$1 AND message_type='system'`, cid).Scan(&systemCount); err != nil || systemCount != 2 {
		t.Fatalf("system messages=%d err=%v", systemCount, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE target_id=$1 AND action='message.edited'`, mid).Scan(&editAuditCount); err != nil || editAuditCount != 1 {
		t.Fatalf("edit audits=%d err=%v", editAuditCount, err)
	}
}

func TestPostgresInvalidatesOnlySupportedPushProviders(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	uid := "push_invalidation_user_" + suffix
	deviceIDs := []string{
		"push_invalidation_getui_" + suffix,
		"push_invalidation_voip_" + suffix,
		"push_invalidation_fcm_" + suffix,
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$2,'Push invalidation test',now())`, uid, "push_invalidation_"+suffix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_users WHERE id=$1`, uid)
	})
	providers := []string{"getui", "apns_voip", "fcm"}
	for index, deviceID := range deviceIDs {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_devices(id,user_id,platform,provider,push_token,notifications_enabled,updated_at) VALUES($1,$2,'ios',$3,$4,true,now())`, deviceID, uid, providers[index], "push_token_"+suffix+fmt.Sprint(index)); err != nil {
			t.Fatal(err)
		}
	}

	if err = p.InvalidatePushDevices(ctx, deviceIDs); err != nil {
		t.Fatal(err)
	}
	type deviceState struct {
		enabled bool
		token   string
	}
	states := make(map[string]deviceState, len(deviceIDs))
	rows, err := p.pool.Query(ctx, `SELECT provider,notifications_enabled,push_token FROM im_devices WHERE id=ANY($1::text[])`, deviceIDs)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var provider string
		var state deviceState
		if err = rows.Scan(&provider, &state.enabled, &state.token); err != nil {
			t.Fatal(err)
		}
		states[provider] = state
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"getui", "apns_voip"} {
		state := states[provider]
		if state.enabled || state.token != "" {
			t.Fatalf("provider %s was not invalidated: %+v", provider, state)
		}
	}
	fcm := states["fcm"]
	if !fcm.enabled || fcm.token == "" {
		t.Fatalf("unrelated provider was modified: %+v", fcm)
	}
}
