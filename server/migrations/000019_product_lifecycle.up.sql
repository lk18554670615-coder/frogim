ALTER TABLE im_members ADD COLUMN IF NOT EXISTS archived boolean NOT NULL DEFAULT false;
ALTER TABLE im_members ADD COLUMN IF NOT EXISTS last_delivered_seq bigint NOT NULL DEFAULT 0;

ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS expires_at timestamptz;
ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS expired_at timestamptz;
CREATE INDEX IF NOT EXISTS im_messages_expiry_idx ON im_messages(expires_at,id)
  WHERE expires_at IS NOT NULL AND expired_at IS NULL AND recalled_at IS NULL;

CREATE TABLE IF NOT EXISTS im_scheduled_messages(
 id text PRIMARY KEY,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 conversation_id text NOT NULL REFERENCES im_conversations(id) ON DELETE CASCADE,
 client_msg_id text NOT NULL,message_type text NOT NULL,body jsonb NOT NULL,reply_to_id text,
 expires_in_seconds bigint NOT NULL DEFAULT 0,scheduled_at timestamptz NOT NULL,
 status text NOT NULL CHECK(status IN ('pending','processing','sent','cancelled','failed')),
 attempts integer NOT NULL DEFAULT 0,available_at timestamptz NOT NULL,locked_at timestamptz,
 last_error text NOT NULL DEFAULT '',sent_message_id text REFERENCES im_messages(id),
 created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL,
 UNIQUE(user_id,client_msg_id)
);
CREATE INDEX IF NOT EXISTS im_scheduled_messages_due_idx ON im_scheduled_messages(available_at,scheduled_at,id)
  WHERE status IN ('pending','processing');
