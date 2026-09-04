package store

import (
	"context"
	"strings"
)

// A ready video's cover is immutable, including when /complete is retried.
func (p *Postgres) CompleteMediaWithCover(ctx context.Context, id, uid string, size int64, sum, cover string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status, owner, mime, existing string
	err = tx.QueryRow(ctx, `SELECT status,owner_id,mime,COALESCE(cover_media_id,'') FROM im_media WHERE id=$1 FOR UPDATE`, id).Scan(&status, &owner, &mime, &existing)
	if err != nil {
		return err
	}
	if owner != uid {
		return ErrForbidden
	}
	if status == "ready" {
		if existing != cover {
			return ErrConflict
		}
		return tx.Commit(ctx)
	}
	if status != "pending" {
		return ErrForbidden
	}
	if cover != "" {
		if !strings.HasPrefix(mime, "video/") || cover == id {
			return ErrForbidden
		}
		var coverOwner, coverMIME, coverStatus, cleanup string
		err = tx.QueryRow(ctx, `SELECT owner_id,mime,status,cleanup_status FROM im_media WHERE id=$1 FOR UPDATE`, cover).Scan(&coverOwner, &coverMIME, &coverStatus, &cleanup)
		if err != nil {
			return err
		}
		if coverOwner != uid || coverMIME != "image/jpeg" || coverStatus != "ready" || cleanup == "processing" {
			return ErrForbidden
		}
	}
	_, err = tx.Exec(ctx, `UPDATE im_media SET status='ready',size=$3,checksum=$4,cover_media_id=NULLIF($5,''),completed_at=now() WHERE id=$1 AND owner_id=$2`, id, uid, size, sum, cover)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
