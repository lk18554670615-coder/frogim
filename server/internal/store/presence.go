package store

import (
	"context"
	"time"
)

type PresencePermissionStore interface {
	AllowedPresenceTargets(context.Context, string, []string, string) (map[string]bool, error)
}

// PresenceLastOfflineStore exposes only the timestamp needed by the authorized
// presence endpoint. Callers must still apply relationship/group permissions.
type PresenceLastOfflineStore interface {
	PresenceLastOfflineAt(context.Context, []string) (map[string]time.Time, error)
}

func (p *WithRedis) AllowedPresenceTargets(ctx context.Context, actor string, ids []string, groupID string) (map[string]bool, error) {
	if s, ok := p.base.(PresencePermissionStore); ok {
		return s.AllowedPresenceTargets(ctx, actor, ids, groupID)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) PresenceLastOfflineAt(ctx context.Context, ids []string) (map[string]time.Time, error) {
	if s, ok := p.base.(PresenceLastOfflineStore); ok {
		return s.PresenceLastOfflineAt(ctx, ids)
	}
	return nil, ErrUnsupported
}

func (p *Postgres) PresenceLastOfflineAt(ctx context.Context, ids []string) (map[string]time.Time, error) {
	rows, err := p.pool.Query(ctx, `SELECT user_id,last_offline_at
		FROM im_wukong_presence
		WHERE user_id=ANY($1::text[]) AND online=false AND last_offline_at IS NOT NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]time.Time, len(ids))
	for rows.Next() {
		var id string
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		result[id] = at.UTC()
	}
	return result, rows.Err()
}
func (p *Postgres) AllowedPresenceTargets(ctx context.Context, actor string, ids []string, groupID string) (map[string]bool, error) {
	rows, err := p.pool.Query(ctx, `SELECT target.id
		FROM im_users viewer
		JOIN im_users target ON target.id=ANY($2::text[]) AND target.deleted_at IS NULL
		WHERE viewer.id=$1 AND viewer.is_internal_user AND NOT viewer.banned AND viewer.deleted_at IS NULL
		AND NOT EXISTS(
			SELECT 1 FROM im_blocks block_row
			WHERE (block_row.user_id=$1 AND block_row.blocked_user_id=target.id)
				OR (block_row.user_id=target.id AND block_row.blocked_user_id=$1)
		)
		AND (
			($3='' AND EXISTS(
				SELECT 1 FROM im_friendships friendship
				WHERE friendship.user_id=$1 AND friendship.friend_user_id=target.id
			))
			OR ($3<>'' AND EXISTS(
				SELECT 1 FROM im_groups group_row
				JOIN im_members viewer_member
					ON viewer_member.conversation_id=group_row.conversation_id AND viewer_member.user_id=$1
				JOIN im_members target_member
					ON target_member.conversation_id=group_row.conversation_id AND target_member.user_id=target.id
				WHERE group_row.conversation_id=$3 AND group_row.dissolved_at IS NULL
					AND (viewer_member.expires_at IS NULL OR viewer_member.expires_at>now())
					AND (target_member.expires_at IS NULL OR target_member.expires_at>now())
			))
		)`, actor, ids, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}
