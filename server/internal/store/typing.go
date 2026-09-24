package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// TypingRecipientStore resolves ephemeral event recipients from current account
// and membership state, never from cached profiles or JWT claims.
type TypingRecipientStore interface {
	TypingRecipients(context.Context, string, string) ([]string, error)
}

func (p *WithRedis) TypingRecipients(ctx context.Context, actor, cid string) ([]string, error) {
	if s, ok := p.base.(TypingRecipientStore); ok {
		return s.TypingRecipients(ctx, actor, cid)
	}
	return nil, ErrUnsupported
}

func (p *Postgres) TypingRecipients(ctx context.Context, actor, cid string) ([]string, error) {
	var allowed bool
	var recipients []string
	// One snapshot validates the sender, bounds group fanout before filtering,
	// and selects all eligible viewers. Do not turn the 500-member safeguard
	// into a 500-authorized-viewer limit or issue a query for every member.
	err := p.pool.QueryRow(ctx, `WITH members AS MATERIALIZED (
		SELECT user_id,role,expires_at FROM im_members WHERE conversation_id=$2 LIMIT 501
	)
	SELECT EXISTS(
		SELECT 1 FROM im_members sender JOIN im_users u ON u.id=sender.user_id
		WHERE sender.conversation_id=c.id AND sender.user_id=$1
		AND (c.kind<>'group' OR (
			NOT u.banned AND u.deleted_at IS NULL
			AND (sender.expires_at IS NULL OR sender.expires_at>now())
			AND EXISTS(SELECT 1 FROM im_groups g WHERE g.conversation_id=c.id AND g.dissolved_at IS NULL)
		))
	), CASE WHEN (SELECT count(*) FROM members)>500 THEN ARRAY[]::text[] ELSE ARRAY(
		SELECT m.user_id FROM members m JOIN im_users u ON u.id=m.user_id
		WHERE m.user_id<>$1 AND (c.kind<>'group' OR (
			NOT u.banned AND u.deleted_at IS NULL
			AND (m.expires_at IS NULL OR m.expires_at>now())
			AND (u.is_internal_user OR m.role IN ('owner','admin'))
		)) ORDER BY m.user_id
	) END
	FROM im_conversations c WHERE c.id=$2`, actor, cid).Scan(&allowed, &recipients)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrForbidden
	}
	return recipients, nil
}
