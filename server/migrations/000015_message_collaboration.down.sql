DROP TABLE IF EXISTS im_group_message_pins;
DROP TABLE IF EXISTS im_message_reactions;
DROP TABLE IF EXISTS im_message_edits;
ALTER TABLE im_messages DROP COLUMN IF EXISTS edit_version;
ALTER TABLE im_messages DROP COLUMN IF EXISTS edited_at;
