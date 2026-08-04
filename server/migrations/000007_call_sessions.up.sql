CREATE TABLE IF NOT EXISTS im_call_sessions(
  id text PRIMARY KEY,
  conversation_id text NOT NULL REFERENCES im_conversations(id) ON DELETE CASCADE,
  caller_id text NOT NULL REFERENCES im_users(id),
  callee_id text NOT NULL REFERENCES im_users(id),
  media_type text NOT NULL CHECK(media_type IN ('audio','video')),
  status text NOT NULL CHECK(status IN ('invited','accepted','rejected','cancelled','ended','missed')),
  end_reason text NOT NULL DEFAULT '',
  ended_by text NOT NULL DEFAULT '',
  invited_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  accepted_at timestamptz,
  ended_at timestamptz,
  updated_at timestamptz NOT NULL,
  CHECK(caller_id<>callee_id)
);
CREATE INDEX IF NOT EXISTS im_call_sessions_admin_idx ON im_call_sessions(invited_at DESC,id);
CREATE INDEX IF NOT EXISTS im_call_sessions_expiry_idx ON im_call_sessions(expires_at,id) WHERE status='invited';
CREATE UNIQUE INDEX IF NOT EXISTS im_call_sessions_one_active_idx ON im_call_sessions(conversation_id) WHERE status IN ('invited','accepted');
