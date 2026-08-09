ALTER TABLE im_conversations ADD COLUMN IF NOT EXISTS member_count integer NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS im_members_conversation_joined_idx ON im_members(conversation_id,joined_at,user_id);

UPDATE im_conversations c
SET member_count=counts.total
FROM (
  SELECT conversation_id,count(*)::integer AS total
  FROM im_members
  GROUP BY conversation_id
) counts
WHERE c.id=counts.conversation_id AND c.member_count<>counts.total;

CREATE OR REPLACE FUNCTION im_members_increment_count() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  UPDATE im_conversations c SET member_count=c.member_count+counts.total
  FROM (SELECT conversation_id,count(*)::integer AS total FROM inserted_members GROUP BY conversation_id) counts
  WHERE c.id=counts.conversation_id;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION im_members_decrement_count() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  UPDATE im_conversations c SET member_count=GREATEST(0,c.member_count-counts.total)
  FROM (SELECT conversation_id,count(*)::integer AS total FROM deleted_members GROUP BY conversation_id) counts
  WHERE c.id=counts.conversation_id;
  RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS im_members_count_insert ON im_members;
CREATE TRIGGER im_members_count_insert
AFTER INSERT ON im_members
REFERENCING NEW TABLE AS inserted_members
FOR EACH STATEMENT EXECUTE FUNCTION im_members_increment_count();

DROP TRIGGER IF EXISTS im_members_count_delete ON im_members;
CREATE TRIGGER im_members_count_delete
AFTER DELETE ON im_members
REFERENCING OLD TABLE AS deleted_members
FOR EACH STATEMENT EXECUTE FUNCTION im_members_decrement_count();
