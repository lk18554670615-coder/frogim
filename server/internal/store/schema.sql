CREATE TABLE IF NOT EXISTS im_state_meta(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),revision bigint NOT NULL DEFAULT 0);
INSERT INTO im_state_meta(singleton,revision) VALUES(true,0) ON CONFLICT(singleton) DO NOTHING;
CREATE TABLE IF NOT EXISTS im_users(id text PRIMARY KEY,phone text UNIQUE NOT NULL,name text NOT NULL,handle text UNIQUE,signature text NOT NULL DEFAULT '',avatar_media_id text,avatar_url text NOT NULL DEFAULT '',banned boolean NOT NULL DEFAULT false,created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL DEFAULT now());
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS handle text;
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS signature text NOT NULL DEFAULT '';
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS avatar_media_id text;
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS password_hash text NOT NULL DEFAULT '';
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS password_updated_at timestamptz;
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS handle_change_count integer NOT NULL DEFAULT 0 CHECK(handle_change_count BETWEEN 0 AND 2);
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS banned_until timestamptz;
CREATE UNIQUE INDEX IF NOT EXISTS im_users_handle_unique_idx ON im_users(lower(handle)) WHERE handle IS NOT NULL;
CREATE TABLE IF NOT EXISTS im_refresh_sessions(id text PRIMARY KEY,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,token_hash bytea NOT NULL,expires_at timestamptz NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),revoked_at timestamptz,replaced_by text);
CREATE INDEX IF NOT EXISTS im_refresh_sessions_user_idx ON im_refresh_sessions(user_id,expires_at DESC);
CREATE INDEX IF NOT EXISTS im_users_search_idx ON im_users(lower(name));
CREATE INDEX IF NOT EXISTS im_users_deleted_idx ON im_users(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TABLE IF NOT EXISTS im_friend_requests(id text PRIMARY KEY,from_user_id text NOT NULL REFERENCES im_users(id),to_user_id text NOT NULL REFERENCES im_users(id),message text NOT NULL DEFAULT '',status text NOT NULL,created_at timestamptz NOT NULL,UNIQUE(from_user_id,to_user_id,status));
ALTER TABLE im_friend_requests ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT 'search';
ALTER TABLE im_friend_requests ADD COLUMN IF NOT EXISTS source_id text NOT NULL DEFAULT '';
ALTER TABLE im_friend_requests ADD COLUMN IF NOT EXISTS expires_at timestamptz;
ALTER TABLE im_friend_requests ADD COLUMN IF NOT EXISTS updated_at timestamptz;
ALTER TABLE im_friend_requests ADD COLUMN IF NOT EXISTS resolved_at timestamptz;
UPDATE im_friend_requests SET expires_at=COALESCE(expires_at,created_at+interval '7 days'),updated_at=COALESCE(updated_at,created_at) WHERE expires_at IS NULL OR updated_at IS NULL;
ALTER TABLE im_friend_requests ALTER COLUMN expires_at SET NOT NULL;
ALTER TABLE im_friend_requests ALTER COLUMN updated_at SET NOT NULL;
ALTER TABLE im_friend_requests DROP CONSTRAINT IF EXISTS im_friend_requests_from_user_id_to_user_id_status_key;
CREATE UNIQUE INDEX IF NOT EXISTS im_friend_requests_pending_idx ON im_friend_requests(from_user_id,to_user_id) WHERE status='pending';
CREATE INDEX IF NOT EXISTS im_friend_requests_expiry_idx ON im_friend_requests(expires_at,id) WHERE status='pending';
CREATE TABLE IF NOT EXISTS im_friendships(user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,friend_user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,remark text NOT NULL DEFAULT '',tags text[] NOT NULL DEFAULT '{}',created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(user_id,friend_user_id));
ALTER TABLE im_friendships ADD COLUMN IF NOT EXISTS remark text NOT NULL DEFAULT '';
ALTER TABLE im_friendships ADD COLUMN IF NOT EXISTS tags text[] NOT NULL DEFAULT '{}';
ALTER TABLE im_friendships ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();
CREATE TABLE IF NOT EXISTS im_blocks(user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,blocked_user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(user_id,blocked_user_id));
CREATE TABLE IF NOT EXISTS im_conversations(id text PRIMARY KEY,kind text NOT NULL,title text NOT NULL DEFAULT '',avatar_url text NOT NULL DEFAULT '',current_seq bigint NOT NULL DEFAULT 0,last_message_seq bigint NOT NULL DEFAULT 0,created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL);
ALTER TABLE im_conversations ADD COLUMN IF NOT EXISTS member_count integer NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS im_direct_index(pair_key text PRIMARY KEY,conversation_id text UNIQUE NOT NULL REFERENCES im_conversations(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS im_members(conversation_id text NOT NULL REFERENCES im_conversations(id) ON DELETE CASCADE,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,role text NOT NULL,last_read_seq bigint NOT NULL DEFAULT 0,muted_until timestamptz,pinned boolean NOT NULL DEFAULT false,notifications_muted boolean NOT NULL DEFAULT false,manual_unread boolean NOT NULL DEFAULT false,hidden_until_seq bigint,joined_at timestamptz NOT NULL,PRIMARY KEY(conversation_id,user_id));
ALTER TABLE im_members ADD COLUMN IF NOT EXISTS pinned boolean NOT NULL DEFAULT false;
ALTER TABLE im_members ADD COLUMN IF NOT EXISTS notifications_muted boolean NOT NULL DEFAULT false;
ALTER TABLE im_members ADD COLUMN IF NOT EXISTS manual_unread boolean NOT NULL DEFAULT false;
ALTER TABLE im_members ADD COLUMN IF NOT EXISTS hidden_until_seq bigint;
ALTER TABLE im_members ADD COLUMN IF NOT EXISTS group_nickname text NOT NULL DEFAULT '';
ALTER TABLE im_members ADD COLUMN IF NOT EXISTS archived boolean NOT NULL DEFAULT false;
ALTER TABLE im_members ADD COLUMN IF NOT EXISTS last_delivered_seq bigint NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS im_members_user_idx ON im_members(user_id,joined_at DESC);
CREATE INDEX IF NOT EXISTS im_members_conversation_joined_idx ON im_members(conversation_id,joined_at,user_id);
UPDATE im_conversations c SET member_count=counts.total FROM (SELECT conversation_id,count(*)::integer AS total FROM im_members GROUP BY conversation_id) counts WHERE c.id=counts.conversation_id AND c.member_count<>counts.total;
CREATE OR REPLACE FUNCTION im_members_increment_count() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  UPDATE im_conversations c SET member_count=c.member_count+counts.total
  FROM (SELECT conversation_id,count(*)::integer AS total FROM inserted_members GROUP BY conversation_id) counts
  WHERE c.id=counts.conversation_id;
  RETURN NULL;
END $$;
CREATE OR REPLACE FUNCTION im_members_decrement_count() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  UPDATE im_conversations c SET member_count=GREATEST(0,c.member_count-counts.total)
  FROM (SELECT conversation_id,count(*)::integer AS total FROM deleted_members GROUP BY conversation_id) counts
  WHERE c.id=counts.conversation_id;
  RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS im_members_count_insert ON im_members;
CREATE TRIGGER im_members_count_insert AFTER INSERT ON im_members REFERENCING NEW TABLE AS inserted_members FOR EACH STATEMENT EXECUTE FUNCTION im_members_increment_count();
DROP TRIGGER IF EXISTS im_members_count_delete ON im_members;
CREATE TRIGGER im_members_count_delete AFTER DELETE ON im_members REFERENCING OLD TABLE AS deleted_members FOR EACH STATEMENT EXECUTE FUNCTION im_members_decrement_count();
CREATE TABLE IF NOT EXISTS im_groups(conversation_id text PRIMARY KEY REFERENCES im_conversations(id) ON DELETE CASCADE,owner_id text NOT NULL REFERENCES im_users(id),announcement text NOT NULL DEFAULT '',announcement_version bigint NOT NULL DEFAULT 0,join_policy text NOT NULL DEFAULT 'invite',allow_member_add_friend boolean NOT NULL DEFAULT true,all_muted_until timestamptz,qr_token text UNIQUE,qr_expires_at timestamptz,dissolved_at timestamptz,updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS im_group_announcement_reads(conversation_id text NOT NULL REFERENCES im_groups(conversation_id) ON DELETE CASCADE,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,announcement_version bigint NOT NULL,read_at timestamptz NOT NULL,PRIMARY KEY(conversation_id,user_id,announcement_version));
CREATE TABLE IF NOT EXISTS im_group_invites(id text PRIMARY KEY,conversation_id text NOT NULL REFERENCES im_groups(conversation_id) ON DELETE CASCADE,inviter_id text NOT NULL REFERENCES im_users(id),invitee_id text NOT NULL REFERENCES im_users(id),source text NOT NULL,status text NOT NULL,created_at timestamptz NOT NULL,expires_at timestamptz NOT NULL,updated_at timestamptz NOT NULL,resolved_at timestamptz);
CREATE UNIQUE INDEX IF NOT EXISTS im_group_invites_pending_idx ON im_group_invites(conversation_id,invitee_id) WHERE status='pending';
CREATE INDEX IF NOT EXISTS im_group_invites_expiry_idx ON im_group_invites(expires_at,id) WHERE status='pending';
CREATE TABLE IF NOT EXISTS im_messages(id text PRIMARY KEY,conversation_id text NOT NULL REFERENCES im_conversations(id) ON DELETE CASCADE,sender_id text NOT NULL REFERENCES im_users(id),client_msg_id text NOT NULL,conversation_seq bigint NOT NULL,message_type text NOT NULL,body jsonb NOT NULL,reply_to_id text,recalled_at timestamptz,created_at timestamptz NOT NULL,UNIQUE(sender_id,client_msg_id),UNIQUE(conversation_id,conversation_seq));
ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS edited_at timestamptz;
ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS edit_version integer NOT NULL DEFAULT 0 CHECK(edit_version>=0);
ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS expires_at timestamptz;
ALTER TABLE im_messages ADD COLUMN IF NOT EXISTS expired_at timestamptz;
CREATE INDEX IF NOT EXISTS im_messages_expiry_idx ON im_messages(expires_at,id) WHERE expires_at IS NOT NULL AND expired_at IS NULL AND recalled_at IS NULL;
CREATE INDEX IF NOT EXISTS im_messages_media_id_idx ON im_messages((body->>'mediaId')) WHERE message_type IN ('image','audio','video','file');
CREATE TABLE IF NOT EXISTS im_message_edits(message_id text NOT NULL REFERENCES im_messages(id) ON DELETE CASCADE,version integer NOT NULL CHECK(version>=0),edit_id text,editor_id text NOT NULL REFERENCES im_users(id),body jsonb NOT NULL,created_at timestamptz NOT NULL,PRIMARY KEY(message_id,version));
CREATE UNIQUE INDEX IF NOT EXISTS im_message_edits_idempotency_idx ON im_message_edits(message_id,edit_id) WHERE edit_id IS NOT NULL;
CREATE TABLE IF NOT EXISTS im_message_edit_requests(message_id text NOT NULL REFERENCES im_messages(id) ON DELETE CASCADE,edit_id text NOT NULL,body jsonb NOT NULL,version integer NOT NULL CHECK(version>=0),created_at timestamptz NOT NULL,PRIMARY KEY(message_id,edit_id));
CREATE TABLE IF NOT EXISTS im_message_reactions(message_id text NOT NULL REFERENCES im_messages(id) ON DELETE CASCADE,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,emoji text NOT NULL,created_at timestamptz NOT NULL,PRIMARY KEY(message_id,user_id,emoji));
CREATE INDEX IF NOT EXISTS im_message_reactions_aggregate_idx ON im_message_reactions(message_id,emoji);
CREATE TABLE IF NOT EXISTS im_group_message_pins(conversation_id text NOT NULL REFERENCES im_groups(conversation_id) ON DELETE CASCADE,message_id text NOT NULL REFERENCES im_messages(id) ON DELETE CASCADE,pinned_by text NOT NULL REFERENCES im_users(id),pinned_at timestamptz NOT NULL,PRIMARY KEY(conversation_id,message_id));
CREATE INDEX IF NOT EXISTS im_group_message_pins_list_idx ON im_group_message_pins(conversation_id,pinned_at DESC,message_id);
CREATE INDEX IF NOT EXISTS im_messages_history_idx ON im_messages(conversation_id,conversation_seq DESC);
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
CREATE INDEX IF NOT EXISTS im_messages_text_search_trgm_idx ON im_messages USING gin (lower(body->>'text') gin_trgm_ops) WHERE message_type='text' AND recalled_at IS NULL;
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
CREATE INDEX IF NOT EXISTS im_scheduled_messages_due_idx ON im_scheduled_messages(available_at,scheduled_at,id) WHERE status IN ('pending','processing');
CREATE TABLE IF NOT EXISTS im_user_cursors(user_id text PRIMARY KEY REFERENCES im_users(id) ON DELETE CASCADE,current_seq bigint NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS im_sync_events(user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,user_sync_seq bigint NOT NULL,event_type text NOT NULL,payload jsonb NOT NULL,created_at timestamptz NOT NULL,PRIMARY KEY(user_id,user_sync_seq));
CREATE INDEX IF NOT EXISTS im_sync_events_created_idx ON im_sync_events(created_at);
CREATE TABLE IF NOT EXISTS im_reports(id text PRIMARY KEY,reporter_id text NOT NULL REFERENCES im_users(id),target_type text NOT NULL,target_id text NOT NULL,reason text NOT NULL,details text NOT NULL DEFAULT '',status text NOT NULL,resolution text NOT NULL DEFAULT '',created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL);
CREATE INDEX IF NOT EXISTS im_reports_queue_idx ON im_reports(status,created_at);
CREATE TABLE IF NOT EXISTS im_audits(id text PRIMARY KEY,actor_id text NOT NULL,action text NOT NULL,target_type text NOT NULL,target_id text NOT NULL,metadata jsonb NOT NULL,created_at timestamptz NOT NULL);
ALTER TABLE im_audits ADD COLUMN IF NOT EXISTS result text NOT NULL DEFAULT 'success';
ALTER TABLE im_audits ADD COLUMN IF NOT EXISTS ip text NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS im_sensitive_words(id text PRIMARY KEY,value text NOT NULL);
CREATE TABLE IF NOT EXISTS im_settings(key text PRIMARY KEY,value jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS im_devices(id text PRIMARY KEY,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,platform text NOT NULL,provider text NOT NULL,push_token text NOT NULL,notifications_enabled boolean NOT NULL DEFAULT true,preview_enabled boolean NOT NULL DEFAULT true,sound_enabled boolean NOT NULL DEFAULT true,vibration_enabled boolean NOT NULL DEFAULT true,updated_at timestamptz NOT NULL DEFAULT now(),UNIQUE(provider,push_token));
-- 兼容早期已存在的 im_devices 表；CREATE TABLE IF NOT EXISTS 不会补齐新增列。
ALTER TABLE im_devices ADD COLUMN IF NOT EXISTS notifications_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE im_devices ADD COLUMN IF NOT EXISTS preview_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE im_devices ADD COLUMN IF NOT EXISTS sound_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE im_devices ADD COLUMN IF NOT EXISTS vibration_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE im_devices DROP CONSTRAINT IF EXISTS im_devices_provider_push_token_key;
CREATE UNIQUE INDEX IF NOT EXISTS im_devices_active_provider_push_token_idx ON im_devices(provider,push_token) WHERE notifications_enabled AND push_token<>'';
CREATE INDEX IF NOT EXISTS im_devices_user_idx ON im_devices(user_id);
CREATE TABLE IF NOT EXISTS im_push_outbox(id bigserial PRIMARY KEY,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,event_type text NOT NULL,payload jsonb NOT NULL,status text NOT NULL DEFAULT 'pending',attempts int NOT NULL DEFAULT 0,available_at timestamptz NOT NULL DEFAULT now(),created_at timestamptz NOT NULL DEFAULT now(),sent_at timestamptz);
ALTER TABLE im_push_outbox ADD COLUMN IF NOT EXISTS locked_at timestamptz;
ALTER TABLE im_push_outbox ADD COLUMN IF NOT EXISTS last_error text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS im_push_outbox_pending_idx ON im_push_outbox(status,available_at) WHERE status='pending';
CREATE INDEX IF NOT EXISTS im_push_outbox_retention_idx ON im_push_outbox(COALESCE(sent_at,available_at),id) WHERE status IN ('sent','failed');
CREATE TABLE IF NOT EXISTS im_event_outbox(id bigserial PRIMARY KEY,event_type text NOT NULL,aggregate_id text NOT NULL,payload jsonb NOT NULL,status text NOT NULL DEFAULT 'pending',attempts integer NOT NULL DEFAULT 0,available_at timestamptz NOT NULL DEFAULT now(),locked_at timestamptz,last_error text,created_at timestamptz NOT NULL DEFAULT now(),published_at timestamptz);
ALTER TABLE im_event_outbox ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0;
ALTER TABLE im_event_outbox ADD COLUMN IF NOT EXISTS available_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE im_event_outbox ADD COLUMN IF NOT EXISTS locked_at timestamptz;
ALTER TABLE im_event_outbox ADD COLUMN IF NOT EXISTS last_error text;
DROP INDEX IF EXISTS im_event_outbox_pending_idx;
CREATE INDEX im_event_outbox_pending_idx ON im_event_outbox(status,available_at,id) WHERE status IN ('pending','processing');
CREATE INDEX IF NOT EXISTS im_event_outbox_retention_idx ON im_event_outbox(published_at,id) WHERE status='published';
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
CREATE INDEX IF NOT EXISTS im_message_fanout_pending_idx ON im_message_fanout(status,id) WHERE status IN ('pending','processing');
CREATE INDEX IF NOT EXISTS im_message_fanout_retention_idx ON im_message_fanout(completed_at,id) WHERE status='completed';
CREATE TABLE IF NOT EXISTS im_media(id text PRIMARY KEY,owner_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,object_key text UNIQUE NOT NULL,mime text NOT NULL,size bigint NOT NULL,status text NOT NULL,checksum text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),completed_at timestamptz);
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_status text NOT NULL DEFAULT '';
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_locked_at timestamptz;
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_attempts integer NOT NULL DEFAULT 0;
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_last_error text NOT NULL DEFAULT '';
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_updated_at timestamptz;
CREATE INDEX IF NOT EXISTS im_media_owner_idx ON im_media(owner_id,created_at DESC);
CREATE INDEX IF NOT EXISTS im_media_cleanup_idx ON im_media(created_at,id) WHERE cleanup_status IN ('','pending','processing');
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
CREATE TABLE IF NOT EXISTS im_announcements(
 id text PRIMARY KEY,title text NOT NULL,content text NOT NULL,status text NOT NULL,
 pinned boolean NOT NULL DEFAULT false,target_type text NOT NULL,target_user_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
 scheduled_at timestamptz,published_at timestamptz,withdrawn_at timestamptz,push_on_publish boolean NOT NULL DEFAULT false,
 created_by text NOT NULL,created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS im_announcements_status_idx ON im_announcements(status,scheduled_at,pinned DESC,created_at DESC);
CREATE TABLE IF NOT EXISTS im_announcement_reads(announcement_id text NOT NULL REFERENCES im_announcements(id) ON DELETE CASCADE,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,read_at timestamptz NOT NULL,PRIMARY KEY(announcement_id,user_id));
CREATE TABLE IF NOT EXISTS im_favorites(user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,message_id text NOT NULL REFERENCES im_messages(id) ON DELETE CASCADE,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(user_id,message_id));
CREATE INDEX IF NOT EXISTS im_favorites_user_idx ON im_favorites(user_id,created_at DESC);
CREATE TABLE IF NOT EXISTS im_feedback(id text PRIMARY KEY,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,category text NOT NULL DEFAULT 'other',content text NOT NULL,contact text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS im_feedback_created_idx ON im_feedback(created_at DESC);
