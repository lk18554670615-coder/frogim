-- PostgreSQL is authoritative for WuKongIM system-account membership. The
-- runtime cache is refreshed through the persistent WuKong outbox.
CREATE TABLE IF NOT EXISTS im_wukong_system_users(
  user_id text PRIMARY KEY REFERENCES im_users(id) ON DELETE CASCADE,
  enabled boolean NOT NULL,
  updated_by text NOT NULL,
  reason text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS im_wukong_system_users_enabled_idx
  ON im_wukong_system_users(enabled,user_id);
