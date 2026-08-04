DROP TABLE IF EXISTS im_feedback;
DROP TABLE IF EXISTS im_favorites;
DROP INDEX IF EXISTS im_users_handle_unique_idx;
ALTER TABLE im_users DROP COLUMN IF EXISTS avatar_media_id;
ALTER TABLE im_users DROP COLUMN IF EXISTS signature;
ALTER TABLE im_users DROP COLUMN IF EXISTS handle;
