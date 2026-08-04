CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), phone varchar(32) UNIQUE NOT NULL,
    display_name varchar(80) NOT NULL, avatar_url text, status varchar(20) NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (status IN ('active','banned','deleted'))
);
CREATE INDEX users_display_name_idx ON users USING gin (to_tsvector('simple', display_name));
CREATE TABLE devices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform varchar(20) NOT NULL, push_token text, last_seen_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(user_id,id)
);
CREATE TABLE friend_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), from_user_id uuid NOT NULL REFERENCES users(id), to_user_id uuid NOT NULL REFERENCES users(id),
    message varchar(300), status varchar(20) NOT NULL DEFAULT 'pending', created_at timestamptz NOT NULL DEFAULT now(), resolved_at timestamptz,
    CHECK (from_user_id <> to_user_id), CHECK (status IN ('pending','accepted','rejected','cancelled'))
);
CREATE UNIQUE INDEX friend_requests_one_pending_idx ON friend_requests(from_user_id,to_user_id) WHERE status='pending';
CREATE TABLE friendships (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, friend_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(user_id,friend_user_id), CHECK(user_id<>friend_user_id)
);
CREATE TABLE blocks (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, blocked_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(user_id,blocked_user_id), CHECK(user_id<>blocked_user_id)
);
CREATE TABLE conversations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), kind varchar(20) NOT NULL, title varchar(80), avatar_url text,
    current_seq bigint NOT NULL DEFAULT 0, last_message_seq bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CHECK(kind IN ('direct','group'))
);
CREATE TABLE direct_conversations (
    conversation_id uuid PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    low_user_id uuid NOT NULL REFERENCES users(id), high_user_id uuid NOT NULL REFERENCES users(id),
    UNIQUE(low_user_id,high_user_id), CHECK(low_user_id<>high_user_id)
);
CREATE TABLE groups (
    conversation_id uuid PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    owner_id uuid NOT NULL REFERENCES users(id), announcement text, join_policy varchar(20) NOT NULL DEFAULT 'invite',
    max_members int NOT NULL DEFAULT 500, CHECK(max_members BETWEEN 2 AND 100000)
);
CREATE TABLE conversation_members (
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role varchar(20) NOT NULL DEFAULT 'member', last_read_seq bigint NOT NULL DEFAULT 0, muted_until timestamptz,
    joined_at timestamptz NOT NULL DEFAULT now(), left_at timestamptz, PRIMARY KEY(conversation_id,user_id),
    CHECK(role IN ('owner','admin','member'))
);
CREATE INDEX conversation_members_user_idx ON conversation_members(user_id,joined_at DESC) WHERE left_at IS NULL;
CREATE TABLE messages (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    sender_id uuid NOT NULL REFERENCES users(id), client_msg_id varchar(100) NOT NULL, conversation_seq bigint NOT NULL,
    message_type varchar(30) NOT NULL, body jsonb NOT NULL DEFAULT '{}', reply_to_id uuid REFERENCES messages(id), recalled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(sender_id,client_msg_id), UNIQUE(conversation_id,conversation_seq)
);
CREATE INDEX messages_history_idx ON messages(conversation_id,conversation_seq DESC);
CREATE TABLE message_receipts (
    message_id uuid NOT NULL REFERENCES messages(id) ON DELETE CASCADE, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    delivered_at timestamptz, read_at timestamptz, PRIMARY KEY(message_id,user_id)
);
CREATE TABLE user_sync_events (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, user_sync_seq bigint NOT NULL,
    event_type varchar(50) NOT NULL, payload jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(user_id,user_sync_seq)
);
CREATE INDEX user_sync_events_created_idx ON user_sync_events(created_at);
CREATE TABLE reports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), reporter_id uuid NOT NULL REFERENCES users(id), target_type varchar(30) NOT NULL,
    target_id varchar(100) NOT NULL, reason varchar(80) NOT NULL, details text, status varchar(20) NOT NULL DEFAULT 'pending',
    resolution text, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK(status IN ('pending','resolved','rejected'))
);
CREATE INDEX reports_queue_idx ON reports(status,created_at);
CREATE TABLE audit_logs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), actor_id varchar(100) NOT NULL, action varchar(80) NOT NULL,
    target_type varchar(30) NOT NULL, target_id varchar(100) NOT NULL, metadata jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_created_idx ON audit_logs(created_at DESC);
