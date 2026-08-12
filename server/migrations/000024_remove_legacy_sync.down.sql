CREATE TABLE IF NOT EXISTS im_user_cursors(
 user_id text PRIMARY KEY REFERENCES im_users(id) ON DELETE CASCADE,
 current_seq bigint NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS im_sync_events(
 user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 user_sync_seq bigint NOT NULL,
 event_type text NOT NULL,
 payload jsonb NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(user_id,user_sync_seq)
);
CREATE INDEX IF NOT EXISTS im_sync_events_created_idx ON im_sync_events(created_at);
