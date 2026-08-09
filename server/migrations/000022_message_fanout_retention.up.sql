CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

CREATE INDEX IF NOT EXISTS im_push_outbox_retention_idx
  ON im_push_outbox(COALESCE(sent_at,available_at),id)
  WHERE status IN ('sent','failed');

CREATE INDEX IF NOT EXISTS im_event_outbox_retention_idx
  ON im_event_outbox(published_at,id)
  WHERE status='published';

CREATE TABLE IF NOT EXISTS im_message_fanout(
  id bigserial PRIMARY KEY,
  conversation_id text NOT NULL REFERENCES im_conversations(id) ON DELETE CASCADE,
  sender_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
  event_payload jsonb NOT NULL,
  push_payload jsonb NOT NULL,
  mention_all boolean NOT NULL DEFAULT false,
  mentions text[] NOT NULL DEFAULT '{}',
  last_user_id text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','completed')),
  attempts integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL,
  locked_at timestamptz,
  completed_at timestamptz
);
CREATE INDEX IF NOT EXISTS im_message_fanout_pending_idx
  ON im_message_fanout(status,id) WHERE status IN ('pending','processing');
CREATE INDEX IF NOT EXISTS im_message_fanout_retention_idx
  ON im_message_fanout(completed_at,id) WHERE status='completed';
