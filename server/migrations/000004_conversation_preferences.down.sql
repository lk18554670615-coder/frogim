DROP INDEX IF EXISTS im_members_visible_order_idx;
ALTER TABLE im_members DROP COLUMN IF EXISTS hidden_until_seq;
ALTER TABLE im_members DROP COLUMN IF EXISTS manual_unread;
ALTER TABLE im_members DROP COLUMN IF EXISTS notifications_muted;
ALTER TABLE im_members DROP COLUMN IF EXISTS pinned;
