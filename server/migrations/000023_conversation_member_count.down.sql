DROP TRIGGER IF EXISTS im_members_count_delete ON im_members;
DROP TRIGGER IF EXISTS im_members_count_insert ON im_members;
DROP FUNCTION IF EXISTS im_members_decrement_count();
DROP FUNCTION IF EXISTS im_members_increment_count();
DROP INDEX IF EXISTS im_members_conversation_joined_idx;
ALTER TABLE im_conversations DROP COLUMN IF EXISTS member_count;
