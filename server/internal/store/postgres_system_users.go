package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/wukong"
)

func (p *Postgres) WukongSystemUIDs(ctx context.Context) ([]string, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT su.user_id FROM im_wukong_system_users su
		JOIN im_users account ON account.id=su.user_id
		WHERE su.enabled=true AND account.banned=false AND account.deleted_at IS NULL
		ORDER BY su.user_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []string{}
	for rows.Next() {
		var uid string
		if err = rows.Scan(&uid); err != nil {
			return nil, err
		}
		items = append(items, uid)
	}
	return items, rows.Err()
}

func (p *Postgres) IsWukongSystemUser(ctx context.Context, userID string) (bool, error) {
	var enabled bool
	err := p.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM im_wukong_system_users su
			JOIN im_users account ON account.id=su.user_id
			WHERE su.user_id=$1 AND su.enabled=true AND account.banned=false AND account.deleted_at IS NULL
		)
	`, strings.TrimSpace(userID)).Scan(&enabled)
	return enabled, err
}

func scanWukongSystemUser(row pgx.Row) (*WukongSystemUser, error) {
	item := &WukongSystemUser{}
	err := row.Scan(&item.UserID, &item.Name, &item.Enabled, &item.SyncStatus, &item.UpdatedBy, &item.Reason, &item.UpdatedAt)
	return item, err
}

func (p *Postgres) ListWukongSystemUsers(ctx context.Context) ([]*WukongSystemUser, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT s.user_id,u.name,s.enabled,
			CASE WHEN o.status IS NULL OR o.status='completed' THEN 'synced' ELSE o.status END,
			s.updated_by,s.reason,s.updated_at
		FROM im_wukong_system_users s
		JOIN im_users u ON u.id=s.user_id
		LEFT JOIN LATERAL (
			SELECT status FROM im_wukong_outbox
			WHERE aggregate_type='system_user' AND aggregate_id=s.user_id
			ORDER BY id DESC LIMIT 1
		) o ON true
		WHERE s.enabled=true OR COALESCE(o.status,'completed')<>'completed'
		ORDER BY s.updated_at DESC,s.user_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*WukongSystemUser{}
	for rows.Next() {
		item, scanErr := scanWukongSystemUser(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) SetWukongSystemUser(ctx context.Context, userID string, enabled bool, actorID, reason string, at time.Time) (*WukongSystemUser, error) {
	userID, actorID, reason = strings.TrimSpace(userID), strings.TrimSpace(actorID), strings.TrimSpace(reason)
	if userID == "" || actorID == "" || reason == "" {
		return nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var name string
	if err = tx.QueryRow(ctx, `SELECT name FROM im_users WHERE id=$1 AND banned=false`, userID).Scan(&name); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO im_wukong_system_users(user_id,enabled,updated_by,reason,updated_at)
		VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(user_id) DO UPDATE SET enabled=excluded.enabled,updated_by=excluded.updated_by,
			reason=excluded.reason,updated_at=excluded.updated_at,
			robot_enabled=CASE WHEN excluded.enabled THEN im_wukong_system_users.robot_enabled ELSE false END,
			robot_version=CASE WHEN NOT excluded.enabled AND im_wukong_system_users.robot_version>0 THEN im_wukong_system_users.robot_version+1 ELSE im_wukong_system_users.robot_version END
	`, userID, enabled, actorID, reason, at); err != nil {
		return nil, err
	}
	operation := wukong.OperationSystemUIDRemove
	if enabled {
		operation = wukong.OperationSystemUIDAdd
	}
	if err = enqueueWukongOutbox(ctx, tx,
		fmt.Sprintf("system-user:%s:%t:%d", userID, enabled, at.UnixNano()),
		operation, "system_user", userID, map[string]any{"uids": []string{userID}}); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &WukongSystemUser{UserID: userID, Name: name, Enabled: enabled, SyncStatus: "pending", UpdatedBy: actorID, Reason: reason, UpdatedAt: at}, nil
}

func (p *WithRedis) WukongSystemUIDs(ctx context.Context) ([]string, error) {
	if source, ok := p.base.(WukongSystemUserStore); ok {
		return source.WukongSystemUIDs(ctx)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) IsWukongSystemUser(ctx context.Context, userID string) (bool, error) {
	if source, ok := p.base.(WukongSystemUserStore); ok {
		return source.IsWukongSystemUser(ctx, userID)
	}
	return false, ErrUnsupported
}

func (p *WithRedis) ListWukongSystemUsers(ctx context.Context) ([]*WukongSystemUser, error) {
	if source, ok := p.base.(WukongSystemUserStore); ok {
		return source.ListWukongSystemUsers(ctx)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) SetWukongSystemUser(ctx context.Context, userID string, enabled bool, actorID, reason string, at time.Time) (*WukongSystemUser, error) {
	if source, ok := p.base.(WukongSystemUserStore); ok {
		return source.SetWukongSystemUser(ctx, userID, enabled, actorID, reason, at)
	}
	return nil, ErrUnsupported
}
