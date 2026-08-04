ALTER TABLE im_users ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
CREATE INDEX IF NOT EXISTS im_users_deleted_idx
ON im_users(deleted_at) WHERE deleted_at IS NOT NULL;
