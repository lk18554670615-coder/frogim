DROP INDEX IF EXISTS im_wukong_message_index_expiry_idx;
ALTER TABLE im_wukong_message_index
  DROP COLUMN IF EXISTS expired_at,
  DROP COLUMN IF EXISTS expires_at,
  DROP COLUMN IF EXISTS media_id;
