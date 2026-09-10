package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/wukong"
)

type MessageDeletionResult struct {
	ConversationID string   `json:"conversationId"`
	MessageIDs     []string `json:"messageIds"`
	Version        int64    `json:"version"`
}

type MessageDeletionStore interface {
	DeletedUnreadCount(context.Context, string, string, uint8, int64, int64) (int, error)
	DeleteMessagesForEveryone(context.Context, string, string, []string, string) (MessageDeletionResult, error)
}

func (p *Postgres) DeletedUnreadCount(ctx context.Context, uid, channel string, kind uint8, after, last int64) (int, error) {
	var count int
	err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_message_index i JOIN im_conversations c ON c.id=i.conversation_id
 WHERE i.message_seq>$4 AND i.message_seq<=$5 AND i.sender_id<>$1 AND i.channel_type=$3 AND im_message_is_deleted(i.message_id::text)
 AND EXISTS(SELECT 1 FROM im_members m WHERE m.conversation_id=c.id AND m.user_id=$1)
 AND (($3=2 AND c.id=$2) OR ($3=1 AND c.kind='direct' AND EXISTS(SELECT 1 FROM im_members peer WHERE peer.conversation_id=c.id AND peer.user_id=$2)))`, uid, channel, kind, after, last).Scan(&count)
	return count, err
}
func (p *WithRedis) DeletedUnreadCount(ctx context.Context, uid, ch string, kind uint8, after, last int64) (int, error) {
	if s, ok := p.base.(MessageDeletionStore); ok {
		return s.DeletedUnreadCount(ctx, uid, ch, kind, after, last)
	}
	return 0, ErrUnsupported
}

func deletionAudit(ctx context.Context, tx pgx.Tx, actor, action, targetType, target, ip string, data any) error {
	id, err := secureOpaqueToken("aud_")
	if err != nil {
		return err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,result,ip,created_at) VALUES($1,$2,$3,$4,$5,$6,'success',$7,now())`, id, actor, action, targetType, target, raw, ip)
	return err
}

func (p *Postgres) DeleteMessagesForEveryone(ctx context.Context, uid, cid string, ids []string, ip string) (MessageDeletionResult, error) {
	result := MessageDeletionResult{ConversationID: cid, MessageIDs: []string{}}
	if cid == "" || len(ids) == 0 || len(ids) > 100 {
		return result, ErrConflict
	}
	unique := map[string]bool{}
	for _, id := range ids {
		if !looksLikeWukongMessageID(id) {
			return result, ErrNotFound
		}
		if !unique[id] {
			result.MessageIDs = append(result.MessageIDs, id)
			unique[id] = true
		}
	}
	ordered := append([]string(nil), result.MessageIDs...)
	sort.Strings(ordered)
	// One bounded wait for the entire batch, not 100 independent timeouts.
	indexCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	for _, id := range ordered {
		ready, e := p.waitForWukongMessageIndex(indexCtx, id)
		if e != nil || !ready {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			if e != nil && !errors.Is(e, context.DeadlineExceeded) {
				return result, e
			}
			return result, ErrNotFound
		}
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	// Permission revocation takes the same row lock; authorization is never JWT cached.
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT is_internal_user FROM im_users WHERE id=$1 AND NOT banned AND deleted_at IS NULL FOR SHARE`, uid).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !allowed) {
		return result, ErrForbidden
	}
	if err != nil {
		return result, err
	}
	var role, kind string
	var ordinaryGroup bool
	err = tx.QueryRow(ctx, `SELECT m.role,c.kind,EXISTS(SELECT 1 FROM im_groups g WHERE g.conversation_id=c.id AND g.dissolved_at IS NULL)
		FROM im_members m JOIN im_conversations c ON c.id=m.conversation_id
		WHERE m.user_id=$1 AND m.conversation_id=$2 AND (m.expires_at IS NULL OR m.expires_at>now()) FOR SHARE OF m`, uid, cid).Scan(&role, &kind, &ordinaryGroup)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrForbidden
	}
	if err != nil {
		return result, err
	}
	if (kind != "direct" && !ordinaryGroup) || (ordinaryGroup && role != "owner" && role != "admin") {
		return result, ErrForbidden
	}
	if ordinaryGroup {
		var active bool
		if err = tx.QueryRow(ctx, `SELECT dissolved_at IS NULL FROM im_groups WHERE conversation_id=$1 FOR SHARE`, cid).Scan(&active); err != nil {
			return result, err
		}
		if !active {
			return result, ErrForbidden
		}
	}
	changed := []string{}
	now := time.Now().UTC()
	for _, id := range ordered {
		meta, found, e := loadWukongMutationMetaIncludingDeleted(ctx, tx, uid, id)
		if e != nil {
			return result, e
		}
		if !found {
			return result, ErrNotFound
		}
		if meta.ConversationID != cid || (meta.ChannelType != wukong.ChannelPerson && meta.ChannelType != wukong.ChannelGroup) {
			return result, ErrForbidden
		}
		if meta.Extension["deletedForEveryoneAt"] == nil {
			var expired bool
			if e = tx.QueryRow(ctx, `SELECT expired_at IS NOT NULL OR (expires_at IS NOT NULL AND expires_at<=$2) FROM im_wukong_message_index WHERE message_id::text=$1`, id, now).Scan(&expired); e != nil {
				return result, e
			}
			if expired || meta.ContentType == wukong.ContentTypeSystemEvent {
				return result, ErrForbidden
			}
			meta.Extension["deletedForEveryoneAt"] = now.Format(time.RFC3339Nano)
			meta.Extension["deletedForEveryoneBy"] = uid
			if e = saveWukongMessageExtension(ctx, tx, meta, uid, meta.Extension, now); e != nil {
				return result, e
			}
			if _, e = tx.Exec(ctx, `DELETE FROM im_group_message_pins WHERE conversation_id=$1 AND message_id=$2`, cid, id); e != nil {
				return result, e
			}
			changed = append(changed, id)
			if _, e = tx.Exec(ctx, `UPDATE im_wukong_reminders SET done=true,text='',data='{}',version=nextval('im_wukong_reminder_version_seq'),updated_at=$2 WHERE message_id::text=$1 AND NOT done`, id, now); e != nil {
				return result, e
			}
		}
		var version int64
		if e = tx.QueryRow(ctx, `SELECT sync_version FROM im_wukong_message_extensions WHERE message_id::text=$1`, id).Scan(&version); e != nil {
			return result, e
		}
		result.Version = max(result.Version, version)
	}
	if len(changed) > 0 {
		payload, _ := json.Marshal(map[string]any{"conversationId": cid, "messageIds": changed, "version": result.Version})
		if _, err = appendMemberBusinessEvent(ctx, tx, cid, "messages.deleted", payload, now); err != nil {
			return result, err
		}
		if err = deletionAudit(ctx, tx, uid, "messages.deleted_for_everyone", "conversation", cid, ip, map[string]any{"messageIds": changed, "version": result.Version}); err != nil {
			return result, err
		}
	}
	return result, tx.Commit(ctx)
}

func (p *WithRedis) DeleteMessagesForEveryone(ctx context.Context, uid, cid string, ids []string, ip string) (MessageDeletionResult, error) {
	if s, ok := p.base.(MessageDeletionStore); ok {
		return s.DeleteMessagesForEveryone(ctx, uid, cid, ids, ip)
	}
	return MessageDeletionResult{}, ErrForbidden
}
