-- 75: a signal request may outlive a broken API/database connection. Keep a
-- durable barrier until LiveKit has conclusively completed its join handshake.
-- No token, SDP or media is stored. Ambiguous attempts must not expire silently.
CREATE TABLE IF NOT EXISTS im_tenant_media_attempts (
 id text PRIMARY KEY,
 local_user_id text NOT NULL REFERENCES im_users(id),
 assignment_version bigint NOT NULL,
 auth_version bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS im_tenant_media_attempts_user ON im_tenant_media_attempts(local_user_id);
