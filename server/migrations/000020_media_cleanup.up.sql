ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_status text NOT NULL DEFAULT '';
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_locked_at timestamptz;
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_attempts integer NOT NULL DEFAULT 0;
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_last_error text NOT NULL DEFAULT '';
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_updated_at timestamptz;
CREATE INDEX IF NOT EXISTS im_media_cleanup_idx ON im_media(created_at,id)
  WHERE cleanup_status IN ('','pending','processing');
