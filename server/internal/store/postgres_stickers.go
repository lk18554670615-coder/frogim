package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func scanStickerCategory(row pgx.Row) (*StickerCategory, error) {
	item := &StickerCategory{}
	err := row.Scan(&item.ID, &item.Name, &item.SortOrder, &item.Enabled, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

func (p *Postgres) SaveStickerCategory(ctx context.Context, input StickerCategoryInput) (*StickerCategory, error) {
	input.ID, input.Name = strings.TrimSpace(input.ID), strings.TrimSpace(input.Name)
	if input.ID == "" || input.Name == "" || len([]rune(input.Name)) > 80 || input.At.IsZero() {
		return nil, ErrConflict
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO im_sticker_categories(id,name,sort_order,enabled,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$5) ON CONFLICT(id) DO UPDATE SET name=excluded.name,
		sort_order=excluded.sort_order,enabled=excluded.enabled,updated_at=excluded.updated_at`,
		input.ID, input.Name, input.SortOrder, input.Enabled, input.At)
	if err != nil {
		return nil, err
	}
	return scanStickerCategory(p.pool.QueryRow(ctx, `SELECT id,name,sort_order,enabled,created_at,updated_at
		FROM im_sticker_categories WHERE id=$1`, input.ID))
}

func (p *Postgres) ListStickerCategories(ctx context.Context, includeDisabled bool) ([]*StickerCategory, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,name,sort_order,enabled,created_at,updated_at
		FROM im_sticker_categories WHERE $1 OR enabled ORDER BY sort_order,id`, includeDisabled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*StickerCategory, 0)
	for rows.Next() {
		item, scanErr := scanStickerCategory(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const stickerPackSelect = `SELECT pack.id,pack.category_id,category.name,pack.name,pack.description,
	pack.cover_media_id,cover.mime,pack.status,pack.sort_order,
	EXISTS(SELECT 1 FROM im_sticker_pack_favorites favorite WHERE favorite.pack_id=pack.id AND favorite.user_id=$1),
	pack.created_by,COALESCE(pack.reviewed_by,''),pack.review_reason,pack.reviewed_at,pack.created_at,pack.updated_at
	FROM im_sticker_packs pack JOIN im_sticker_categories category ON category.id=pack.category_id
	JOIN im_media cover ON cover.id=pack.cover_media_id`

func scanStickerPack(row pgx.Row) (*StickerPack, error) {
	item := &StickerPack{}
	err := row.Scan(&item.ID, &item.CategoryID, &item.CategoryName, &item.Name, &item.Description,
		&item.CoverMediaID, &item.CoverMIME, &item.Status, &item.SortOrder, &item.Favorite,
		&item.CreatedBy, &item.ReviewedBy, &item.ReviewReason, &item.ReviewedAt, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

func (p *Postgres) SaveStickerPack(ctx context.Context, input StickerPackInput) (*StickerPack, error) {
	input.ID, input.CategoryID, input.Name = strings.TrimSpace(input.ID), strings.TrimSpace(input.CategoryID), strings.TrimSpace(input.Name)
	input.Description, input.CoverMediaID = strings.TrimSpace(input.Description), strings.TrimSpace(input.CoverMediaID)
	input.Status, input.ActorID = strings.TrimSpace(input.Status), strings.TrimSpace(input.ActorID)
	if input.Status == "" {
		input.Status = "draft"
	}
	if input.ID == "" || input.CategoryID == "" || input.Name == "" || input.CoverMediaID == "" || input.ActorID == "" ||
		len([]rune(input.Name)) > 100 || len([]rune(input.Description)) > 2000 ||
		(input.Status != "draft" && input.Status != "reviewing") || input.At.IsZero() {
		return nil, ErrConflict
	}
	var valid bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_sticker_categories category
		JOIN im_media media ON media.id=$2 AND media.status='ready' AND media.mime LIKE 'image/%'
		WHERE category.id=$1)`, input.CategoryID, input.CoverMediaID).Scan(&valid); err != nil {
		return nil, err
	}
	if !valid {
		return nil, ErrNotFound
	}
	tag, err := p.pool.Exec(ctx, `INSERT INTO im_sticker_packs(
		id,category_id,name,description,cover_media_id,status,sort_order,created_by,created_at,updated_at
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
	ON CONFLICT(id) DO UPDATE SET category_id=excluded.category_id,name=excluded.name,
		description=excluded.description,cover_media_id=excluded.cover_media_id,status=excluded.status,
		sort_order=excluded.sort_order,reviewed_by=NULL,review_reason='',reviewed_at=NULL,updated_at=excluded.updated_at
	WHERE im_sticker_packs.status IN ('draft','reviewing','rejected','disabled')`, input.ID, input.CategoryID,
		input.Name, input.Description, input.CoverMediaID, input.Status, input.SortOrder, input.ActorID, input.At)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, ErrConflict
	}
	return p.GetStickerPack(ctx, input.ActorID, input.ID, true)
}

func (p *Postgres) ReviewStickerPack(ctx context.Context, packID, status, reason, actorID string, at time.Time) (*StickerPack, error) {
	packID, status, reason, actorID = strings.TrimSpace(packID), strings.TrimSpace(status), strings.TrimSpace(reason), strings.TrimSpace(actorID)
	if packID == "" || actorID == "" || len([]rune(reason)) > 2000 ||
		(status != "published" && status != "rejected" && status != "disabled" && status != "reviewing") || at.IsZero() {
		return nil, ErrConflict
	}
	if status == "rejected" && reason == "" {
		return nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var previous string
	if err = tx.QueryRow(ctx, `SELECT status FROM im_sticker_packs WHERE id=$1 FOR UPDATE`, packID).Scan(&previous); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `UPDATE im_sticker_packs SET status=$2,reviewed_by=$3,review_reason=$4,reviewed_at=$5,updated_at=$5
		WHERE id=$1 AND (
			($2 IN ('published','rejected') AND status='reviewing') OR
			($2='disabled' AND status='published') OR
			($2='reviewing' AND status IN ('draft','rejected','disabled'))
		)`, packID, status, actorID, reason, at)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, ErrConflict
	}
	metadata, _ := json.Marshal(map[string]any{"reason": reason, "from": previous, "to": status})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,result,created_at) VALUES($1,$2,'sticker_pack.reviewed','sticker_pack',$3,$4,'success',$5)`, "aud_sticker_"+packID+"_"+at.UTC().Format("20060102150405.000000000"), actorID, packID, metadata, at); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.GetStickerPack(ctx, actorID, packID, true)
}

func scanStickerItem(row pgx.Row) (*StickerItem, error) {
	item := &StickerItem{}
	var metadata []byte
	err := row.Scan(&item.ID, &item.PackID, &item.Name, &item.MediaID, &item.MIME, &item.Emoji,
		&item.SortOrder, &item.Status, &metadata, &item.Favorite, &item.UseCount, &item.UsedAt,
		&item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(metadata, &item.Metadata); err != nil {
		return nil, err
	}
	return item, nil
}

func (p *Postgres) SaveStickerItem(ctx context.Context, input StickerItemInput) (*StickerItem, error) {
	input.ID, input.PackID, input.Name = strings.TrimSpace(input.ID), strings.TrimSpace(input.PackID), strings.TrimSpace(input.Name)
	input.MediaID, input.Emoji, input.Status = strings.TrimSpace(input.MediaID), strings.TrimSpace(input.Emoji), strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "published"
	}
	if input.ID == "" || input.PackID == "" || input.Name == "" || input.MediaID == "" ||
		len([]rune(input.Name)) > 100 || len([]rune(input.Emoji)) > 32 ||
		(input.Status != "published" && input.Status != "disabled") || input.At.IsZero() {
		return nil, ErrConflict
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	metadata, err := json.Marshal(input.Metadata)
	if err != nil || len(metadata) > 16<<10 {
		return nil, ErrConflict
	}
	var valid bool
	if err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_sticker_packs pack
		JOIN im_media media ON media.id=$2 AND media.status='ready' AND media.mime LIKE 'image/%'
		WHERE pack.id=$1 AND pack.status IN ('draft','reviewing','rejected','disabled'))`, input.PackID, input.MediaID).Scan(&valid); err != nil {
		return nil, err
	}
	if !valid {
		return nil, ErrNotFound
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO im_sticker_items(
		id,pack_id,name,media_id,emoji,sort_order,status,metadata,created_at,updated_at
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
	ON CONFLICT(id) DO UPDATE SET pack_id=excluded.pack_id,name=excluded.name,media_id=excluded.media_id,
		emoji=excluded.emoji,sort_order=excluded.sort_order,status=excluded.status,metadata=excluded.metadata,updated_at=excluded.updated_at`,
		input.ID, input.PackID, input.Name, input.MediaID, input.Emoji, input.SortOrder, input.Status, metadata, input.At)
	if err != nil {
		return nil, err
	}
	return p.loadStickerItem(ctx, "", input.ID, true)
}

const stickerItemSelect = `SELECT item.id,item.pack_id,item.name,item.media_id,media.mime,item.emoji,
	item.sort_order,item.status,item.metadata,
	EXISTS(SELECT 1 FROM im_sticker_item_favorites favorite WHERE favorite.sticker_id=item.id AND favorite.user_id=$1),
	COALESCE(recent.use_count,0),recent.used_at,item.created_at,item.updated_at
	FROM im_sticker_items item JOIN im_media media ON media.id=item.media_id
	LEFT JOIN im_sticker_recent recent ON recent.sticker_id=item.id AND recent.user_id=$1`

func (p *Postgres) loadStickerItem(ctx context.Context, userID, stickerID string, includeUnpublished bool) (*StickerItem, error) {
	return scanStickerItem(p.pool.QueryRow(ctx, stickerItemSelect+`
		JOIN im_sticker_packs pack ON pack.id=item.pack_id
		WHERE item.id=$2 AND ($3 OR (item.status='published' AND pack.status='published'))`, userID, stickerID, includeUnpublished))
}

func (p *Postgres) loadStickerItems(ctx context.Context, userID string, packs []*StickerPack, includeUnpublished bool) error {
	if len(packs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(packs))
	byID := make(map[string]*StickerPack, len(packs))
	for _, pack := range packs {
		ids = append(ids, pack.ID)
		byID[pack.ID] = pack
		pack.Items = make([]*StickerItem, 0)
	}
	rows, err := p.pool.Query(ctx, stickerItemSelect+`
		WHERE item.pack_id=ANY($2::text[]) AND ($3 OR item.status='published')
		ORDER BY item.pack_id,item.sort_order,item.id`, userID, ids, includeUnpublished)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		item, scanErr := scanStickerItem(rows)
		if scanErr != nil {
			return scanErr
		}
		if pack := byID[item.PackID]; pack != nil {
			pack.Items = append(pack.Items, item)
		}
	}
	return rows.Err()
}

func (p *Postgres) ListStickerPacks(ctx context.Context, userID, categoryID string, includeUnpublished bool) ([]*StickerPack, error) {
	rows, err := p.pool.Query(ctx, stickerPackSelect+`
		WHERE ($2='' OR pack.category_id=$2) AND ($3 OR (pack.status='published' AND category.enabled))
		ORDER BY category.sort_order,pack.sort_order,pack.id`, strings.TrimSpace(userID), strings.TrimSpace(categoryID), includeUnpublished)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*StickerPack, 0)
	for rows.Next() {
		item, scanErr := scanStickerPack(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = p.loadStickerItems(ctx, userID, items, includeUnpublished); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *Postgres) GetStickerPack(ctx context.Context, userID, packID string, includeUnpublished bool) (*StickerPack, error) {
	item, err := scanStickerPack(p.pool.QueryRow(ctx, stickerPackSelect+`
		WHERE pack.id=$2 AND ($3 OR (pack.status='published' AND category.enabled))`, strings.TrimSpace(userID), strings.TrimSpace(packID), includeUnpublished))
	if err != nil {
		return nil, err
	}
	if err = p.loadStickerItems(ctx, userID, []*StickerPack{item}, includeUnpublished); err != nil {
		return nil, err
	}
	return item, nil
}

func (p *Postgres) SetStickerPackFavorite(ctx context.Context, userID, packID string, active bool, at time.Time) error {
	if active {
		tag, err := p.pool.Exec(ctx, `INSERT INTO im_sticker_pack_favorites(user_id,pack_id,created_at)
			SELECT $1,pack.id,$3 FROM im_sticker_packs pack WHERE pack.id=$2 AND pack.status='published'
			ON CONFLICT(user_id,pack_id) DO NOTHING`, strings.TrimSpace(userID), strings.TrimSpace(packID), at)
		if err == nil && tag.RowsAffected() == 0 {
			var exists bool
			err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_sticker_pack_favorites WHERE user_id=$1 AND pack_id=$2)`, userID, packID).Scan(&exists)
			if err == nil && !exists {
				return ErrNotFound
			}
		}
		return err
	}
	_, err := p.pool.Exec(ctx, `DELETE FROM im_sticker_pack_favorites WHERE user_id=$1 AND pack_id=$2`, strings.TrimSpace(userID), strings.TrimSpace(packID))
	return err
}

func (p *Postgres) SetStickerFavorite(ctx context.Context, userID, stickerID string, active bool, at time.Time) error {
	if active {
		tag, err := p.pool.Exec(ctx, `INSERT INTO im_sticker_item_favorites(user_id,sticker_id,created_at)
			SELECT $1,item.id,$3 FROM im_sticker_items item JOIN im_sticker_packs pack ON pack.id=item.pack_id
			WHERE item.id=$2 AND item.status='published' AND pack.status='published'
			ON CONFLICT(user_id,sticker_id) DO NOTHING`, strings.TrimSpace(userID), strings.TrimSpace(stickerID), at)
		if err == nil && tag.RowsAffected() == 0 {
			var exists bool
			err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_sticker_item_favorites WHERE user_id=$1 AND sticker_id=$2)`, userID, stickerID).Scan(&exists)
			if err == nil && !exists {
				return ErrNotFound
			}
		}
		return err
	}
	_, err := p.pool.Exec(ctx, `DELETE FROM im_sticker_item_favorites WHERE user_id=$1 AND sticker_id=$2`, strings.TrimSpace(userID), strings.TrimSpace(stickerID))
	return err
}

func (p *Postgres) RecordStickerUse(ctx context.Context, userID, stickerID string, at time.Time) error {
	tag, err := p.pool.Exec(ctx, `INSERT INTO im_sticker_recent(user_id,sticker_id,use_count,used_at)
		SELECT $1,item.id,1,$3 FROM im_sticker_items item JOIN im_sticker_packs pack ON pack.id=item.pack_id
		WHERE item.id=$2 AND item.status='published' AND pack.status='published'
		ON CONFLICT(user_id,sticker_id) DO UPDATE SET use_count=im_sticker_recent.use_count+1,used_at=excluded.used_at`,
		strings.TrimSpace(userID), strings.TrimSpace(stickerID), at)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (p *Postgres) listUserStickers(ctx context.Context, userID string, recent bool, limit int) ([]*StickerItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	join := `JOIN im_sticker_item_favorites source ON source.sticker_id=item.id AND source.user_id=$1`
	order := `source.created_at DESC,item.id`
	if recent {
		join = `JOIN im_sticker_recent source ON source.sticker_id=item.id AND source.user_id=$1`
		order = `source.used_at DESC,item.id`
	}
	rows, err := p.pool.Query(ctx, stickerItemSelect+` `+join+`
		JOIN im_sticker_packs pack ON pack.id=item.pack_id
		WHERE item.status='published' AND pack.status='published' ORDER BY `+order+` LIMIT $2`, strings.TrimSpace(userID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*StickerItem, 0)
	for rows.Next() {
		item, scanErr := scanStickerItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) ListRecentStickers(ctx context.Context, userID string, limit int) ([]*StickerItem, error) {
	return p.listUserStickers(ctx, userID, true, limit)
}

func (p *Postgres) ListFavoriteStickers(ctx context.Context, userID string, limit int) ([]*StickerItem, error) {
	return p.listUserStickers(ctx, userID, false, limit)
}

func (p *Postgres) CanUseSticker(ctx context.Context, userID, stickerID string) (bool, error) {
	var allowed bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users user_row
		JOIN im_sticker_items item ON item.id=$2 AND item.status='published'
		JOIN im_sticker_packs pack ON pack.id=item.pack_id AND pack.status='published'
		JOIN im_sticker_categories category ON category.id=pack.category_id AND category.enabled
		WHERE user_row.id=$1 AND user_row.deleted_at IS NULL AND NOT user_row.banned)`, strings.TrimSpace(userID), strings.TrimSpace(stickerID)).Scan(&allowed)
	return allowed, err
}

func (p *WithRedis) SaveStickerCategory(ctx context.Context, input StickerCategoryInput) (*StickerCategory, error) {
	if store, ok := p.base.(StickerStore); ok {
		return store.SaveStickerCategory(ctx, input)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListStickerCategories(ctx context.Context, includeDisabled bool) ([]*StickerCategory, error) {
	if store, ok := p.base.(StickerStore); ok {
		return store.ListStickerCategories(ctx, includeDisabled)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) SaveStickerPack(ctx context.Context, input StickerPackInput) (*StickerPack, error) {
	if store, ok := p.base.(StickerStore); ok {
		return store.SaveStickerPack(ctx, input)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ReviewStickerPack(ctx context.Context, packID, status, reason, actorID string, at time.Time) (*StickerPack, error) {
	if store, ok := p.base.(StickerStore); ok {
		return store.ReviewStickerPack(ctx, packID, status, reason, actorID, at)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) SaveStickerItem(ctx context.Context, input StickerItemInput) (*StickerItem, error) {
	if store, ok := p.base.(StickerStore); ok {
		return store.SaveStickerItem(ctx, input)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListStickerPacks(ctx context.Context, userID, categoryID string, includeUnpublished bool) ([]*StickerPack, error) {
	if store, ok := p.base.(StickerStore); ok {
		return store.ListStickerPacks(ctx, userID, categoryID, includeUnpublished)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) GetStickerPack(ctx context.Context, userID, packID string, includeUnpublished bool) (*StickerPack, error) {
	if store, ok := p.base.(StickerStore); ok {
		return store.GetStickerPack(ctx, userID, packID, includeUnpublished)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) SetStickerPackFavorite(ctx context.Context, userID, packID string, active bool, at time.Time) error {
	if store, ok := p.base.(StickerStore); ok {
		return store.SetStickerPackFavorite(ctx, userID, packID, active, at)
	}
	return ErrUnsupported
}

func (p *WithRedis) SetStickerFavorite(ctx context.Context, userID, stickerID string, active bool, at time.Time) error {
	if store, ok := p.base.(StickerStore); ok {
		return store.SetStickerFavorite(ctx, userID, stickerID, active, at)
	}
	return ErrUnsupported
}

func (p *WithRedis) RecordStickerUse(ctx context.Context, userID, stickerID string, at time.Time) error {
	if store, ok := p.base.(StickerStore); ok {
		return store.RecordStickerUse(ctx, userID, stickerID, at)
	}
	return ErrUnsupported
}

func (p *WithRedis) ListRecentStickers(ctx context.Context, userID string, limit int) ([]*StickerItem, error) {
	if store, ok := p.base.(StickerStore); ok {
		return store.ListRecentStickers(ctx, userID, limit)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListFavoriteStickers(ctx context.Context, userID string, limit int) ([]*StickerItem, error) {
	if store, ok := p.base.(StickerStore); ok {
		return store.ListFavoriteStickers(ctx, userID, limit)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) CanUseSticker(ctx context.Context, userID, stickerID string) (bool, error) {
	if store, ok := p.base.(StickerStore); ok {
		return store.CanUseSticker(ctx, userID, stickerID)
	}
	return false, ErrUnsupported
}
