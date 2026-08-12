package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/linli/im/server/internal/model"
)

func TestPostgresConversationProjectionAndMemberPagingAreBounded(t *testing.T) {
	databaseURL := os.Getenv("IM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repository, err := NewPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	conversationID := "perf_group_" + suffix
	now := time.Now()
	userIDs := make([]string, 12)
	for index := range userIDs {
		userIDs[index] = fmt.Sprintf("perf_user_%s_%02d", suffix, index)
		if _, err = repository.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$2,$3,$4)`, userIDs[index], fmt.Sprintf("perf_phone_%s_%02d", suffix, index), fmt.Sprintf("User %02d", index), now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = repository.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,created_at,updated_at) VALUES($1,'group','Performance',$2,$2)`, conversationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.pool.Exec(ctx, `INSERT INTO im_groups(conversation_id,owner_id,updated_at) VALUES($1,$2,$3)`, conversationID, userIDs[0], now); err != nil {
		t.Fatal(err)
	}
	for index, userID := range userIDs {
		role := "member"
		if index == 0 {
			role = "owner"
		}
		if _, err = repository.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,$3,$4)`, conversationID, userID, role, now); err != nil {
			t.Fatal(err)
		}
	}

	conversations, err := repository.ListConversations(ctx, userIDs[0], 10)
	if err != nil || len(conversations) != 1 {
		t.Fatalf("conversations=%d err=%v", len(conversations), err)
	}
	preview, ok := conversations[0]["members"].([]*model.ConversationMember)
	if !ok || len(preview) != 8 || conversations[0]["memberCount"].(int64) != 12 {
		t.Fatalf("member preview=%T/%d count=%v", conversations[0]["members"], len(preview), conversations[0]["memberCount"])
	}
	firstPage, next, err := repository.ListConversationMembersPage(ctx, userIDs[0], conversationID, "", 5)
	if err != nil || len(firstPage) != 5 || next != "5" {
		t.Fatalf("first page=%d next=%q err=%v", len(firstPage), next, err)
	}
	secondPage, next, err := repository.ListConversationMembersPage(ctx, userIDs[0], conversationID, next, 200)
	if err != nil || len(secondPage) != 7 || next != "" {
		t.Fatalf("second page=%d next=%q err=%v", len(secondPage), next, err)
	}

}
