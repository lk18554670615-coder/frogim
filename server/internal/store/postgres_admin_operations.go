package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/model"
)

func (p *Postgres) AdminStats(ctx context.Context) (map[string]any, error) {
	var users, bannedUsers, conversations, messages, pendingReports int64
	err := p.pool.QueryRow(ctx, `WITH all_messages AS (`+adminMessageUnion+`) SELECT
		(SELECT count(*) FROM im_users),(SELECT count(*) FROM im_users WHERE banned),
		(SELECT count(*) FROM im_conversations),(SELECT count(*) FROM all_messages),
		(SELECT count(*) FROM im_reports WHERE status='pending')
	`).Scan(&users, &bannedUsers, &conversations, &messages, &pendingReports)
	if err != nil {
		return nil, err
	}
	messageTrend := make([]map[string]any, 0, 12)
	rows, err := p.pool.Query(ctx, `WITH buckets AS (
		SELECT generate_series(
			date_trunc('hour', now())-interval '11 hours',
			date_trunc('hour', now()),
			interval '1 hour'
		) AS bucket
	)
	SELECT bucket,count(message_index.message_id)
	FROM buckets
	LEFT JOIN im_wukong_message_index message_index
		ON message_index.message_timestamp>=bucket
		AND message_index.message_timestamp<bucket+interval '1 hour'
	GROUP BY bucket ORDER BY bucket`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var bucket time.Time
		var count int64
		if err = rows.Scan(&bucket, &count); err != nil {
			rows.Close()
			return nil, err
		}
		messageTrend = append(messageTrend, map[string]any{"time": bucket, "count": count})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	channelMix := make([]map[string]any, 0, 3)
	rows, err = p.pool.Query(ctx, `SELECT
		CASE channel_type WHEN 1 THEN 'direct' WHEN 2 THEN 'group' ELSE 'other' END AS kind,
		count(*)
	FROM im_wukong_message_index
	GROUP BY kind ORDER BY kind`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind string
		var count int64
		if err = rows.Scan(&kind, &count); err != nil {
			rows.Close()
			return nil, err
		}
		channelMix = append(channelMix, map[string]any{"kind": kind, "count": count})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	activity := make([]map[string]any, 0, 5)
	rows, err = p.pool.Query(ctx, `SELECT id,actor_id,action,target_type,target_id,COALESCE(result,'success'),COALESCE(ip,''),created_at
		FROM im_audits ORDER BY created_at DESC,id DESC LIMIT 5`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, actorID, action, targetType, targetID, result, ip string
		var createdAt time.Time
		if err = rows.Scan(&id, &actorID, &action, &targetType, &targetID, &result, &ip, &createdAt); err != nil {
			rows.Close()
			return nil, err
		}
		activity = append(activity, map[string]any{
			"id": id, "actorId": actorID, "action": action, "targetType": targetType,
			"targetId": targetID, "result": result, "ip": ip, "createdAt": createdAt,
		})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	return map[string]any{
		"users": users, "bannedUsers": bannedUsers, "conversations": conversations,
		"messages": messages, "pendingReports": pendingReports, "messageTrend": messageTrend,
		"channelMix": channelMix, "activity": activity,
	}, nil
}

func (p *Postgres) ListAdminGroups(ctx context.Context, q, status, cursor string, limit int) ([]map[string]any, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	pattern := "%" + q + "%"
	where := `($1='' OR c.id ILIKE $2 OR c.title ILIKE $2 OR g.owner_id ILIKE $2 OR owner.name ILIKE $2) AND ($3='' OR ($3='active' AND g.dissolved_at IS NULL AND (g.all_muted_until IS NULL OR g.all_muted_until<=now())) OR ($3='muted' AND g.dissolved_at IS NULL AND g.all_muted_until>now()) OR ($3='dissolved' AND g.dissolved_at IS NOT NULL))`
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_groups g JOIN im_conversations c ON c.id=g.conversation_id JOIN im_users owner ON owner.id=g.owner_id WHERE `+where, q, pattern, status).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, `SELECT c.id,c.title,g.owner_id,owner.name,(SELECT count(*) FROM im_members m WHERE m.conversation_id=c.id),GREATEST(c.last_message_seq,COALESCE((SELECT max(message_seq) FROM im_wukong_message_index WHERE conversation_id=c.id),0)),CASE WHEN g.dissolved_at IS NOT NULL THEN 'dissolved' WHEN g.all_muted_until>now() THEN 'muted' ELSE 'active' END,c.created_at,(SELECT count(*) FROM im_reports r WHERE r.target_type='group' AND r.target_id=c.id) FROM im_groups g JOIN im_conversations c ON c.id=g.conversation_id JOIN im_users owner ON owner.id=g.owner_id WHERE `+where+` ORDER BY c.created_at DESC,c.id LIMIT $4 OFFSET $5`, q, pattern, status, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit)
	for rows.Next() {
		var id, title, ownerID, ownerName, state string
		var members, messages, reports int64
		var created any
		if err = rows.Scan(&id, &title, &ownerID, &ownerName, &members, &messages, &state, &created, &reports); err != nil {
			return nil, 0, "", err
		}
		items = append(items, map[string]any{"id": id, "name": title, "ownerId": ownerID, "owner": ownerName, "memberCount": members, "messageCount": messages, "status": state, "createdAt": created, "reportCount": reports})
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

func (p *Postgres) ListAdminFriendships(ctx context.Context, q, cursor string, limit int) ([]AdminFriendship, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	pattern := "%" + q + "%"
	where := `f.user_id<f.friend_user_id AND ($1='' OR f.user_id ILIKE $2 OR f.friend_user_id ILIKE $2 OR u.name ILIKE $2 OR friend.name ILIKE $2)`
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_friendships f JOIN im_users u ON u.id=f.user_id JOIN im_users friend ON friend.id=f.friend_user_id WHERE `+where, q, pattern).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, `SELECT f.user_id,f.friend_user_id,u.name,friend.name,f.created_at,f.updated_at FROM im_friendships f JOIN im_users u ON u.id=f.user_id JOIN im_users friend ON friend.id=f.friend_user_id WHERE `+where+` ORDER BY f.updated_at DESC,f.user_id,f.friend_user_id LIMIT $3 OFFSET $4`, q, pattern, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	items := make([]AdminFriendship, 0, limit)
	for rows.Next() {
		var item AdminFriendship
		if err = rows.Scan(&item.UserID, &item.FriendUserID, &item.UserName, &item.FriendName, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, 0, "", err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

func (p *Postgres) ListAdminFeedback(ctx context.Context, q, category, cursor string, limit int) ([]AdminFeedback, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	pattern := "%" + q + "%"
	where := `($1='' OR feedback.id ILIKE $2 OR feedback.user_id ILIKE $2 OR user_row.name ILIKE $2 OR feedback.content ILIKE $2 OR feedback.contact ILIKE $2) AND ($3='' OR feedback.category=$3)`
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_feedback feedback JOIN im_users user_row ON user_row.id=feedback.user_id WHERE `+where, q, pattern, category).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, `SELECT feedback.id,feedback.user_id,user_row.name,feedback.category,feedback.content,feedback.contact,feedback.created_at FROM im_feedback feedback JOIN im_users user_row ON user_row.id=feedback.user_id WHERE `+where+` ORDER BY feedback.created_at DESC,feedback.id LIMIT $4 OFFSET $5`, q, pattern, category, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	items := make([]AdminFeedback, 0, limit)
	for rows.Next() {
		var item AdminFeedback
		if err = rows.Scan(&item.ID, &item.UserID, &item.UserName, &item.Category, &item.Content, &item.Contact, &item.CreatedAt); err != nil {
			return nil, 0, "", err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

func (p *Postgres) AdminPushStatus(ctx context.Context) (map[string]any, error) {
	result := map[string]any{}
	rows, err := p.pool.Query(ctx, `SELECT provider,count(*) FILTER(WHERE notifications_enabled),count(*) FILTER(WHERE NOT notifications_enabled) FROM im_devices GROUP BY provider ORDER BY provider`)
	if err != nil {
		return nil, err
	}
	providers := make([]map[string]any, 0)
	for rows.Next() {
		var provider string
		var active, disabled int64
		if err = rows.Scan(&provider, &active, &disabled); err != nil {
			rows.Close()
			return nil, err
		}
		providers = append(providers, map[string]any{"provider": provider, "activeDevices": active, "disabledDevices": disabled})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result["providers"] = providers
	rows, err = p.pool.Query(ctx, `SELECT status,count(*),COALESCE(sum(attempts),0),max(created_at),max(sent_at) FROM im_push_outbox GROUP BY status ORDER BY status`)
	if err != nil {
		return nil, err
	}
	queue := make([]map[string]any, 0)
	for rows.Next() {
		var status string
		var count, attempts int64
		var latestCreated, latestSent any
		if err = rows.Scan(&status, &count, &attempts, &latestCreated, &latestSent); err != nil {
			rows.Close()
			return nil, err
		}
		queue = append(queue, map[string]any{"status": status, "count": count, "attempts": attempts, "latestCreatedAt": latestCreated, "latestSentAt": latestSent})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result["queue"] = queue
	return result, nil
}

func (p *Postgres) AdminTaskStatus(ctx context.Context) (map[string]any, error) {
	result := map[string]any{}
	var scheduledPending, scheduledProcessing, scheduledFailed, expiring int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='processing'),count(*) FILTER(WHERE status='failed') FROM im_scheduled_messages`).Scan(&scheduledPending, &scheduledProcessing, &scheduledFailed); err != nil {
		return nil, err
	}
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_message_index WHERE expires_at>now() AND expired_at IS NULL`).Scan(&expiring); err != nil {
		return nil, err
	}
	result["scheduledMessages"] = map[string]any{"pending": scheduledPending, "processing": scheduledProcessing, "failed": scheduledFailed}
	result["messageExpiry"] = map[string]any{"waiting": expiring}
	var outboxPending, outboxProcessing, outboxFailed, reconcilePending, reconcileCompleted, reconcileFailed int64
	var outboxOldestSeconds float64
	var outboxLastCompleted *time.Time
	if err := p.pool.QueryRow(ctx, `SELECT
		count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='processing'),count(*) FILTER(WHERE status='failed'),
		count(*) FILTER(WHERE operation='channel.reconcile' AND status IN ('pending','processing')),
		count(*) FILTER(WHERE operation='channel.reconcile' AND status='completed'),
		count(*) FILTER(WHERE operation='channel.reconcile' AND status='failed'),
		COALESCE(EXTRACT(EPOCH FROM (now()-min(created_at) FILTER(WHERE status IN ('pending','processing')))),0),
		max(completed_at) FROM im_wukong_outbox`).Scan(
		&outboxPending, &outboxProcessing, &outboxFailed, &reconcilePending, &reconcileCompleted, &reconcileFailed,
		&outboxOldestSeconds, &outboxLastCompleted,
	); err != nil {
		return nil, err
	}
	result["wukongOutbox"] = map[string]any{
		"pending": outboxPending, "processing": outboxProcessing, "failed": outboxFailed,
		"oldestPendingSeconds": outboxOldestSeconds, "lastCompletedAt": outboxLastCompleted,
		"reconcilePending": reconcilePending, "reconcileCompleted": reconcileCompleted, "reconcileFailed": reconcileFailed,
	}
	var webhookPending, webhookProcessing, webhookFailed int64
	var webhookOldestSeconds float64
	var webhookLastCompleted *time.Time
	if err := p.pool.QueryRow(ctx, `SELECT
		count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='processing'),count(*) FILTER(WHERE status='failed'),
		COALESCE(EXTRACT(EPOCH FROM (now()-min(received_at) FILTER(WHERE status IN ('pending','processing')))),0),
		max(completed_at) FROM im_wukong_webhook_events`).Scan(
		&webhookPending, &webhookProcessing, &webhookFailed, &webhookOldestSeconds, &webhookLastCompleted,
	); err != nil {
		return nil, err
	}
	result["wukongWebhook"] = map[string]any{
		"pending": webhookPending, "processing": webhookProcessing, "failed": webhookFailed,
		"oldestPendingSeconds": webhookOldestSeconds, "lastCompletedAt": webhookLastCompleted,
	}
	return result, nil
}

func (p *Postgres) AdminUserOverview(ctx context.Context, id string) (map[string]any, error) {
	var user model.User
	var devices, friends, groups int64
	var updatedAt any
	err := p.pool.QueryRow(ctx, `SELECT u.id,u.phone,u.name,COALESCE(u.handle,''),u.signature,u.avatar_url,u.banned,u.handle_change_count,u.created_at,u.updated_at,
		(SELECT count(*) FROM im_devices d WHERE d.user_id=u.id),(SELECT count(*) FROM im_friendships f WHERE f.user_id=u.id),(SELECT count(*) FROM im_members m WHERE m.user_id=u.id)
		FROM im_users u WHERE u.id=$1`, id).Scan(&user.ID, &user.Phone, &user.Name, &user.Handle, &user.Signature, &user.AvatarURL, &user.Banned, &user.HandleChangeCount, &user.CreatedAt, &updatedAt, &devices, &friends, &groups)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"user": &user, "deviceCount": devices, "friendCount": friends, "groupCount": groups, "handleChangesUsed": user.HandleChangeCount, "handleChangesRemaining": max(0, 2-user.HandleChangeCount)}, nil
}

func (p *Postgres) AdminGroupOverview(ctx context.Context, id string) (map[string]any, error) {
	var title, owner, announcement, joinPolicy string
	var memberCount, announcementVersion, messageCount int64
	var allowMemberAddFriend bool
	err := p.pool.QueryRow(ctx, `SELECT c.title,g.owner_id,g.announcement,g.announcement_version,g.join_policy,g.allow_member_add_friend,GREATEST(c.last_message_seq,COALESCE((SELECT max(message_seq) FROM im_wukong_message_index WHERE conversation_id=c.id),0)),(SELECT count(*) FROM im_members m WHERE m.conversation_id=c.id)
		FROM im_conversations c JOIN im_groups g ON g.conversation_id=c.id WHERE c.id=$1`, id).Scan(&title, &owner, &announcement, &announcementVersion, &joinPolicy, &allowMemberAddFriend, &messageCount, &memberCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "title": title, "ownerId": owner, "announcement": announcement, "announcementVersion": announcementVersion, "joinPolicy": joinPolicy, "allowMemberAddFriend": allowMemberAddFriend, "messageCount": messageCount, "memberCount": memberCount}, nil
}

func (p *Postgres) ListAdminGroupMembers(ctx context.Context, id, q, cursor string, limit int) ([]*model.ConversationMember, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	pattern := "%" + q + "%"
	where := `m.conversation_id=$1 AND ($2='' OR m.user_id ILIKE $3 OR u.name ILIKE $3 OR COALESCE(u.handle,'') ILIKE $3 OR m.group_nickname ILIKE $3)`
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_members m JOIN im_users u ON u.id=m.user_id WHERE `+where, id, q, pattern).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, `SELECT m.conversation_id,m.user_id,u.name,COALESCE(u.handle,''),u.avatar_url,m.role,m.muted_until,m.last_read_seq,m.last_delivered_seq,m.group_nickname,m.joined_at FROM im_members m JOIN im_users u ON u.id=m.user_id WHERE `+where+` ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END,m.joined_at,m.user_id LIMIT $4 OFFSET $5`, id, q, pattern, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	items := make([]*model.ConversationMember, 0, limit)
	for rows.Next() {
		item := new(model.ConversationMember)
		if err = rows.Scan(&item.ConversationID, &item.UserID, &item.Name, &item.Handle, &item.AvatarURL, &item.Role, &item.MutedUntil, &item.LastReadSeq, &item.LastDeliveredSeq, &item.GroupNickname, &item.JoinedAt); err != nil {
			return nil, 0, "", err
		}
		item.ID = item.UserID
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

func (p *Postgres) RecordAdminAudit(ctx context.Context, entry *model.AuditEntry) error {
	raw, err := json.Marshal(entry.Metadata)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,result,ip,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, entry.ID, entry.ActorID, entry.Action, entry.TargetType, entry.TargetID, raw, entry.Result, entry.IP, entry.CreatedAt)
	return err
}
