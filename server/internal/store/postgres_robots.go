package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const robotProfileColumns = `s.user_id,u.name,s.robot_username,s.robot_placeholder,s.robot_enabled,s.robot_inline_on,s.robot_version,s.robot_menus,s.updated_by,s.reason,s.updated_at`

func scanRobotProfile(row pgx.Row) (*RobotProfile, error) {
	item := &RobotProfile{}
	var rawMenus []byte
	err := row.Scan(
		&item.UserID,
		&item.Name,
		&item.Username,
		&item.Placeholder,
		&item.Enabled,
		&item.InlineOn,
		&item.Version,
		&rawMenus,
		&item.UpdatedBy,
		&item.Reason,
		&item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(rawMenus) != 0 {
		if err = json.Unmarshal(rawMenus, &item.Menus); err != nil {
			return nil, err
		}
	}
	if item.Menus == nil {
		item.Menus = []RobotMenu{}
	}
	return item, nil
}

func (p *Postgres) ListRobotProfiles(ctx context.Context) ([]*RobotProfile, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+robotProfileColumns+`
		FROM im_wukong_system_users s JOIN im_users u ON u.id=s.user_id
		WHERE s.robot_version>0 ORDER BY s.updated_at DESC,s.user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*RobotProfile{}
	for rows.Next() {
		item, scanErr := scanRobotProfile(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) RobotProfilesForConversation(ctx context.Context, userID, conversationID string) ([]*RobotProfile, error) {
	userID, conversationID = strings.TrimSpace(userID), strings.TrimSpace(conversationID)
	if userID == "" || conversationID == "" {
		return nil, ErrConflict
	}
	var viewerAllowed bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM im_members
		WHERE conversation_id=$1 AND user_id=$2 AND (expires_at IS NULL OR expires_at>now())
	)`, conversationID, userID).Scan(&viewerAllowed); err != nil {
		return nil, err
	}
	if !viewerAllowed {
		return nil, ErrNotFound
	}
	rows, err := p.pool.Query(ctx, `SELECT `+robotProfileColumns+`
		FROM im_wukong_system_users s
		JOIN im_users u ON u.id=s.user_id
		JOIN im_members robot_member ON robot_member.user_id=s.user_id AND robot_member.conversation_id=$1
		WHERE s.enabled=true AND s.robot_enabled=true AND s.robot_version>0
			AND u.banned=false AND u.deleted_at IS NULL
			AND (robot_member.expires_at IS NULL OR robot_member.expires_at>now())
		ORDER BY s.user_id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*RobotProfile{}
	for rows.Next() {
		item, scanErr := scanRobotProfile(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) ConfigureRobotProfile(ctx context.Context, profile RobotProfile, actorID, reason string, at time.Time) (*RobotProfile, error) {
	profile.UserID = strings.TrimSpace(profile.UserID)
	profile.Username = strings.TrimSpace(profile.Username)
	profile.Placeholder = strings.TrimSpace(profile.Placeholder)
	actorID, reason = strings.TrimSpace(actorID), strings.TrimSpace(reason)
	if profile.UserID == "" || actorID == "" || reason == "" {
		return nil, ErrConflict
	}
	rawMenus, err := json.Marshal(profile.Menus)
	if err != nil {
		return nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var userExists, systemEnabled bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1 AND banned=false AND deleted_at IS NULL),COALESCE((SELECT enabled FROM im_wukong_system_users WHERE user_id=$1),false)`, profile.UserID).Scan(&userExists, &systemEnabled); err != nil {
		return nil, err
	}
	if !userExists {
		return nil, ErrNotFound
	}
	if !systemEnabled {
		return nil, ErrForbidden
	}
	updated, err := scanRobotProfile(tx.QueryRow(ctx, `UPDATE im_wukong_system_users s SET
		robot_enabled=$2,robot_username=$3,robot_placeholder=$4,robot_inline_on=$5,
		robot_menus=$6,robot_version=robot_version+1,updated_by=$7,reason=$8,updated_at=$9
		FROM im_users u WHERE s.user_id=$1 AND u.id=s.user_id
		RETURNING `+robotProfileColumns, profile.UserID, profile.Enabled, profile.Username, profile.Placeholder, profile.InlineOn, rawMenus, actorID, reason, at))
	if err != nil {
		return nil, err
	}
	auditID, err := secureOpaqueToken("aud_robot_")
	if err != nil {
		return nil, err
	}
	metadata, _ := json.Marshal(map[string]any{
		"enabled": profile.Enabled, "inlineOn": profile.InlineOn,
		"username": profile.Username, "menuCount": len(profile.Menus), "reason": reason,
	})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'wukong.robot.updated','robot',$3,$4,$5)`, auditID, actorID, profile.UserID, metadata, at); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}

func (p *WithRedis) ListRobotProfiles(ctx context.Context) ([]*RobotProfile, error) {
	if source, ok := p.base.(RobotStore); ok {
		return source.ListRobotProfiles(ctx)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) RobotProfilesForConversation(ctx context.Context, userID, conversationID string) ([]*RobotProfile, error) {
	if source, ok := p.base.(RobotStore); ok {
		return source.RobotProfilesForConversation(ctx, userID, conversationID)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ConfigureRobotProfile(ctx context.Context, profile RobotProfile, actorID, reason string, at time.Time) (*RobotProfile, error) {
	if source, ok := p.base.(RobotStore); ok {
		return source.ConfigureRobotProfile(ctx, profile, actorID, reason, at)
	}
	return nil, ErrUnsupported
}
