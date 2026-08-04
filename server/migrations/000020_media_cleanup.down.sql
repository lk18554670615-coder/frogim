DROP INDEX IF EXISTS im_media_cleanup_idx;
ALTER TABLE im_media DROP COLUMN IF EXISTS cleanup_updated_at;
ALTER TABLE im_media DROP COLUMN IF EXISTS cleanup_last_error;
ALTER TABLE im_media DROP COLUMN IF EXISTS cleanup_attempts;
ALTER TABLE im_media DROP COLUMN IF EXISTS cleanup_locked_at;
ALTER TABLE im_media DROP COLUMN IF EXISTS cleanup_status;
