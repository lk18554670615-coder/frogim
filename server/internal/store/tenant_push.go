package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// ValidTenantPush never fills in missing versions or routing from current state.
func (item OutboxItem) ValidTenantPush() bool {
	r := item.TenantPush
	return item.TenantManaged && r != nil && item.ID > 0 &&
		r.RequestID == "push_"+strconv.FormatInt(item.ID, 10) &&
		r.LocalUserID == item.UserID && r.EventType == item.EventType && r.Validate(time.Now()) == nil
}

func (p *Postgres) canPresentTenantPush(ctx context.Context, item OutboxItem) (bool, error) {
	if !item.ValidTenantPush() || !item.TenantPush.ExpiresAt.After(time.Now()) {
		return false, nil
	}
	r := item.TenantPush
	var active bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users u CROSS JOIN im_tenant_identity t
	 WHERE t.singleton AND t.tenant_id=$1 AND t.access_enabled AND t.access_version=$6
	 AND u.id=$2 AND u.platform_account_id=$3 AND u.assignment_version=$4 AND u.platform_auth_version=$5
	 AND u.local_identity_state='active' AND NOT u.banned AND u.deleted_at IS NULL)`,
		r.TenantID, r.LocalUserID, r.AccountID, r.AssignmentVersion, r.AuthVersion, r.RealmVersion).Scan(&active)
	if err != nil || !active {
		return false, err
	}
	// Recheck business visibility as well as platform identity. No body is read
	// here or passed to the platform; a not-yet-indexed IM message waits for retry.
	switch r.EventType {
	case "message.created":
		mid, err := strconv.ParseInt(r.MessageID, 10, 64)
		if err != nil || mid <= 0 {
			return false, nil
		}
		err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_conversations c
		 JOIN im_members m ON m.conversation_id=c.id AND m.user_id=$2
		 LEFT JOIN im_groups g ON g.conversation_id=c.id
		 WHERE c.id=i.conversation_id AND c.id=$3 AND NOT m.notifications_muted
		 AND (m.expires_at IS NULL OR m.expires_at>now())
		 AND (g.conversation_id IS NULL OR (g.dissolved_at IS NULL AND NOT g.banned))
		 AND (c.kind<>'direct' OR EXISTS(SELECT 1 FROM im_friendships f WHERE f.user_id=$2 AND f.friend_user_id=i.sender_id))
		 AND NOT EXISTS(SELECT 1 FROM im_blocks b WHERE (b.user_id=$2 AND b.blocked_user_id=i.sender_id) OR (b.user_id=i.sender_id AND b.blocked_user_id=$2))
		 AND im_can_read_group_message($2,c.id,i.message_seq,i.message_timestamp))
		 AND i.expired_at IS NULL AND (i.expires_at IS NULL OR i.expires_at>now())
		 AND NOT im_message_is_deleted(i.message_id::text)
		 AND NOT EXISTS(SELECT 1 FROM im_wukong_message_extensions e WHERE e.message_id=i.message_id AND e.payload->>'recalledAt' IS NOT NULL)
		 FROM im_wukong_message_index i WHERE i.message_id=$1`, mid, item.UserID, r.ConversationID).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, errors.New("push message index not ready")
		}
		return active, err
	case "call.invited":
		err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_call_sessions c
		 JOIN im_members m ON m.conversation_id=c.conversation_id AND m.user_id=$2
		 LEFT JOIN im_groups g ON g.conversation_id=c.conversation_id
		 WHERE c.id=$1 AND c.conversation_id=$3 AND (c.status='invited' OR (c.call_kind='group' AND c.status='accepted')) AND c.expires_at>now()
		 AND $2=ANY(c.participant_ids) AND $2<>c.caller_id
		 AND NOT $2=ANY(c.joined_user_ids) AND NOT $2=ANY(c.declined_user_ids) AND NOT $2=ANY(c.left_user_ids)
		 AND (g.conversation_id IS NULL OR (NOT g.banned AND g.dissolved_at IS NULL))
		 AND NOT EXISTS(SELECT 1 FROM im_blocks b WHERE (b.user_id=$2 AND b.blocked_user_id=c.caller_id) OR (b.user_id=c.caller_id AND b.blocked_user_id=$2))
		 AND (m.expires_at IS NULL OR m.expires_at>now()))`, r.CallID, item.UserID, r.ConversationID).Scan(&active)
	case "friend.request", "friend.request.updated":
		id, _ := item.Payload["requestId"].(string)
		err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_friend_requests f
		 WHERE f.id=$1 AND ($2=f.from_user_id OR $2=f.to_user_id)
		 AND ($3<>'friend.request' OR (f.to_user_id=$2 AND f.status='pending' AND f.expires_at>now()))
		 AND NOT EXISTS(SELECT 1 FROM im_blocks b WHERE (b.user_id=f.from_user_id AND b.blocked_user_id=f.to_user_id) OR (b.user_id=f.to_user_id AND b.blocked_user_id=f.from_user_id)))`, id, item.UserID, r.EventType).Scan(&active)
	case "group.invite":
		id, _ := item.Payload["inviteId"].(string)
		err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_group_invites v JOIN im_groups g ON g.conversation_id=v.conversation_id
		 WHERE v.id=$1 AND v.invitee_id=$2 AND v.status='pending' AND v.expires_at>now() AND NOT g.banned AND g.dissolved_at IS NULL
		 AND NOT EXISTS(SELECT 1 FROM im_members m WHERE m.conversation_id=v.conversation_id AND m.user_id=$2)
		 AND EXISTS(SELECT 1 FROM im_members m WHERE m.conversation_id=v.conversation_id AND m.user_id=v.inviter_id))`, id, item.UserID).Scan(&active)
	case "announcement.published":
		id, _ := item.Payload["announcementId"].(string)
		err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_announcements a WHERE a.id=$1 AND a.status='published'
		 AND (a.target_type='all' OR a.target_user_ids ? $2))`, id, item.UserID).Scan(&active)
	default:
		return false, nil
	}
	return active, err
}
