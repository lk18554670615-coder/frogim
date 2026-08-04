ALTER TABLE im_members ADD COLUMN IF NOT EXISTS pinned boolean NOT NULL DEFAULT false;
ALTER TABLE im_members ADD COLUMN IF NOT EXISTS notifications_muted boolean NOT NULL DEFAULT false;
ALTER TABLE im_members ADD COLUMN IF NOT EXISTS manual_unread boolean NOT NULL DEFAULT false;
ALTER TABLE im_members ADD COLUMN IF NOT EXISTS hidden_until_seq bigint;

CREATE INDEX IF NOT EXISTS im_members_visible_order_idx
  ON im_members(user_id, pinned DESC, conversation_id)
  WHERE hidden_until_seq IS NULL;
