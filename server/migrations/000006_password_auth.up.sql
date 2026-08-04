ALTER TABLE im_users ADD COLUMN IF NOT EXISTS password_hash text NOT NULL DEFAULT '';
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS password_updated_at timestamptz;
