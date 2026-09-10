package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type InternalUserUpdate struct {
	UserID         string `json:"userId"`
	IsInternalUser bool   `json:"isInternalUser"`
	Changed        bool   `json:"changed"`
}

type InternalUserStore interface {
	InternalUser(context.Context, string) (bool, error)
	SetInternalUser(context.Context, string, string, bool, string, string) (InternalUserUpdate, error)
}

func (p *Postgres) InternalUser(ctx context.Context, uid string) (bool, error) {
	var internal bool
	err := p.pool.QueryRow(ctx, `SELECT is_internal_user FROM im_users WHERE id=$1 AND deleted_at IS NULL`, uid).Scan(&internal)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return internal, err
}

func (p *Postgres) SetInternalUser(ctx context.Context, actor, uid string, internal bool, reason, requestIP string) (InternalUserUpdate, error) {
	result := InternalUserUpdate{UserID: uid, IsInternalUser: internal}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var previous bool
	err = tx.QueryRow(ctx, `SELECT is_internal_user FROM im_users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, uid).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	result.Changed = previous != internal
	if _, err = tx.Exec(ctx, `UPDATE im_users SET is_internal_user=$2,updated_at=now() WHERE id=$1`, uid, internal); err != nil {
		return result, err
	}
	if err = deletionAudit(ctx, tx, actor, "user.internal_status.updated", "user", uid, requestIP, map[string]any{
		"before": previous, "after": internal, "changed": result.Changed, "reason": strings.TrimSpace(reason),
	}); err != nil {
		return result, err
	}
	if result.Changed {
		changeID, tokenErr := secureOpaqueToken("permission_")
		if tokenErr != nil {
			return result, tokenErr
		}
		payload, _ := json.Marshal(map[string]any{
			"userId": uid, "changeId": changeID, "isInternalUser": internal,
			"actorId": actor, "before": previous, "after": internal, "reason": strings.TrimSpace(reason),
		})
		now := time.Now().UTC()
		if err = appendUserBusinessEvent(ctx, tx, uid, "user.internal_status.updated", payload, now); err != nil {
			return result, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func (p *WithRedis) InternalUser(ctx context.Context, uid string) (bool, error) {
	if s, ok := p.base.(InternalUserStore); ok {
		return s.InternalUser(ctx, uid)
	}
	return false, ErrUnsupported
}

func (p *WithRedis) SetInternalUser(ctx context.Context, actor, uid string, internal bool, reason, requestIP string) (InternalUserUpdate, error) {
	if s, ok := p.base.(InternalUserStore); ok {
		return s.SetInternalUser(ctx, actor, uid, internal, reason, requestIP)
	}
	return InternalUserUpdate{}, ErrUnsupported
}
