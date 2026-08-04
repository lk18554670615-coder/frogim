ALTER TABLE im_users ADD COLUMN IF NOT EXISTS handle text;
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS signature text NOT NULL DEFAULT '';
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS avatar_media_id text;
UPDATE im_users SET handle='user_' || lower(regexp_replace(id, '[^a-zA-Z0-9_]', '_', 'g')) WHERE handle IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS im_users_handle_unique_idx ON im_users(lower(handle)) WHERE handle IS NOT NULL;

CREATE TABLE IF NOT EXISTS im_favorites(
  user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
  message_id text NOT NULL REFERENCES im_messages(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(user_id,message_id)
);
CREATE INDEX IF NOT EXISTS im_favorites_user_idx ON im_favorites(user_id,created_at DESC);

CREATE TABLE IF NOT EXISTS im_feedback(
  id text PRIMARY KEY,
  user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
  category text NOT NULL DEFAULT 'other',
  content text NOT NULL,
  contact text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS im_feedback_created_idx ON im_feedback(created_at DESC);
