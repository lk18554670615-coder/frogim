DROP INDEX IF EXISTS im_users_deleted_idx;
ALTER TABLE im_users DROP COLUMN IF EXISTS deleted_at;
