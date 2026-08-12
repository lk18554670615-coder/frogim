package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/model"
)

const scheduledMessageColumns = `id,user_id,conversation_id,client_msg_id,message_type,body,COALESCE(reply_to_id,''),expires_in_seconds,scheduled_at,status,attempts,COALESCE(last_error,''),COALESCE(sent_message_id,''),created_at,updated_at`

func scanScheduled(row pgx.Row) (*model.ScheduledMessage, error) {
	item := new(model.ScheduledMessage)
	var body []byte
	err := row.Scan(&item.ID, &item.UserID, &item.ConversationID, &item.ClientMsgID, &item.Type, &body, &item.ReplyToID, &item.ExpiresInSeconds, &item.ScheduledAt, &item.Status, &item.Attempts, &item.LastError, &item.SentMessageID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(body, &item.Body); err != nil {
		return nil, err
	}
	return item, nil
}

func (p *Postgres) CreateScheduledMessage(ctx context.Context, item *model.ScheduledMessage) (*model.ScheduledMessage, bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "scheduled:"+item.UserID+":"+item.ClientMsgID); err != nil {
		return nil, false, err
	}
	existing, err := scanScheduled(tx.QueryRow(ctx, `SELECT `+scheduledMessageColumns+` FROM im_scheduled_messages WHERE user_id=$1 AND client_msg_id=$2`, item.UserID, item.ClientMsgID))
	if err == nil {
		if existing.ConversationID != item.ConversationID || existing.Type != item.Type || existing.ReplyToID != item.ReplyToID || existing.ExpiresInSeconds != item.ExpiresInSeconds || !existing.ScheduledAt.Equal(item.ScheduledAt) || !jsonEqual(existing.Body, item.Body) {
			return nil, false, ErrConflict
		}
		return existing, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_members m JOIN im_users u ON u.id=m.user_id LEFT JOIN im_groups g ON g.conversation_id=m.conversation_id WHERE m.conversation_id=$1 AND m.user_id=$2 AND NOT u.banned AND g.dissolved_at IS NULL)`, item.ConversationID, item.UserID).Scan(&allowed); err != nil {
		return nil, false, err
	}
	if !allowed {
		return nil, false, ErrForbidden
	}
	raw, _ := json.Marshal(item.Body)
	created, err := scanScheduled(tx.QueryRow(ctx, `INSERT INTO im_scheduled_messages(id,user_id,conversation_id,client_msg_id,message_type,body,reply_to_id,expires_in_seconds,scheduled_at,status,available_at,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,'pending',$9,$10,$10) RETURNING `+scheduledMessageColumns,
		item.ID, item.UserID, item.ConversationID, item.ClientMsgID, item.Type, raw, item.ReplyToID, item.ExpiresInSeconds, item.ScheduledAt, item.CreatedAt))
	if err != nil {
		return nil, false, err
	}
	payload, _ := json.Marshal(map[string]any{"scheduledMessage": created})
	if err = appendUserBusinessEvent(ctx, tx, item.UserID, "scheduled.created", payload, item.CreatedAt); err != nil {
		return nil, false, err
	}
	return created, false, tx.Commit(ctx)
}

func jsonEqual(a, b map[string]any) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(aa) == string(bb)
}

func (p *Postgres) UpdateScheduledMessage(ctx context.Context, uid, id string, update ScheduledMessageUpdate, at time.Time) (*model.ScheduledMessage, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var body any
	if update.BodySet {
		body, _ = json.Marshal(update.Body)
	}
	item, err := scanScheduled(tx.QueryRow(ctx, `UPDATE im_scheduled_messages SET
		message_type=COALESCE($3,message_type), body=CASE WHEN $4 THEN $5::jsonb ELSE body END,
		reply_to_id=CASE WHEN $6::boolean THEN NULLIF($7,'') ELSE reply_to_id END,
		expires_in_seconds=COALESCE($8,expires_in_seconds), scheduled_at=COALESCE($9,scheduled_at),
		available_at=COALESCE($9,available_at), updated_at=$10
		WHERE id=$1 AND user_id=$2 AND status='pending' RETURNING `+scheduledMessageColumns,
		id, uid, update.Type, update.BodySet, body, update.ReplyToID != nil, stringValue(update.ReplyToID), update.ExpiresInSeconds, update.ScheduledAt, at))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, p.scheduledMutationError(ctx, tx, uid, id)
	}
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]any{"scheduledMessage": item})
	if err = appendUserBusinessEvent(ctx, tx, uid, "scheduled.updated", payload, at); err != nil {
		return nil, err
	}
	return item, tx.Commit(ctx)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (p *Postgres) scheduledMutationError(ctx context.Context, tx pgx.Tx, uid, id string) error {
	var owner, status string
	err := tx.QueryRow(ctx, `SELECT user_id,status FROM im_scheduled_messages WHERE id=$1`, id).Scan(&owner, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if owner != uid {
		return ErrForbidden
	}
	return ErrConflict
}

func (p *Postgres) CancelScheduledMessage(ctx context.Context, uid, id string, at time.Time) (*model.ScheduledMessage, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	item, err := scanScheduled(tx.QueryRow(ctx, `UPDATE im_scheduled_messages SET status='cancelled',updated_at=$3 WHERE id=$1 AND user_id=$2 AND status='pending' RETURNING `+scheduledMessageColumns, id, uid, at))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, p.scheduledMutationError(ctx, tx, uid, id)
	}
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]any{"scheduledMessage": item})
	if err = appendUserBusinessEvent(ctx, tx, uid, "scheduled.cancelled", payload, at); err != nil {
		return nil, err
	}
	return item, tx.Commit(ctx)
}

func (p *Postgres) ListScheduledMessages(ctx context.Context, uid, status string, limit int) ([]*model.ScheduledMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx, `SELECT `+scheduledMessageColumns+` FROM im_scheduled_messages WHERE user_id=$1 AND ($2='' OR status=$2) ORDER BY scheduled_at,id LIMIT $3`, uid, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*model.ScheduledMessage, 0)
	for rows.Next() {
		item, err := scanScheduled(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) LeaseScheduledMessages(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]*model.ScheduledMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := p.pool.Query(ctx, `WITH picked AS (
		SELECT id FROM im_scheduled_messages WHERE
		(status='pending' AND scheduled_at<=$1 AND available_at<=$1) OR
		(status='processing' AND locked_at<$2)
		ORDER BY scheduled_at,id FOR UPDATE SKIP LOCKED LIMIT $3
	) UPDATE im_scheduled_messages s SET status='processing',attempts=attempts+1,locked_at=$1,updated_at=$1
	FROM picked WHERE s.id=picked.id RETURNING `+scheduledPrefixedColumns("s"), now, now.Add(-lease), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*model.ScheduledMessage, 0)
	for rows.Next() {
		item, err := scanScheduled(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scheduledPrefixedColumns(alias string) string {
	return alias + `.id,` + alias + `.user_id,` + alias + `.conversation_id,` + alias + `.client_msg_id,` + alias + `.message_type,` + alias + `.body,COALESCE(` + alias + `.reply_to_id,''),` + alias + `.expires_in_seconds,` + alias + `.scheduled_at,` + alias + `.status,` + alias + `.attempts,COALESCE(` + alias + `.last_error,''),COALESCE(` + alias + `.sent_message_id,''),` + alias + `.created_at,` + alias + `.updated_at`
}

func (p *Postgres) CompleteScheduledMessage(ctx context.Context, id, messageID string, sendErr error, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var item *model.ScheduledMessage
	if sendErr == nil {
		item, err = scanScheduled(tx.QueryRow(ctx, `UPDATE im_scheduled_messages SET status='sent',sent_message_id=$2,last_error='',locked_at=NULL,updated_at=$3 WHERE id=$1 AND status='processing' RETURNING `+scheduledMessageColumns, id, messageID, at))
	} else {
		delay := 5 * time.Second
		item, err = scanScheduled(tx.QueryRow(ctx, `UPDATE im_scheduled_messages SET status=CASE WHEN attempts>=10 THEN 'failed' ELSE 'pending' END,last_error=$2,available_at=$3,locked_at=NULL,updated_at=$4 WHERE id=$1 AND status='processing' RETURNING `+scheduledMessageColumns, id, truncateError(sendErr), at.Add(delay), at))
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"scheduledMessage": item})
	if err = appendUserBusinessEvent(ctx, tx, item.UserID, "scheduled."+item.Status, payload, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func truncateError(err error) string {
	value := err.Error()
	if len(value) > 500 {
		return value[:500]
	}
	return value
}

func (p *Postgres) ExpireMessages(ctx context.Context, at time.Time, limit int) ([]ExpiredMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `WITH picked AS (
		SELECT id FROM im_messages WHERE expires_at<=$1 AND expired_at IS NULL ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT $2
	) UPDATE im_messages m SET body='{}'::jsonb,expired_at=$1 FROM picked WHERE m.id=picked.id RETURNING m.id,m.conversation_id,m.conversation_seq`, at, limit)
	if err != nil {
		return nil, err
	}
	items := make([]ExpiredMessage, 0)
	ids := make([]string, 0)
	for rows.Next() {
		item := ExpiredMessage{ExpiredAt: at}
		if err = rows.Scan(&item.MessageID, &item.ConversationID, &item.ConversationSeq); err != nil {
			rows.Close()
			return nil, err
		}
		items, ids = append(items, item), append(ids, item.MessageID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	wukongIDs := make([]int64, 0)
	rows, err = tx.Query(ctx, `WITH picked AS (
		SELECT message_id FROM im_wukong_message_index
		WHERE expires_at IS NOT NULL AND expired_at IS NULL AND expires_at<=$1
		  AND conversation_id IS NOT NULL
		ORDER BY expires_at,message_id
		FOR UPDATE SKIP LOCKED LIMIT $2
	) UPDATE im_wukong_message_index message_index SET expired_at=message_index.expires_at
	  FROM picked WHERE message_index.message_id=picked.message_id
	  RETURNING message_index.message_id,message_index.conversation_id,message_index.message_seq,message_index.expired_at`, at, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var messageID int64
		item := ExpiredMessage{}
		if err = rows.Scan(&messageID, &item.ConversationID, &item.ConversationSeq, &item.ExpiredAt); err != nil {
			rows.Close()
			return nil, err
		}
		item.MessageID = strconv.FormatInt(messageID, 10)
		items = append(items, item)
		ids = append(ids, item.MessageID)
		wukongIDs = append(wukongIDs, messageID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		for _, statement := range []string{
			`DELETE FROM im_message_reactions WHERE message_id=ANY($1::text[])`,
			`DELETE FROM im_group_message_pins WHERE message_id=ANY($1::text[])`,
			`DELETE FROM im_favorites WHERE message_id=ANY($1::text[])`,
		} {
			if _, err = tx.Exec(ctx, statement, ids); err != nil {
				return nil, err
			}
		}
	}
	if len(wukongIDs) > 0 {
		for _, statement := range []string{
			`DELETE FROM im_wukong_message_extensions WHERE message_id=ANY($1::bigint[])`,
			`DELETE FROM im_wukong_reminders WHERE message_id=ANY($1::bigint[])`,
		} {
			if _, err = tx.Exec(ctx, statement, wukongIDs); err != nil {
				return nil, err
			}
		}
		// Release a channel's media authorization only when no still-visible
		// WuKong message in that channel references the same durable media.
		if _, err = tx.Exec(ctx, `DELETE FROM im_wukong_media_channels binding
			USING im_wukong_message_index expired
			WHERE expired.message_id=ANY($1::bigint[]) AND expired.media_id<>''
			  AND binding.media_id=expired.media_id
			  AND binding.channel_id=expired.channel_id AND binding.channel_type=expired.channel_type
			  AND NOT EXISTS(
				SELECT 1 FROM im_wukong_message_index active
				WHERE active.media_id=binding.media_id AND active.channel_id=binding.channel_id
				  AND active.channel_type=binding.channel_type AND active.expired_at IS NULL
				  AND (active.expires_at IS NULL OR active.expires_at>$2)
			  )`, wukongIDs, at); err != nil {
			return nil, err
		}
	}
	for index := range items {
		payload, _ := json.Marshal(map[string]any{"messageId": items[index].MessageID, "conversationId": items[index].ConversationID, "conversationSeq": items[index].ConversationSeq, "expiredAt": items[index].ExpiredAt})
		members, syncErr := appendMemberBusinessEvent(ctx, tx, items[index].ConversationID, "message.expired", payload, at)
		if syncErr != nil {
			return nil, syncErr
		}
		items[index].MemberIDs = members
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *Postgres) LeaseMediaCleanup(ctx context.Context, now time.Time, pendingAge, orphanAge, lease time.Duration, limit int) ([]MediaCleanupItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := p.pool.Query(ctx, `WITH picked AS (
		SELECT media.id FROM im_media media WHERE (
			(media.status='pending' AND media.created_at<$2) OR
			(media.status='ready' AND COALESCE(media.completed_at,media.created_at)<$3 AND
			 NOT EXISTS(SELECT 1 FROM im_messages message WHERE message.body->>'mediaId'=media.id AND message.recalled_at IS NULL AND message.expired_at IS NULL) AND
			 NOT EXISTS(SELECT 1 FROM im_wukong_media_channels binding WHERE binding.media_id=media.id) AND
			 NOT EXISTS(SELECT 1 FROM im_moments moment WHERE media.id=ANY(moment.media_ids) AND moment.status<>'deleted') AND
			 NOT EXISTS(SELECT 1 FROM im_sticker_packs pack WHERE pack.cover_media_id=media.id) AND
			 NOT EXISTS(SELECT 1 FROM im_sticker_items item WHERE item.media_id=media.id) AND
			 NOT EXISTS(SELECT 1 FROM im_users user_row WHERE user_row.avatar_media_id=media.id))
		) AND (media.cleanup_status IN ('','pending') OR (media.cleanup_status='processing' AND media.cleanup_locked_at<$4))
		ORDER BY media.created_at,media.id FOR UPDATE SKIP LOCKED LIMIT $5
	) UPDATE im_media media SET cleanup_status='processing',cleanup_locked_at=$1,cleanup_attempts=cleanup_attempts+1,cleanup_updated_at=$1
	FROM picked WHERE media.id=picked.id RETURNING media.id,media.object_key`, now, now.Add(-pendingAge), now.Add(-orphanAge), now.Add(-lease), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]MediaCleanupItem, 0)
	for rows.Next() {
		var item MediaCleanupItem
		if err = rows.Scan(&item.ID, &item.ObjectKey); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) CompleteMediaCleanup(ctx context.Context, id string, cleanupErr error, at time.Time) error {
	if cleanupErr == nil {
		tag, err := p.pool.Exec(ctx, `DELETE FROM im_media WHERE id=$1 AND cleanup_status='processing'`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrConflict
		}
		return nil
	}
	tag, err := p.pool.Exec(ctx, `UPDATE im_media SET cleanup_status='pending',cleanup_locked_at=NULL,cleanup_last_error=$2,cleanup_updated_at=$3 WHERE id=$1 AND cleanup_status='processing'`, id, truncateError(cleanupErr), at)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return err
}

func (p *Postgres) MediaCleanupStatus(ctx context.Context, now time.Time, pendingAge, orphanAge time.Duration) (MediaCleanupStatus, error) {
	var status MediaCleanupStatus
	err := p.pool.QueryRow(ctx, `SELECT
		count(*) FILTER(WHERE status='pending' AND created_at<$1),
		count(*) FILTER(WHERE status='ready' AND COALESCE(completed_at,created_at)<$2 AND
		 NOT EXISTS(SELECT 1 FROM im_messages message WHERE message.body->>'mediaId'=im_media.id AND message.recalled_at IS NULL AND message.expired_at IS NULL) AND
		 NOT EXISTS(SELECT 1 FROM im_wukong_media_channels binding WHERE binding.media_id=im_media.id) AND
		 NOT EXISTS(SELECT 1 FROM im_users user_row WHERE user_row.avatar_media_id=im_media.id)),
		count(*) FILTER(WHERE cleanup_status='processing'),
		COALESCE(sum(cleanup_attempts) FILTER(WHERE cleanup_last_error<>''),0),
		max(cleanup_updated_at)
		FROM im_media`, now.Add(-pendingAge), now.Add(-orphanAge)).Scan(&status.PendingCandidates, &status.OrphanCandidates, &status.Processing, &status.FailedAttempts, &status.LastRunAt)
	return status, err
}
