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
	rows, err := p.pool.Query(ctx, `SELECT u.id FROM im_users u
 WHERE u.id=ANY($2::text[]) AND u.deleted_at IS NULL AND (
  u.id=$1 OR (
   EXISTS(SELECT 1 FROM im_friendships f WHERE f.user_id=$1 AND f.friend_user_id=u.id)
   AND NOT EXISTS(SELECT 1 FROM im_blocks b WHERE (b.user_id=$1 AND b.blocked_user_id=u.id) OR (b.user_id=u.id AND b.blocked_user_id=$1))
  ) OR EXISTS(
   SELECT 1 FROM im_groups g
   JOIN im_members actor ON actor.conversation_id=g.conversation_id AND actor.user_id=$1
   JOIN im_members target ON target.conversation_id=g.conversation_id AND target.user_id=u.id
   WHERE g.conversation_id=$3 AND g.dissolved_at IS NULL AND actor.role IN ('owner','admin')
  ))`, actor, ids, groupID)
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
