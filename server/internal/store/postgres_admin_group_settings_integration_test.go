package store

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAdminUpdateGroupSettingsIsAtomicAndOptimistic(t *testing.T) {
	databaseURL := os.Getenv("IM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	ownerID := "settings_owner_" + suffix
	groupID := "settings_group_" + suffix
	createdAt := time.Now().UTC().Truncate(time.Millisecond)
	defer func() {
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_outbox WHERE aggregate_id=$1 OR payload->>'channel_id'=$1`, groupID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_audits WHERE target_id=$1 OR target_id LIKE $1||':%'`, groupID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_conversations WHERE id=$1`, groupID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_users WHERE id=$1`, ownerID)
	}()
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$2,$3,$4,$4)`, ownerID, "settings-phone-"+suffix, ownerID, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err = p.CreateGroupRecord(ctx, groupID, ownerID, "original name", nil, createdAt); err != nil {
		t.Fatal(err)
	}
	overview, err := p.AdminGroupOverview(ctx, groupID)
	if err != nil {
		t.Fatal(err)
	}
	expectedUpdatedAt, ok := overview["updatedAt"].(time.Time)
	if !ok {
		t.Fatalf("updatedAt type=%T", overview["updatedAt"])
	}

	name := "updated name"
	announcement := "private-body-do-not-copy"
	joinPolicy := "member_approval"
	allowMemberAddFriend := false
	historyVisible := true
	rateLimit := 10
	allMuted := true
	updatedAt := createdAt.Add(2 * time.Second)
	result, err := p.AdminUpdateGroupSettings(ctx, AdminGroupSettingsUpdate{
		ActorID: "admin_settings_test", GroupID: groupID, Reason: "verified settings change", RequestIP: "127.0.0.1",
		ExpectedUpdatedAt: expectedUpdatedAt, Name: &name, Announcement: &announcement, JoinPolicy: &joinPolicy,
		AllowMemberAddFriend: &allowMemberAddFriend, HistoryVisibleToNewMembers: &historyVisible,
		MemberMessageRateLimitPerMinute: &rateLimit, AllMuted: &allMuted, At: updatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantFields := []string{"name", "announcement", "joinPolicy", "allowMemberAddFriend", "historyVisibleToNewMembers", "memberMessageRateLimitPerMinute", "allMuted"}
	if !slices.Equal(result.ChangedFields, wantFields) {
		t.Fatalf("changedFields=%v want=%v", result.ChangedFields, wantFields)
	}
	storedUpdatedAt, ok := result.Group["updatedAt"].(time.Time)
	if !ok || !storedUpdatedAt.Equal(updatedAt) {
		t.Fatalf("updatedAt=%v type=%T", result.Group["updatedAt"], result.Group["updatedAt"])
	}
	if result.Group["title"] != name || result.Group["announcement"] != announcement || result.Group["joinPolicy"] != joinPolicy || result.Group["allowMemberAddFriend"] != false || result.Group["historyVisibleToNewMembers"] != true || result.Group["memberMessageRateLimitPerMinute"] != rateLimit {
		t.Fatalf("unexpected overview: %#v", result.Group)
	}
	if result.Group["joinPolicyVersion"] != int64(2) || result.Group["historyPolicyVersion"] != int64(2) || result.Group["messageRateLimitVersion"] != int64(2) {
		t.Fatalf("versions join=%v history=%v rate=%v", result.Group["joinPolicyVersion"], result.Group["historyPolicyVersion"], result.Group["messageRateLimitVersion"])
	}
	mutedUntil, ok := result.Group["allMutedUntil"].(*time.Time)
	if !ok || mutedUntil == nil || !mutedUntil.After(updatedAt) {
		t.Fatalf("allMutedUntil=%v type=%T", result.Group["allMutedUntil"], result.Group["allMutedUntil"])
	}

	var auditCount int
	var auditMetadata string
	if err = p.pool.QueryRow(ctx, `SELECT count(*),max(metadata::text) FROM im_audits WHERE action='group.settings.updated' AND target_id=$1`, groupID).Scan(&auditCount, &auditMetadata); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 || strings.Contains(auditMetadata, announcement) || !strings.Contains(auditMetadata, "verified settings change") {
		t.Fatalf("audit count=%d metadata=%s", auditCount, auditMetadata)
	}
	var muteBefore, muteAfter bool
	var muteReason string
	if err = p.pool.QueryRow(ctx, `SELECT (metadata->'before'->>'muted')::boolean,(metadata->'after'->>'muted')::boolean,metadata->>'reason' FROM im_audits WHERE action='group.mute_all.updated' AND target_id=$1 ORDER BY created_at DESC LIMIT 1`, groupID).Scan(&muteBefore, &muteAfter, &muteReason); err != nil {
		t.Fatal(err)
	}
	if muteBefore || !muteAfter || muteReason != "verified settings change" {
		t.Fatalf("settings mute audit before=%v after=%v reason=%q", muteBefore, muteAfter, muteReason)
	}

	noChange, err := p.AdminUpdateGroupSettings(ctx, AdminGroupSettingsUpdate{
		ActorID: "admin_settings_test", GroupID: groupID, Reason: "no-op", ExpectedUpdatedAt: storedUpdatedAt,
		Name: &name, Announcement: &announcement, JoinPolicy: &joinPolicy, AllowMemberAddFriend: &allowMemberAddFriend,
		HistoryVisibleToNewMembers: &historyVisible, MemberMessageRateLimitPerMinute: &rateLimit, AllMuted: &allMuted,
		At: updatedAt.Add(time.Second),
	})
	if err != nil || len(noChange.ChangedFields) != 0 {
		t.Fatalf("no-op result=%+v err=%v", noChange, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE action='group.settings.updated' AND target_id=$1`, groupID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("no-op audit count=%d err=%v", auditCount, err)
	}

	conflictingName := "must not overwrite"
	if _, err = p.AdminUpdateGroupSettings(ctx, AdminGroupSettingsUpdate{
		ActorID: "admin_settings_test", GroupID: groupID, Reason: "stale write", ExpectedUpdatedAt: expectedUpdatedAt,
		Name: &conflictingName, At: updatedAt.Add(2 * time.Second),
	}); !errors.Is(err, ErrGroupSettingsChanged) {
		t.Fatalf("stale write error=%v", err)
	}

	missingMediaID := "missing-media-" + suffix
	if _, err = p.AdminUpdateGroupSettings(ctx, AdminGroupSettingsUpdate{
		ActorID: "admin_settings_test", GroupID: groupID, Reason: "atomic rollback", ExpectedUpdatedAt: storedUpdatedAt,
		Name: &conflictingName, AvatarMediaID: &missingMediaID, At: updatedAt.Add(3 * time.Second),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid avatar error=%v", err)
	}
	var storedName string
	if err = p.pool.QueryRow(ctx, `SELECT title FROM im_conversations WHERE id=$1`, groupID).Scan(&storedName); err != nil || storedName != name {
		t.Fatalf("atomic rollback title=%q err=%v", storedName, err)
	}
}
