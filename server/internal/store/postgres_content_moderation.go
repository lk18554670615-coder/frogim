package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (p *Postgres) ListAdminMoments(ctx context.Context, query, status, cursor string, limit int) ([]*Moment, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	query, status = strings.TrimSpace(query), strings.TrimSpace(status)
	if status != "" && status != "published" && status != "hidden" && status != "deleted" {
		return nil, 0, "", ErrConflict
	}
	pattern := "%" + query + "%"
	where := `($1='' OR moment.id ILIKE $2 OR moment.author_id ILIKE $2 OR author.name ILIKE $2 OR moment.content ILIKE $2) AND ($3='' OR moment.status=$3)`
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_moments moment JOIN im_users author ON author.id=moment.author_id WHERE `+where, query, pattern, status).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, momentSelectSQL+` WHERE ($2='' OR moment.id ILIKE $3 OR moment.author_id ILIKE $3 OR author.name ILIKE $3 OR moment.content ILIKE $3) AND ($4='' OR moment.status=$4) ORDER BY moment.created_at DESC,moment.id DESC LIMIT $5 OFFSET $6`, "", query, pattern, status, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	items := make([]*Moment, 0, limit)
	for rows.Next() {
		item, scanErr := scanMoment(rows)
		if scanErr != nil {
			return nil, 0, "", scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}

func (p *Postgres) ModerateMoment(ctx context.Context, momentID, status, actor, reason string, at time.Time) error {
	momentID, status, actor, reason = strings.TrimSpace(momentID), strings.TrimSpace(status), strings.TrimSpace(actor), strings.TrimSpace(reason)
	if momentID == "" || actor == "" || reason == "" || len([]rune(reason)) > 500 || (status != "published" && status != "hidden" && status != "deleted") || at.IsZero() {
		return ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current string
	if err = tx.QueryRow(ctx, `SELECT status FROM im_moments WHERE id=$1 FOR UPDATE`, momentID).Scan(&current); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if current == "deleted" || current == status {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE im_moments SET status=$2,deleted_at=CASE WHEN $2='deleted' THEN $3::timestamptz ELSE NULL::timestamptz END,updated_at=$3::timestamptz WHERE id=$1`, momentID, status, at); err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{"reason": reason, "from": current, "to": status})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,result,created_at) VALUES($1,$2,'moment.moderated','moment',$3,$4,'success',$5)`, "aud_moment_"+momentID+"_"+at.UTC().Format("20060102150405.000000000"), actor, momentID, metadata, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListAdminStickerPacks(ctx context.Context, query, status, cursor string, limit int) ([]*StickerPack, int64, string, error) {
	offset, limit := pageOffset(cursor, limit)
	query, status = strings.TrimSpace(query), strings.TrimSpace(status)
	if status != "" && status != "draft" && status != "reviewing" && status != "published" && status != "rejected" && status != "disabled" {
		return nil, 0, "", ErrConflict
	}
	pattern := "%" + query + "%"
	where := `($1='' OR pack.id ILIKE $2 OR pack.name ILIKE $2 OR pack.description ILIKE $2 OR pack.created_by ILIKE $2 OR category.name ILIKE $2) AND ($3='' OR pack.status=$3)`
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_sticker_packs pack JOIN im_sticker_categories category ON category.id=pack.category_id WHERE `+where, query, pattern, status).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := p.pool.Query(ctx, stickerPackSelect+` WHERE ($2='' OR pack.id ILIKE $3 OR pack.name ILIKE $3 OR pack.description ILIKE $3 OR pack.created_by ILIKE $3 OR category.name ILIKE $3) AND ($4='' OR pack.status=$4) ORDER BY pack.updated_at DESC,pack.id LIMIT $5 OFFSET $6`, "", query, pattern, status, limit, offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	items := make([]*StickerPack, 0, limit)
	for rows.Next() {
		item, scanErr := scanStickerPack(rows)
		if scanErr != nil {
			return nil, 0, "", scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, "", err
	}
	if err = p.loadStickerItems(ctx, "", items, true); err != nil {
		return nil, 0, "", err
	}
	return items, total, nextPageCursor(offset, len(items), total), nil
}
