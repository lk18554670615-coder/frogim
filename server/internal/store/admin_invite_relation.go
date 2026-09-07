package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrInviteRelationCycle = errors.New("invite relation would form a cycle")
	ErrInviteRelationStale = errors.New("invite relation has changed")
)

type InviteRelationBinding struct {
	InviteCodeID       string    `json:"inviteCodeId"`
	InviteCode         string    `json:"inviteCode"`
	InviterID          string    `json:"inviterId"`
	RegistrationMethod string    `json:"registrationMethod"`
	BindingSource      string    `json:"bindingSource"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
	Version            int64     `json:"version"`
}

// SetAdminInviteRelation corrects only the source relationship, never the user's
// own code or self-change quota. Version zero explicitly means currently unbound.
func (p *Postgres) SetAdminInviteRelation(ctx context.Context, actor, userID, code, reason string, expectedVersion int64, at time.Time) (*InviteRelationBinding, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Serialize graph rewrites, including writes to previously unrelated users.
	// Registration only inserts a fresh leaf and cannot introduce a cycle.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(490739165)`); err != nil {
		return nil, err
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	var inviterID string
	if err = tx.QueryRow(ctx, `SELECT user_id FROM im_user_invite_codes WHERE upper(code)=$1`, code).Scan(&inviterID); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInviteInvalid
	}
	if err != nil {
		return nil, err
	}
	if inviterID == userID {
		return nil, ErrInviteRelationCycle
	}
	// User-before-code matches self-service code changes. Ordered user locks
	// stabilize account availability while competing bans/deletions wait.
	// NO KEY UPDATE permits FK key-share locks taken by concurrent code resets.
	rows, err := tx.Query(ctx, `SELECT id,deleted_at IS NOT NULL,(banned AND (banned_until IS NULL OR banned_until>now())) FROM im_users WHERE id=ANY($1::text[]) ORDER BY id FOR NO KEY UPDATE`, []string{userID, inviterID})
	if err != nil {
		return nil, err
	}
	found := false
	for rows.Next() {
		var id string
		var deleted, banned bool
		if err = rows.Scan(&id, &deleted, &banned); err != nil {
			rows.Close()
			return nil, err
		}
		if id == userID {
			found = !deleted
		}
		if id == inviterID && (deleted || banned) {
			rows.Close()
			return nil, ErrInviteInvalid
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}
	resolved, err := resolveInviteCodeTx(ctx, tx, code)
	if err != nil {
		return nil, err
	}
	before := InviteRelationBinding{}
	err = tx.QueryRow(ctx, `SELECT r.invite_code_id,c.code,r.inviter_user_id,r.registration_method,r.binding_source,r.created_at,r.updated_at,r.version FROM im_user_invite_relations r JOIN im_user_invite_codes c ON c.id=r.invite_code_id WHERE r.invitee_user_id=$1 FOR UPDATE OF r`, userID).Scan(&before.InviteCodeID, &before.InviteCode, &before.InviterID, &before.RegistrationMethod, &before.BindingSource, &before.CreatedAt, &before.UpdatedAt, &before.Version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	// A repeated request for an already applied binding is a harmless no-op.
	if before.InviteCodeID == resolved.ID {
		return &before, tx.Commit(ctx)
	}
	if before.Version != expectedVersion {
		return nil, ErrInviteRelationStale
	}
	var cycle bool
	err = tx.QueryRow(ctx, `WITH RECURSIVE ancestors(id) AS (SELECT $1::text UNION SELECT r.inviter_user_id FROM im_user_invite_relations r JOIN ancestors a ON r.invitee_user_id=a.id) SELECT EXISTS(SELECT 1 FROM ancestors WHERE id=$2)`, inviterID, userID).Scan(&cycle)
	if err != nil {
		return nil, err
	}
	if cycle {
		return nil, ErrInviteRelationCycle
	}
	after := InviteRelationBinding{InviteCodeID: resolved.ID, InviteCode: resolved.Code, InviterID: inviterID, RegistrationMethod: before.RegistrationMethod, BindingSource: "admin", CreatedAt: before.CreatedAt, UpdatedAt: at, Version: before.Version + 1}
	if before.Version == 0 {
		after.RegistrationMethod = "admin"
		after.CreatedAt = at
	}
	_, err = tx.Exec(ctx, `INSERT INTO im_user_invite_relations(invitee_user_id,inviter_user_id,invite_code_id,registration_method,created_at,binding_source,updated_at,version) VALUES($1,$2,$3,$4,$5,'admin',$6,$7) ON CONFLICT(invitee_user_id) DO UPDATE SET inviter_user_id=EXCLUDED.inviter_user_id,invite_code_id=EXCLUDED.invite_code_id,binding_source='admin',updated_at=EXCLUDED.updated_at,version=EXCLUDED.version`, userID, inviterID, resolved.ID, after.RegistrationMethod, after.CreatedAt, at, after.Version)
	if err != nil {
		return nil, err
	}
	auditID, err := secureOpaqueToken("aud_")
	if err != nil {
		return nil, err
	}
	metadata, err := json.Marshal(map[string]any{"before": before, "after": after, "reason": reason})
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,result,created_at) VALUES($1,$2,'invite_relation.changed','user',$3,$4,'success',$5)`, auditID, actor, userID, metadata, at)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &after, nil
}
