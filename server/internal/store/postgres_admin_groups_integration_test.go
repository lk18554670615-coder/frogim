package store

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/linli/im/server/internal/wukong"
)

func TestAdminGroupGovernancePersistsExactWukongSnapshot(t *testing.T) {
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
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	owner, admin, member, blocked := "gov_owner_"+suffix, "gov_admin_"+suffix, "gov_member_"+suffix, "gov_blocked_"+suffix
	groupID := "gov_group_" + suffix
	now := time.Now().UTC().Truncate(time.Millisecond)
	defer func() {
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_outbox WHERE aggregate_id=$1 OR payload->>'channel_id'=$1`, groupID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_audits WHERE target_id=$1 OR target_id LIKE $1||':%'`, groupID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_conversations WHERE id=$1`, groupID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_users WHERE id=ANY($1::text[])`, []string{owner, admin, member, blocked})
	}()
	for index, uid := range []string{owner, admin, member, blocked} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$2,$3,$4,$4)`, uid, "gov-phone-"+strconv.Itoa(index)+suffix, uid, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.CreateGroupRecord(ctx, groupID, owner, "治理测试群", []string{admin, member, blocked}, now); err != nil {
		t.Fatal(err)
	}
	if err = p.AdminApplyGroupMemberAction(ctx, AdminGroupMemberAction{ActorID: "admin_test", ConversationID: groupID, TargetID: admin, Action: "role", Role: "admin", Reason: "角色治理", At: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err = p.AdminSetGroupMuteAll(ctx, "admin_test", groupID, true, "永久禁言", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := p.LoadWukongChannelSnapshot(ctx, groupID, wukong.ChannelGroup)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snapshot.Allowlist, []string{admin, owner}) {
		t.Fatalf("allowlist=%v", snapshot.Allowlist)
	}
	if _, err = p.AuthorizeWukongMessage(ctx, WukongMessageRouteInput{UserID: member, ConversationID: groupID, Type: "text", Text: "blocked"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("muted member authorization=%v", err)
	}
	if _, err = p.AuthorizeWukongMessage(ctx, WukongMessageRouteInput{UserID: owner, ConversationID: groupID, Type: "text", Text: "owner allowed"}); err != nil {
		t.Fatalf("owner authorization=%v", err)
	}
	if err = p.AdminAddGroupBlacklist(ctx, "admin_test", groupID, blocked, "违规成员", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	snapshot, err = p.LoadWukongChannelSnapshot(ctx, groupID, wukong.ChannelGroup)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(snapshot.Denylist, blocked) || slices.Contains(snapshot.Subscribers, blocked) {
		t.Fatalf("snapshot after blacklist=%+v", snapshot)
	}
	if err = p.AddGroupMembers(ctx, owner, groupID, []string{blocked}, now.Add(4*time.Second)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("blacklisted add=%v", err)
	}
	if err = p.AdminRemoveGroupBlacklist(ctx, "admin_test", groupID, blocked, "复核解除", now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	var restored bool
	if err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_members WHERE conversation_id=$1 AND user_id=$2)`, groupID, blocked).Scan(&restored); err != nil || restored {
		t.Fatalf("blacklist removal restored=%v err=%v", restored, err)
	}
	if err = p.AdminSetGroupBan(ctx, "admin_test", groupID, true, "风险处置", now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	snapshot, err = p.LoadWukongChannelSnapshot(ctx, groupID, wukong.ChannelGroup)
	if err != nil || snapshot.Ban != 1 {
		t.Fatalf("banned snapshot=%+v err=%v", snapshot, err)
	}
	if _, err = p.AuthorizeWukongMessage(ctx, WukongMessageRouteInput{UserID: owner, ConversationID: groupID, Type: "text", Text: "blocked by ban"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("banned authorization=%v", err)
	}
	groups, total, _, err := p.ListAdminGroupsScoped(ctx, groupID, "banned", "", "", 10)
	if err != nil || total != 1 || len(groups) != 1 || groups[0]["status"] != "banned" {
		t.Fatalf("banned groups=%+v total=%d err=%v", groups, total, err)
	}
	messageID := time.Now().UnixNano()
	insertTestWukongMessage(t, p, ctx, messageID, "gov-message-"+suffix, groupID, member, 9, wukong.ContentTypeText, nil, "", now)
	already, sequence, recipients, err := p.AdminRecallGroupWukongMessage(ctx, groupID, strconv.FormatInt(messageID, 10), "admin_test", "违规文本", now.Add(7*time.Second))
	if err != nil || already || sequence != 9 || len(recipients) != 3 {
		t.Fatalf("recall already=%v seq=%d recipients=%v err=%v", already, sequence, recipients, err)
	}
	extensions, err := p.LoadAdminGroupMessageExtensions(ctx, groupID, []string{strconv.FormatInt(messageID, 10)})
	if err != nil || extensions[strconv.FormatInt(messageID, 10)]["adminRecall"] != true {
		t.Fatalf("extensions=%+v err=%v", extensions, err)
	}
	if err = p.AdminSetGroupBan(ctx, "admin_test", groupID, false, "恢复", now.Add(8*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = p.AdminSetGroupMuteAll(ctx, "admin_test", groupID, false, "解除禁言", now.Add(9*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = p.DisbandGroupRecord(ctx, "admin_test", groupID, "不可恢复解散", now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	snapshot, err = p.LoadWukongChannelSnapshot(ctx, groupID, wukong.ChannelGroup)
	if err != nil || snapshot.Disband != 1 || snapshot.Ban != 0 {
		t.Fatalf("disband snapshot=%+v err=%v", snapshot, err)
	}
}
