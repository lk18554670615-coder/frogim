package store

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/linli/im/server/internal/wukong"
)

func TestGroupMessageReceiptsExcludeSenderAndMembersWhoJoinedLater(t *testing.T) {
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
	groupID := "receipt_group_" + suffix
	owner := "receipt_owner_" + suffix
	admin := "receipt_admin_" + suffix
	reader := "receipt_reader_" + suffix
	unread := "receipt_unread_" + suffix
	late := "receipt_late_" + suffix
	users := []string{owner, admin, reader, unread, late}
	messageID := time.Now().UnixNano()
	messageAt := time.Now().UTC().Truncate(time.Millisecond)
	joinedAt := messageAt.Add(-time.Hour)

	defer func() {
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_message_extensions WHERE message_id=$1`, messageID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_message_index WHERE message_id=$1`, messageID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_members WHERE conversation_id=$1`, groupID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_friendships WHERE user_id=ANY($1::text[]) OR friend_user_id=ANY($1::text[])`, users)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_conversations WHERE id=$1`, groupID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_users WHERE id=ANY($1::text[])`, users)
	}()

	for index, userID := range users {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at)
			VALUES($1,$2,$3,$4,$4)`, userID, "receipt-phone-"+strconv.Itoa(index)+suffix, "成员"+strconv.Itoa(index), joinedAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,created_at,updated_at)
		VALUES($1,'group','阅读详情测试群',$2,$2)`, groupID, joinedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(
		conversation_id,user_id,role,last_read_seq,last_delivered_seq,joined_at) VALUES
		($1,$2,'owner',10,10,$7),($1,$3,'admin',10,10,$7),
		($1,$4,'member',10,10,$7),($1,$5,'member',9,10,$7),
		($1,$6,'member',999,999,$8)`, groupID, owner, admin, reader, unread, late, joinedAt, messageAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_friendships(user_id,friend_user_id,remark,created_at,updated_at)
		VALUES($1,$2,'好友备注',$3,$3)`, owner, reader, joinedAt); err != nil {
		t.Fatal(err)
	}
	insertTestWukongMessage(t, p, ctx, messageID, "receipt-client-"+suffix, groupID, owner, 10, wukong.ContentTypeText, nil, "", messageAt)

	readPage, err := p.GroupMessageReceipts(ctx, owner, strconv.FormatInt(messageID, 10), "read", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if readPage.Total != 3 || readPage.ReadCount != 2 || readPage.UnreadCount != 1 || len(readPage.Items) != 2 {
		t.Fatalf("unexpected read page: %+v", readPage)
	}
	for _, item := range readPage.Items {
		if item.UserID == owner || item.UserID == late || !item.Read {
			t.Fatalf("ineligible member leaked into read page: %+v", item)
		}
		if item.UserID == reader && item.DisplayName != "好友备注" {
			t.Fatalf("friend remark was not preferred: %+v", item)
		}
	}

	unreadPage, err := p.GroupMessageReceipts(ctx, owner, strconv.FormatInt(messageID, 10), "unread", "", 50)
	if err != nil || len(unreadPage.Items) != 1 || unreadPage.Items[0].UserID != unread || unreadPage.Items[0].Read {
		t.Fatalf("unexpected unread page=%+v error=%v", unreadPage, err)
	}
	if _, err = p.GroupMessageReceipts(ctx, unread, strconv.FormatInt(messageID, 10), "read", "", 50); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ordinary member should be forbidden, got %v", err)
	}

	extensions, err := p.LoadWukongMessageExtensions(ctx, owner, []string{strconv.FormatInt(messageID, 10)})
	if err != nil {
		t.Fatal(err)
	}
	extra := extensions[strconv.FormatInt(messageID, 10)]
	if extra["readCount"] != 2 || extra["unreadCount"] != 1 {
		t.Fatalf("aggregate receipt counted a late member: %+v", extra)
	}
}
