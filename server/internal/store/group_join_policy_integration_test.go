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
	"github.com/linli/im/server/internal/model"
)

func TestGroupJoinPolicyLifecycle(t *testing.T) {
	databaseURL := os.Getenv("IM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := t.Context()
	schema := fmt.Sprintf("group_join_policy_%d", time.Now().UnixNano())
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	if _, err = connection.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer connection.Exec(context.Background(), `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
	separator := "?"
	if strings.Contains(databaseURL, "?") {
		separator = "&"
	}
	p, err := NewPostgres(ctx, databaseURL+separator+"search_path="+schema+",public")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.SetGroupHistoryBoundaryReader(func(context.Context, string, string) (uint64, error) { return 12, nil })

	now := time.Now().UTC().Truncate(time.Second)
	users := []string{"owner", "admin", "member", "direct", "review", "stale", "manager-add", "blocked-by-policy"}
	for index, userID := range users {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$2,$1,$3,$3)`, userID, fmt.Sprintf("1390000%04d", index), now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_friendships(user_id,friend_user_id,created_at,updated_at) VALUES
		('member','direct',$1,$1),('member','review',$1,$1),('member','stale',$1,$1),
		('member','blocked-by-policy',$1,$1),('owner','manager-add',$1,$1)`, now); err != nil {
		t.Fatal(err)
	}
	conversation, err := p.CreateGroupRecord(ctx, "group", "owner", "Join policy", []string{"admin", "member"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `UPDATE im_members SET role='admin' WHERE conversation_id=$1 AND user_id='admin'`, conversation.ID); err != nil {
		t.Fatal(err)
	}

	outcome, err := p.InviteGroupMemberByPolicy(ctx, &model.GroupJoinRequest{ID: "direct-request", ConversationID: conversation.ID, RequesterID: "member", InviteeID: "direct"}, 500, now.Add(time.Minute))
	if err != nil || outcome.Action != "added" {
		t.Fatalf("member direct invite outcome=%+v error=%v", outcome, err)
	}
	if _, err = p.InviteGroupMemberByPolicy(ctx, &model.GroupJoinRequest{ID: "non-friend", ConversationID: conversation.ID, RequesterID: "member", InviteeID: "manager-add"}, 500, now.Add(2*time.Minute)); !errors.Is(err, ErrFriendRequired) {
		t.Fatalf("non-friend error=%v", err)
	}

	approval := "member_approval"
	if _, err = p.UpdateGroupProfile(ctx, "owner", conversation.ID, GroupProfileUpdate{JoinPolicy: &approval}, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	outcome, err = p.InviteGroupMemberByPolicy(ctx, &model.GroupJoinRequest{ID: "approval-request", ConversationID: conversation.ID, RequesterID: "member", InviteeID: "review"}, 500, now.Add(4*time.Minute))
	if err != nil || outcome.Action != "pending_approval" || outcome.Request == nil || outcome.Request.ExpiresAt.Sub(outcome.Request.CreatedAt) != 7*24*time.Hour {
		t.Fatalf("approval outcome=%+v error=%v", outcome, err)
	}
	profile, err := p.GetGroupProfile(ctx, "admin", conversation.ID)
	if err != nil || !profile.CanDirectInvite || !profile.CanReviewJoinRequests || profile.PendingJoinRequestCount != 1 {
		t.Fatalf("admin profile=%+v error=%v", profile, err)
	}
	request, duplicate, err := p.TransitionGroupJoinRequest(ctx, "admin", conversation.ID, outcome.Request.ID, "approve", 500, now.Add(5*time.Minute))
	if err != nil || duplicate || request.Status != "approved" {
		t.Fatalf("approved request=%+v duplicate=%v error=%v", request, duplicate, err)
	}

	outcome, err = p.InviteGroupMemberByPolicy(ctx, &model.GroupJoinRequest{ID: "stale-request", ConversationID: conversation.ID, RequesterID: "member", InviteeID: "stale"}, 500, now.Add(6*time.Minute))
	if err != nil || outcome.Request == nil {
		t.Fatal("create stale request", err)
	}
	closed := "closed"
	if _, err = p.UpdateGroupProfile(ctx, "owner", conversation.ID, GroupProfileUpdate{JoinPolicy: &closed}, now.Add(7*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT status FROM im_group_join_requests WHERE id=$1`, outcome.Request.ID).Scan(&request.Status); err != nil || request.Status != "invalidated" {
		t.Fatalf("stale status=%q error=%v", request.Status, err)
	}
	if _, err = p.InviteGroupMemberByPolicy(ctx, &model.GroupJoinRequest{ID: "closed-member", ConversationID: conversation.ID, RequesterID: "member", InviteeID: "blocked-by-policy"}, 500, now.Add(8*time.Minute)); !errors.Is(err, ErrJoinPolicy) {
		t.Fatalf("closed member error=%v", err)
	}
	if err = p.AddGroupMembersByPolicy(ctx, "owner", conversation.ID, []string{"manager-add"}, 500, now.Add(9*time.Minute)); err != nil {
		t.Fatalf("manager closed add: %v", err)
	}
}
