CREATE TABLE IF NOT EXISTS im_announcements(
  id text PRIMARY KEY,
  title text NOT NULL,
  content text NOT NULL,
  status text NOT NULL CHECK(status IN ('draft','scheduled','published','withdrawn')),
  pinned boolean NOT NULL DEFAULT false,
  target_type text NOT NULL CHECK(target_type IN ('all','users')),
  target_user_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  scheduled_at timestamptz,
  published_at timestamptz,
  withdrawn_at timestamptz,
  push_on_publish boolean NOT NULL DEFAULT false,
  created_by text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS im_announcements_status_idx ON im_announcements(status,scheduled_at,pinned DESC,created_at DESC);
CREATE TABLE IF NOT EXISTS im_announcement_reads(
  announcement_id text NOT NULL REFERENCES im_announcements(id) ON DELETE CASCADE,
  user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
  read_at timestamptz NOT NULL,
  PRIMARY KEY(announcement_id,user_id)
);
