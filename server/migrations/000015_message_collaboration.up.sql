ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS edited_at timestamptz;
ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS edit_version integer NOT NULL DEFAULT 0;
ALTER TABLE im_messages DROP CONSTRAINT IF EXISTS im_messages_edit_version_check;
ALTER TABLE im_messages ADD CONSTRAINT im_messages_edit_version_check CHECK(edit_version>=0);

CREATE TABLE IF NOT EXISTS im_message_edits(
  message_id text NOT NULL REFERENCES im_messages(id) ON DELETE CASCADE,
  version integer NOT NULL CHECK(version>=0),
  edit_id text,
  editor_id text NOT NULL REFERENCES im_users(id),
  body jsonb NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY(message_id,version)
);
CREATE UNIQUE INDEX IF NOT EXISTS im_message_edits_idempotency_idx
ON im_message_edits(message_id,edit_id) WHERE edit_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS im_message_reactions(
  message_id text NOT NULL REFERENCES im_messages(id) ON DELETE CASCADE,
  user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
  emoji text NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY(message_id,user_id,emoji)
);
CREATE INDEX IF NOT EXISTS im_message_reactions_aggregate_idx
ON im_message_reactions(message_id,emoji);

CREATE TABLE IF NOT EXISTS im_group_message_pins(
  conversation_id text NOT NULL REFERENCES im_groups(conversation_id) ON DELETE CASCADE,
  message_id text NOT NULL REFERENCES im_messages(id) ON DELETE CASCADE,
  pinned_by text NOT NULL REFERENCES im_users(id),
  pinned_at timestamptz NOT NULL,
  PRIMARY KEY(conversation_id,message_id)
);
CREATE INDEX IF NOT EXISTS im_group_message_pins_list_idx
ON im_group_message_pins(conversation_id,pinned_at DESC,message_id);
