package store

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/model"
)

// Wired once during startup. A failed max-sequence read must never grant history.
type GroupHistoryBoundaryReader func(context.Context, string, string) (uint64, error)
type GroupHistoryStore interface {
	SetGroupHistoryBoundaryReader(GroupHistoryBoundaryReader)
	GroupHistoryAccess(context.Context, string, string) (*model.HistoryAccess, error)
	SetAdminGroupHistoryVisibility(context.Context, string, string, bool, string, time.Time) error
}

func (p *Postgres) SetGroupHistoryBoundaryReader(reader GroupHistoryBoundaryReader) {
	p.historyBoundary = reader
}
func (p *WithRedis) SetGroupHistoryBoundaryReader(reader GroupHistoryBoundaryReader) {
	if s, ok := p.base.(GroupHistoryStore); ok {
		s.SetGroupHistoryBoundaryReader(reader)
	}
}
func (p *WithRedis) GroupHistoryAccess(ctx context.Context, uid, cid string) (*model.HistoryAccess, error) {
	if s, ok := p.base.(GroupHistoryStore); ok {
		return s.GroupHistoryAccess(ctx, uid, cid)
	}
	return nil, ErrUnsupported
}
func (p *WithRedis) SetAdminGroupHistoryVisibility(ctx context.Context, actor, cid string, visible bool, reason string, at time.Time) error {
	if s, ok := p.base.(GroupHistoryStore); ok {
		return s.SetAdminGroupHistoryVisibility(ctx, actor, cid, visible, reason, at)
	}
	return ErrUnsupported
}

func (p *Postgres) GroupHistoryAccess(ctx context.Context, uid, cid string) (*model.HistoryAccess, error) {
	h := &model.HistoryAccess{}
	var joined time.Time
	err := p.pool.QueryRow(ctx, `SELECT g.history_policy_version,g.history_visible_to_new_members,m.history_after_seq,m.joined_at,
	 COALESCE(m.history_after_seq,(SELECT max(i.message_seq) FROM im_wukong_message_index i WHERE i.conversation_id=g.conversation_id AND floor(extract(epoch FROM i.message_timestamp))<=floor(extract(epoch FROM m.joined_at))),0)
	 FROM im_groups g JOIN im_members m ON m.conversation_id=g.conversation_id AND m.user_id=$2
	 WHERE g.conversation_id=$1`, cid, uid).Scan(&h.Version, &h.VisibleAll, &h.AfterSeq, &joined, &h.UnreadAfterSeq)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	if h.AfterSeq == nil {
		seconds := joined.Unix()
		h.AfterTimestamp = &seconds
	}
	return h, nil
}

// The group lock serializes policy changes and joins. ON CONFLICT never replaces
// a current membership's cutoff; rejoining after removal obtains a fresh cutoff.
func (p *Postgres) addGroupMembersWithHistory(ctx context.Context, tx pgx.Tx, cid string, ids []string, at time.Time) error {
	var owner string
	if err := tx.QueryRow(ctx, `SELECT owner_id FROM im_groups WHERE conversation_id=$1 AND dissolved_at IS NULL FOR UPDATE`, cid).Scan(&owner); err != nil {
		return err
	}
	var missing int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM im_users u WHERE u.id=ANY($2::text[]) AND NOT EXISTS(SELECT 1 FROM im_members m WHERE m.conversation_id=$1 AND m.user_id=u.id)`, cid, ids).Scan(&missing); err != nil {
		return err
	}
	if missing == 0 {
		return nil
	}
	if p.historyBoundary == nil {
		return ErrUnsupported
	}
	seq, err := p.historyBoundary(ctx, owner, cid)
	if err != nil || seq > math.MaxInt64 {
		return ErrUnsupported
	}
	_, err = tx.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at,history_after_seq,last_read_seq,last_delivered_seq)
	 SELECT $1,id,'member',$3,$4,$4,$4 FROM im_users WHERE id=ANY($2::text[]) ON CONFLICT DO NOTHING`, cid, ids, at, int64(seq))
	return err
}

func setGroupHistoryVisibility(ctx context.Context, tx pgx.Tx, actor, cid string, visible bool, reason string, at time.Time) error {
	var previous bool
	var version int64
	if err := tx.QueryRow(ctx, `SELECT history_visible_to_new_members,history_policy_version FROM im_groups WHERE conversation_id=$1 AND dissolved_at IS NULL FOR UPDATE`, cid).Scan(&previous, &version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if previous == visible {
		return nil
	}
	version++
	if _, err := tx.Exec(ctx, `UPDATE im_groups SET history_visible_to_new_members=$2,history_policy_version=$3,updated_at=$4 WHERE conversation_id=$1`, cid, visible, version, at); err != nil {
		return err
	}
	// Keep read cursors monotonic. Opening history must not create historical unread.
	if _, err := tx.Exec(ctx, `UPDATE im_members SET last_read_seq=GREATEST(last_read_seq,COALESCE(history_after_seq,0)),manual_unread=false WHERE conversation_id=$1`, cid); err != nil {
		return err
	}
	// emitGroupSystem persists the per-member CMD outbox and an audit in this tx.
	return emitGroupSystem(ctx, tx, cid, actor, "group.history.updated", map[string]any{"before": previous, "historyVisibleToNewMembers": visible, "historyPolicyVersion": version, "reason": reason}, at)
}

func (p *Postgres) SetAdminGroupHistoryVisibility(ctx context.Context, actor, cid string, visible bool, reason string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = setGroupHistoryVisibility(ctx, tx, actor, cid, visible, reason, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
