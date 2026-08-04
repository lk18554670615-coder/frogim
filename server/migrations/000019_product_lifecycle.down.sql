DROP TABLE IF EXISTS im_scheduled_messages;
DROP INDEX IF EXISTS im_messages_expiry_idx;
ALTER TABLE im_messages DROP COLUMN IF EXISTS expired_at;
ALTER TABLE im_messages DROP COLUMN IF EXISTS expires_at;
ALTER TABLE im_members DROP COLUMN IF EXISTS last_delivered_seq;
ALTER TABLE im_members DROP COLUMN IF EXISTS archived;
