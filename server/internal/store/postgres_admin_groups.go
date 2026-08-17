package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/model"
)

var permanentGroupMuteUntil = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

func (p *Postgres) ListAdminGroupsScoped(ctx context.Context, q, scope, status, cursor string, limit int) ([]map[string]any, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	q, scope, status = strings.TrimSpace(q), strings.TrimSpace(scope), strings.TrimSpace(status)
	if scope == "" {
		scope = "normal"
	}
	if scope != "normal" && scope != "banned" && scope != "all" {
		return nil, 0, "", ErrConflict
	}
	if status != "" && status != "active" && status != "muted" && status != "dissolved" {
		return nil, 0, "", ErrConflict
	}
	pattern := "%" + q + "%"
	where := `($1='' OR c.id ILIKE $2 OR c.title ILIKE $2 OR g.owner_id ILIKE $2 OR owner.name ILIKE $2 OR owner.phone ILIKE $2)
		AND (($3='normal' AND NOT g.banned) OR ($3='banned' AND g.banned AND g.dissolved_at IS NULL) OR $3='all')
		AND ($4='' OR ($4='active' AND g.dissolved_at IS NULL AND NOT g.banned AND (g.all_muted_until IS NULL OR g.all_muted_until<=now()))
		OR ($4='muted' AND g.dissolved_at IS NULL AND NOT g.banned AND g.all_muted_until>now())
		OR ($4='dissolved' AND g.dissolved_at IS NOT NULL))`
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_groups g JOIN im_conversations c ON c.id=g.conversation_id JOIN im_users owner ON owner.id=g.owner_id WHERE `+where, q, pattern, scope, status).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, `SELECT c.id,c.title,c.avatar_url,g.owner_id,owner.phone,owner.name,COALESCE(owner.handle,''),owner.avatar_url,
		(SELECT count(*) FROM im_members m WHERE m.conversation_id=c.id),
		GREATEST(c.last_message_seq,COALESCE((SELECT max(message_seq) FROM im_wukong_message_index WHERE conversation_id=c.id),0)),
		CASE WHEN g.dissolved_at IS NOT NULL THEN 'dissolved' WHEN g.banned THEN 'banned' WHEN g.all_muted_until>now() THEN 'muted' ELSE 'active' END,
		c.created_at,(SELECT count(*) FROM im_reports r WHERE r.target_type='group' AND r.target_id=c.id),
		CASE WHEN g.all_muted_until>now() THEN g.all_muted_until END,g.banned,g.banned_at,g.banned_by,g.ban_reason,g.dissolved_at
		FROM im_groups g JOIN im_conversations c ON c.id=g.conversation_id JOIN im_users owner ON owner.id=g.owner_id
		WHERE `+where+` ORDER BY c.created_at DESC,c.id LIMIT $5 OFFSET $6`, q, pattern, scope, status, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit)
	for rows.Next() {
		var id, title, avatar, ownerID, phone, name, handle, ownerAvatar, state, bannedBy, banReason string
		var members, messages, reports int64
		var createdAt time.Time
		var allMutedUntil, bannedAt, dissolvedAt *time.Time
		var banned bool
		if err = rows.Scan(&id, &title, &avatar, &ownerID, &phone, &name, &handle, &ownerAvatar, &members, &messages, &state, &createdAt, &reports, &allMutedUntil, &banned, &bannedAt, &bannedBy, &banReason, &dissolvedAt); err != nil {
			return nil, 0, "", err
		}
		owner := &model.User{ID: ownerID, Phone: phone, Name: name, Handle: handle, AvatarURL: ownerAvatar}
		items = append(items, map[string]any{"id": id, "name": title, "avatarUrl": avatar, "ownerId": ownerID, "owner": owner, "memberCount": members, "messageCount": messages, "status": state, "createdAt": createdAt, "reportCount": reports, "allMutedUntil": allMutedUntil, "banned": banned, "bannedAt": bannedAt, "bannedBy": bannedBy, "banReason": banReason, "dissolvedAt": dissolvedAt})
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

func (p *Postgres) ListAdminGroupBlacklist(ctx context.Context, groupID string) ([]AdminGroupBlacklistEntry, error) {
	rows, err := p.pool.Query(ctx, `SELECT u.id,u.phone,u.name,COALESCE(u.handle,''),u.avatar_url,b.operator_id,COALESCE(a.display_name,b.operator_id),b.remark,b.created_at
		FROM im_group_blacklist b JOIN im_users u ON u.id=b.user_id LEFT JOIN im_admin_accounts a ON a.id=b.operator_id
		WHERE b.conversation_id=$1 ORDER BY b.created_at DESC,b.user_id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AdminGroupBlacklistEntry{}
	for rows.Next() {
		item := AdminGroupBlacklistEntry{User: &model.User{}}
		if err = rows.Scan(&item.User.ID, &item.User.Phone, &item.User.Name, &item.User.Handle, &item.User.AvatarURL, &item.OperatorID, &item.OperatorName, &item.Remark, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) AdminAddGroupBlacklist(ctx context.Context, actor, groupID, userID, remark string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owner string
	var dissolved *time.Time
	if err = tx.QueryRow(ctx, `SELECT owner_id,dissolved_at FROM im_groups WHERE conversation_id=$1 FOR UPDATE`, groupID).Scan(&owner, &dissolved); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if dissolved != nil {
		return ErrConflict
	}
	if userID == owner {
		return ErrForbidden
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1 AND deleted_at IS NULL)`, userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_group_blacklist(conversation_id,user_id,operator_id,remark,created_at) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(conversation_id,user_id) DO UPDATE SET operator_id=excluded.operator_id,remark=excluded.remark,created_at=excluded.created_at`, groupID, userID, actor, remark, at); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM im_members WHERE conversation_id=$1 AND user_id=$2`, groupID, userID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_group_invites SET status='cancelled',resolved_at=$3,updated_at=$3 WHERE conversation_id=$1 AND invitee_id=$2 AND status='pending'`, groupID, userID, at); err != nil {
		return err
	}
	if err = emitGroupSystem(ctx, tx, groupID, actor, "group.blacklist.added", map[string]any{"userId": userID, "remark": remark}, at); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"conversationId": groupID, "userId": userID, "action": "blacklisted"})
	if err = appendUserBusinessEvent(ctx, tx, userID, "group.members.updated", payload, at); err != nil {
		return err
	}
	if err = enqueueWukongChannelReconcile(ctx, tx, groupID, "blacklist-added", at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) AdminRemoveGroupBlacklist(ctx context.Context, actor, groupID, userID, reason string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM im_group_blacklist WHERE conversation_id=$1 AND user_id=$2`, groupID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = emitGroupSystem(ctx, tx, groupID, actor, "group.blacklist.removed", map[string]any{"userId": userID, "reason": reason}, at); err != nil {
		return err
	}
	if err = enqueueWukongChannelReconcile(ctx, tx, groupID, "blacklist-removed", at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) AdminSetGroupMuteAll(ctx context.Context, actor, groupID string, muted bool, reason string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var dissolved *time.Time
	if err = tx.QueryRow(ctx, `SELECT dissolved_at FROM im_groups WHERE conversation_id=$1 FOR UPDATE`, groupID).Scan(&dissolved); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if dissolved != nil {
		return ErrConflict
	}
	var until *time.Time
	if muted {
		value := permanentGroupMuteUntil
		until = &value
	}
	if _, err = tx.Exec(ctx, `UPDATE im_groups SET all_muted_until=$2,updated_at=$3 WHERE conversation_id=$1`, groupID, until, at); err != nil {
		return err
	}
	if err = emitGroupSystem(ctx, tx, groupID, actor, "group.mute_all.updated", map[string]any{"muted": muted, "reason": reason}, at); err != nil {
		return err
	}
	if err = enqueueWukongChannelReconcile(ctx, tx, groupID, "mute-all-updated", at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) AdminSetGroupBan(ctx context.Context, actor, groupID string, banned bool, reason string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var dissolved *time.Time
	var current bool
	if err = tx.QueryRow(ctx, `SELECT dissolved_at,banned FROM im_groups WHERE conversation_id=$1 FOR UPDATE`, groupID).Scan(&dissolved, &current); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if dissolved != nil {
		return ErrConflict
	}
	if current == banned {
		return tx.Commit(ctx)
	}
	if banned {
		_, err = tx.Exec(ctx, `UPDATE im_groups SET banned=true,banned_at=$2,banned_by=$3,ban_reason=$4,updated_at=$2 WHERE conversation_id=$1`, groupID, at, actor, reason)
	} else {
		_, err = tx.Exec(ctx, `UPDATE im_groups SET banned=false,banned_at=NULL,banned_by='',ban_reason='',updated_at=$2 WHERE conversation_id=$1`, groupID, at)
	}
	if err != nil {
		return err
	}
	if err = emitGroupSystem(ctx, tx, groupID, actor, "group.ban.updated", map[string]any{"banned": banned, "reason": reason}, at); err != nil {
		return err
	}
	if err = enqueueWukongChannelReconcile(ctx, tx, groupID, "ban-updated", at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) AdminRecallGroupWukongMessage(ctx context.Context, groupID, messageID, actor, reason string, at time.Time) (bool, int64, []string, error) {
	indexed, err := p.waitForWukongMessageIndex(ctx, messageID)
	if err != nil {
		return false, 0, nil, err
	}
	if !indexed {
		return false, 0, nil, ErrNotFound
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, 0, nil, err
	}
	defer tx.Rollback(ctx)
	meta := wukongMutationMeta{}
	var extension []byte
	var ownerID string
	var expiresAt, expiredAt *time.Time
	err = tx.QueryRow(ctx, `SELECT i.message_id::text,i.conversation_id,i.sender_id,i.channel_id,i.channel_type,i.message_seq,i.content_type,i.message_timestamp,
		COALESCE(ext.payload,'{}'::jsonb),i.expires_at,i.expired_at,g.owner_id FROM im_wukong_message_index i
		JOIN im_groups g ON g.conversation_id=i.conversation_id LEFT JOIN im_wukong_message_extensions ext ON ext.message_id=i.message_id AND ext.channel_id=i.channel_id AND ext.channel_type=i.channel_type
		WHERE i.message_id::text=$1 AND i.conversation_id=$2 FOR UPDATE OF i`, messageID, groupID).Scan(&meta.MessageID, &meta.ConversationID, &meta.SenderID, &meta.ChannelID, &meta.ChannelType, &meta.MessageSeq, &meta.ContentType, &meta.MessageAt, &extension, &expiresAt, &expiredAt, &ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil, ErrNotFound
	}
	if err != nil {
		return false, 0, nil, err
	}
	if err = json.Unmarshal(extension, &meta.Extension); err != nil {
		return false, 0, nil, err
	}
	if meta.Extension["recalledAt"] != nil {
		return true, meta.MessageSeq, nil, tx.Commit(ctx)
	}
	if expiredAt != nil || (expiresAt != nil && !expiresAt.After(at)) {
		return false, 0, nil, ErrConflict
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	meta.Extension["recalledAt"] = stamp
	meta.Extension["revoker"] = "system"
	meta.Extension["adminRecall"] = true
	meta.Extension["moderatedBy"] = actor
	meta.Extension["moderationReason"] = reason
	meta.Extension["moderatedAt"] = stamp
	if err = saveWukongMessageExtension(ctx, tx, meta, ownerID, meta.Extension, at); err != nil {
		return false, 0, nil, err
	}
	payload, _ := json.Marshal(map[string]any{"messageId": messageID, "conversationId": groupID, "conversationSeq": meta.MessageSeq, "adminRecall": true})
	recipients, err := appendMemberBusinessEvent(ctx, tx, groupID, "message.recalled", payload, at)
	if err != nil {
		return false, 0, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, 0, nil, err
	}
	return false, meta.MessageSeq, recipients, nil
}

func (p *Postgres) LoadAdminGroupMessageExtensions(ctx context.Context, groupID string, messageIDs []string) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, len(messageIDs))
	if groupID == "" || len(messageIDs) == 0 {
		return result, nil
	}
	rows, err := p.pool.Query(ctx, `SELECT i.message_id::text,COALESCE(ext.payload,'{}'::jsonb) FROM im_wukong_message_index i LEFT JOIN im_wukong_message_extensions ext ON ext.message_id=i.message_id AND ext.channel_id=i.channel_id AND ext.channel_type=i.channel_type WHERE i.conversation_id=$1 AND i.message_id::text=ANY($2::text[])`, groupID, messageIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		value := map[string]any{}
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		result[id] = value
	}
	return result, rows.Err()
}
