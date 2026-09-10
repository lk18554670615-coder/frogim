package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/wukong"
)

// GroupMessageReceipts returns the current member-level read state for one
// normal group message. Members who joined after the message are deliberately
// excluded: their history boundary can otherwise make an old message look read.
func (p *Postgres) GroupMessageReceipts(ctx context.Context, actorID, messageID, status, cursor string, limit int) (*GroupMessageReceiptPage, error) {
	actorID = strings.TrimSpace(actorID)
	messageID = strings.TrimSpace(messageID)
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = "read"
	}
	if actorID == "" || messageID == "" || (status != "read" && status != "unread") {
		return nil, ErrConflict
	}
	offset, limit := pageOffset(cursor, limit)
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var conversationID, senderID, actorRole string
	var messageSeq int64
	var contentType int
	var expired, deleted bool
	var extension []byte
	err = tx.QueryRow(ctx, `
		SELECT i.conversation_id,i.sender_id,i.message_seq,i.content_type,
			i.expired_at IS NOT NULL OR (i.expires_at IS NOT NULL AND i.expires_at<=now()),
			im_message_is_deleted(i.message_id::text),COALESCE(ext.payload,'{}'::jsonb),member.role
		FROM im_wukong_message_index i
		JOIN im_conversations conversation ON conversation.id=i.conversation_id AND conversation.kind='group'
		JOIN im_members member ON member.conversation_id=i.conversation_id AND member.user_id=$1
			AND (member.expires_at IS NULL OR member.expires_at>now())
		LEFT JOIN im_wukong_message_extensions ext ON ext.message_id=i.message_id
			AND ext.channel_id=i.channel_id AND ext.channel_type=i.channel_type
		WHERE i.message_id::text=$2 AND i.channel_type=$3
	`, actorID, messageID, wukong.ChannelGroup).Scan(
		&conversationID, &senderID, &messageSeq, &contentType, &expired, &deleted, &extension, &actorRole,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if actorRole != "owner" && actorRole != "admin" {
		return nil, ErrForbidden
	}
	if deleted || expired || contentType == wukong.ContentTypeSystemEvent ||
		contentType == wukong.ContentTypeCallEvent || contentType == wukong.ContentTypeLiveEvent ||
		contentType == wukong.ContentTypeSupportEvent || contentType == wukong.ContentTypeScreenshot {
		return nil, ErrNotFound
	}
	var extensionValues map[string]any
	if err = json.Unmarshal(extension, &extensionValues); err != nil {
		return nil, err
	}
	if extensionValues["recalledAt"] != nil {
		return nil, ErrNotFound
	}
	var canRead bool
	if err = tx.QueryRow(ctx, `SELECT im_can_read_group_message($1,$2,$3,
		(SELECT message_timestamp FROM im_wukong_message_index WHERE message_id::text=$4))`,
		actorID, conversationID, messageSeq, messageID).Scan(&canRead); err != nil {
		return nil, err
	}
	if !canRead {
		return nil, ErrForbidden
	}

	const eligibleMembers = `
		FROM im_members member
		JOIN im_users user_account ON user_account.id=member.user_id AND user_account.deleted_at IS NULL
		JOIN im_wukong_message_index message_index ON message_index.message_id::text=$2
		WHERE member.conversation_id=$1 AND member.user_id<>$3
			AND member.joined_at<=message_index.message_timestamp
			AND (member.expires_at IS NULL OR member.expires_at>message_index.message_timestamp)`
	var total, readCount int64
	if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE member.last_read_seq>=message_index.message_seq) `+eligibleMembers,
		conversationID, messageID, senderID).Scan(&total, &readCount); err != nil {
		return nil, err
	}

	comparison := "member.last_read_seq>=message_index.message_seq"
	if status == "unread" {
		comparison = "member.last_read_seq<message_index.message_seq"
	}
	rows, err := tx.Query(ctx, `
		SELECT member.user_id,user_account.name,user_account.avatar_url,member.role,
			COALESCE(NULLIF(friendship.remark,''),NULLIF(member.group_nickname,''),user_account.name),
			member.last_read_seq>=message_index.message_seq AS is_read
		FROM im_members member
		JOIN im_users user_account ON user_account.id=member.user_id AND user_account.deleted_at IS NULL
		JOIN im_wukong_message_index message_index ON message_index.message_id::text=$2
		LEFT JOIN im_friendships friendship ON friendship.user_id=$4 AND friendship.friend_user_id=member.user_id
		WHERE member.conversation_id=$1 AND member.user_id<>$3
			AND member.joined_at<=message_index.message_timestamp
			AND (member.expires_at IS NULL OR member.expires_at>message_index.message_timestamp)
			AND `+comparison+`
		ORDER BY CASE member.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END,
			lower(COALESCE(NULLIF(friendship.remark,''),NULLIF(member.group_nickname,''),user_account.name)),member.user_id
		LIMIT $5 OFFSET $6`, conversationID, messageID, senderID, actorID, limit+1, offset)
	if err != nil {
		return nil, err
	}
	items := make([]GroupMessageReceiptMember, 0, limit+1)
	for rows.Next() {
		var item GroupMessageReceiptMember
		if err = rows.Scan(&item.UserID, &item.Name, &item.AvatarURL, &item.Role, &item.DisplayName, &item.Read); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		nextCursor = nextPageCursor(offset, len(items), func() int64 {
			if status == "read" {
				return readCount
			}
			return total - readCount
		}())
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &GroupMessageReceiptPage{
		MessageID:      messageID,
		ConversationID: conversationID,
		ReadCount:      readCount,
		UnreadCount:    total - readCount,
		Total:          total,
		Items:          items,
		NextCursor:     nextCursor,
	}, nil
}

func (p *WithRedis) GroupMessageReceipts(ctx context.Context, actorID, messageID, status, cursor string, limit int) (*GroupMessageReceiptPage, error) {
	if receipts, ok := p.base.(GroupMessageReceiptStore); ok {
		return receipts.GroupMessageReceipts(ctx, actorID, messageID, status, cursor, limit)
	}
	return nil, ErrUnsupported
}
