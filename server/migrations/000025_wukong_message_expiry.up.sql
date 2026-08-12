ALTER TABLE im_wukong_message_index
  ADD COLUMN IF NOT EXISTS media_id text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS expires_at timestamptz,
  ADD COLUMN IF NOT EXISTS expired_at timestamptz;

UPDATE im_wukong_message_index
SET expires_at=message_timestamp + expire_seconds * interval '1 second'
WHERE expire_seconds>0 AND expires_at IS NULL;

CREATE INDEX IF NOT EXISTS im_wukong_message_index_expiry_idx
  ON im_wukong_message_index(expires_at,message_id)
  WHERE expires_at IS NOT NULL AND expired_at IS NULL;
