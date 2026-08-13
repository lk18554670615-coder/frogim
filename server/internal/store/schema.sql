CREATE TABLE IF NOT EXISTS im_state_meta(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),revision bigint NOT NULL DEFAULT 0);
INSERT INTO im_state_meta(singleton,revision) VALUES(true,0) ON CONFLICT(singleton) DO NOTHING;
CREATE TABLE IF NOT EXISTS im_users(id text PRIMARY KEY,phone text UNIQUE NOT NULL,name text NOT NULL,handle text UNIQUE,signature text NOT NULL DEFAULT '',avatar_media_id text,avatar_url text NOT NULL DEFAULT '',banned boolean NOT NULL DEFAULT false,created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL DEFAULT now());
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS handle text;
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS signature text NOT NULL DEFAULT '';
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS avatar_media_id text;
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS avatar_url text NOT NULL DEFAULT '';
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();
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
ALTER TABLE im_members ADD COLUMN IF NOT EXISTS expires_at timestamptz;
CREATE INDEX IF NOT EXISTS im_members_user_idx ON im_members(user_id,joined_at DESC);
CREATE INDEX IF NOT EXISTS im_members_conversation_joined_idx ON im_members(conversation_id,joined_at,user_id);
CREATE INDEX IF NOT EXISTS im_members_expiry_idx ON im_members(expires_at,conversation_id,user_id) WHERE expires_at IS NOT NULL;
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

-- WuKongIM business channels share the existing conversation/member identity
-- model so message indexes, read state, reminders and media authorization keep
-- one authoritative membership source. Customer-service sessions (types 3/10)
-- use the same table later, but are created only by the support workflow because
-- their wire channel IDs have special visitor semantics in the pinned server.
CREATE TABLE IF NOT EXISTS im_business_channels(
 conversation_id text PRIMARY KEY REFERENCES im_conversations(id) ON DELETE CASCADE,
 channel_type smallint NOT NULL CHECK(channel_type IN (3,4,5,6,9,10)),
 category text NOT NULL CHECK(category IN ('customer_service','community','community_topic','info','live','visitor')),
 owner_id text NOT NULL REFERENCES im_users(id),
 parent_id text REFERENCES im_business_channels(conversation_id) ON DELETE CASCADE,
 description text NOT NULL DEFAULT '',
 visibility text NOT NULL DEFAULT 'public' CHECK(visibility IN ('public','private')),
 join_policy text NOT NULL DEFAULT 'open' CHECK(join_policy IN ('open','approval','invite','closed')),
 posting_policy text NOT NULL DEFAULT 'members' CHECK(posting_policy IN ('members','operators')),
 slow_mode_seconds integer NOT NULL DEFAULT 0 CHECK(slow_mode_seconds BETWEEN 0 AND 86400),
 ban boolean NOT NULL DEFAULT false,
 disband boolean NOT NULL DEFAULT false,
 send_ban boolean NOT NULL DEFAULT false,
 allow_stranger boolean NOT NULL DEFAULT false,
 metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(conversation_id,channel_type),
 CHECK((channel_type=3 AND category='customer_service') OR
       (channel_type=4 AND category='community') OR
       (channel_type=5 AND category='community_topic') OR
       (channel_type=6 AND category='info') OR
       (channel_type=9 AND category='live') OR
       (channel_type=10 AND category='visitor')),
 CHECK((channel_type=5 AND parent_id IS NOT NULL) OR (channel_type<>5 AND parent_id IS NULL))
);
CREATE INDEX IF NOT EXISTS im_business_channels_list_idx ON im_business_channels(category,visibility,updated_at DESC,conversation_id);
CREATE INDEX IF NOT EXISTS im_business_channels_parent_idx ON im_business_channels(parent_id,updated_at DESC) WHERE parent_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS im_business_channel_access(
 conversation_id text NOT NULL REFERENCES im_business_channels(conversation_id) ON DELETE CASCADE,
 user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 access_type text NOT NULL CHECK(access_type IN ('allow','deny')),
 reason text NOT NULL DEFAULT '',
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(conversation_id,user_id,access_type)
);
CREATE INDEX IF NOT EXISTS im_business_channel_access_sync_idx ON im_business_channel_access(conversation_id,access_type,user_id);

CREATE TABLE IF NOT EXISTS im_business_channel_send_state(
 conversation_id text NOT NULL REFERENCES im_business_channels(conversation_id) ON DELETE CASCADE,
 user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 last_sent_at timestamptz NOT NULL,
 PRIMARY KEY(conversation_id,user_id)
);

CREATE TABLE IF NOT EXISTS im_support_skill_groups(
 id text PRIMARY KEY,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 description text NOT NULL DEFAULT '',
 routing_strategy text NOT NULL DEFAULT 'least_active' CHECK(routing_strategy IN ('least_active','round_robin')),
 max_concurrent_per_agent integer NOT NULL DEFAULT 5 CHECK(max_concurrent_per_agent BETWEEN 1 AND 100),
 enabled boolean NOT NULL DEFAULT true,
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS im_support_agents(
 user_id text PRIMARY KEY REFERENCES im_users(id) ON DELETE CASCADE,
 status text NOT NULL DEFAULT 'offline' CHECK(status IN ('offline','available','busy','away')),
 max_concurrent integer NOT NULL DEFAULT 5 CHECK(max_concurrent BETWEEN 1 AND 100),
 last_assigned_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS im_support_agent_skills(
 skill_group_id text NOT NULL REFERENCES im_support_skill_groups(id) ON DELETE CASCADE,
 user_id text NOT NULL REFERENCES im_support_agents(user_id) ON DELETE CASCADE,
 priority integer NOT NULL DEFAULT 100 CHECK(priority BETWEEN 1 AND 1000),
 enabled boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(skill_group_id,user_id)
);
CREATE INDEX IF NOT EXISTS im_support_agent_skills_routing_idx ON im_support_agent_skills(skill_group_id,enabled,priority,user_id);
CREATE TABLE IF NOT EXISTS im_support_sessions(
 id text PRIMARY KEY,
 visitor_id text NOT NULL REFERENCES im_users(id),
 skill_group_id text NOT NULL REFERENCES im_support_skill_groups(id),
 channel_id text NOT NULL REFERENCES im_business_channels(conversation_id),
 channel_type smallint NOT NULL CHECK(channel_type IN (3,10)),
 subject text NOT NULL DEFAULT '',
 status text NOT NULL CHECK(status IN ('queued','active','transferring','ended')),
 assigned_agent_id text REFERENCES im_support_agents(user_id),
 metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
 queue_entered_at timestamptz NOT NULL,
 assigned_at timestamptz,
 ended_at timestamptz,
 ended_by text,
 transfer_count integer NOT NULL DEFAULT 0 CHECK(transfer_count>=0),
 rating smallint CHECK(rating BETWEEN 1 AND 5),
 rating_comment text NOT NULL DEFAULT '',
 rated_at timestamptz,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 CHECK((channel_type=10 AND channel_id=visitor_id) OR
       (channel_type=3 AND channel_id=visitor_id||'|'||skill_group_id)),
 CHECK((status='queued' AND assigned_agent_id IS NULL AND assigned_at IS NULL AND ended_at IS NULL) OR
       (status IN ('active','transferring') AND assigned_agent_id IS NOT NULL AND assigned_at IS NOT NULL AND ended_at IS NULL) OR
       (status='ended' AND ended_at IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS im_support_sessions_active_visitor_idx ON im_support_sessions(visitor_id) WHERE status IN ('queued','active','transferring');
CREATE INDEX IF NOT EXISTS im_support_sessions_queue_idx ON im_support_sessions(skill_group_id,queue_entered_at,id) WHERE status='queued';
CREATE INDEX IF NOT EXISTS im_support_sessions_agent_idx ON im_support_sessions(assigned_agent_id,updated_at DESC,id) WHERE status IN ('active','transferring');
CREATE TABLE IF NOT EXISTS im_support_session_events(
 id bigserial PRIMARY KEY,
 session_id text NOT NULL REFERENCES im_support_sessions(id) ON DELETE CASCADE,
 event text NOT NULL,
 actor_id text NOT NULL,
 data jsonb NOT NULL DEFAULT '{}'::jsonb,
 created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS im_support_session_events_session_idx ON im_support_session_events(session_id,id);
-- Media must exist before sticker packs/items add foreign keys to it. Keeping
-- this base table close to the first media consumer also makes a completely
-- fresh schema (including restore drills) independent of historical tables.
CREATE TABLE IF NOT EXISTS im_media(id text PRIMARY KEY,owner_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,object_key text UNIQUE NOT NULL,mime text NOT NULL,size bigint NOT NULL,status text NOT NULL,checksum text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),completed_at timestamptz);
CREATE TABLE IF NOT EXISTS im_moments(
 id text PRIMARY KEY,
 author_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 content text NOT NULL DEFAULT '' CHECK(length(content)<=5000),
 media_kind text NOT NULL DEFAULT 'none' CHECK(media_kind IN ('none','images','video')),
 media_ids text[] NOT NULL DEFAULT '{}' CHECK(cardinality(media_ids)<=9),
 visibility text NOT NULL DEFAULT 'friends' CHECK(visibility IN ('public','friends','private','selected','excluded')),
 visible_user_ids text[] NOT NULL DEFAULT '{}',
 location jsonb NOT NULL DEFAULT '{}',
 status text NOT NULL DEFAULT 'published' CHECK(status IN ('published','hidden','deleted')),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 deleted_at timestamptz,
 CHECK(content<>'' OR cardinality(media_ids)>0),
 CHECK((media_kind='none' AND cardinality(media_ids)=0) OR
       (media_kind='images' AND cardinality(media_ids) BETWEEN 1 AND 9) OR
       (media_kind='video' AND cardinality(media_ids)=1)),
 CHECK((visibility IN ('selected','excluded')) OR cardinality(visible_user_ids)=0)
);
CREATE INDEX IF NOT EXISTS im_moments_feed_idx ON im_moments(created_at DESC,id DESC) WHERE status='published';
CREATE INDEX IF NOT EXISTS im_moments_author_idx ON im_moments(author_id,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS im_moments_media_idx ON im_moments USING gin(media_ids);
CREATE TABLE IF NOT EXISTS im_moment_likes(
 moment_id text NOT NULL REFERENCES im_moments(id) ON DELETE CASCADE,
 user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(moment_id,user_id)
);
CREATE TABLE IF NOT EXISTS im_moment_comments(
 id text PRIMARY KEY,
 moment_id text NOT NULL REFERENCES im_moments(id) ON DELETE CASCADE,
 author_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 parent_id text REFERENCES im_moment_comments(id) ON DELETE SET NULL,
 reply_to_user_id text REFERENCES im_users(id) ON DELETE SET NULL,
 content text NOT NULL CHECK(length(content) BETWEEN 1 AND 2000),
 status text NOT NULL DEFAULT 'active' CHECK(status IN ('active','deleted')),
 created_at timestamptz NOT NULL,
 deleted_at timestamptz
);
CREATE INDEX IF NOT EXISTS im_moment_comments_moment_idx ON im_moment_comments(moment_id,created_at,id) WHERE status='active';
CREATE TABLE IF NOT EXISTS im_moment_reminders(
 id bigserial PRIMARY KEY,
 user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 moment_id text NOT NULL REFERENCES im_moments(id) ON DELETE CASCADE,
 actor_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 type text NOT NULL CHECK(type IN ('like','comment','reply')),
 comment_id text REFERENCES im_moment_comments(id) ON DELETE CASCADE,
 read_at timestamptz,
 created_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS im_moment_reminders_dedupe_idx ON im_moment_reminders(user_id,moment_id,actor_id,type,COALESCE(comment_id,''));
CREATE INDEX IF NOT EXISTS im_moment_reminders_user_idx ON im_moment_reminders(user_id,read_at,created_at DESC,id DESC);
CREATE TABLE IF NOT EXISTS im_sticker_categories(
 id text PRIMARY KEY,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
 sort_order integer NOT NULL DEFAULT 1000,
 enabled boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS im_sticker_packs(
 id text PRIMARY KEY,
 category_id text NOT NULL REFERENCES im_sticker_categories(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 description text NOT NULL DEFAULT '' CHECK(length(description)<=2000),
 cover_media_id text NOT NULL REFERENCES im_media(id),
 status text NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','reviewing','published','rejected','disabled')),
 sort_order integer NOT NULL DEFAULT 1000,
 created_by text NOT NULL,
 reviewed_by text,
 review_reason text NOT NULL DEFAULT '',
 reviewed_at timestamptz,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS im_sticker_packs_public_idx ON im_sticker_packs(category_id,sort_order,id) WHERE status='published';
CREATE TABLE IF NOT EXISTS im_sticker_items(
 id text PRIMARY KEY,
 pack_id text NOT NULL REFERENCES im_sticker_packs(id) ON DELETE CASCADE,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 media_id text NOT NULL REFERENCES im_media(id),
 emoji text NOT NULL DEFAULT '',
 sort_order integer NOT NULL DEFAULT 1000,
 status text NOT NULL DEFAULT 'published' CHECK(status IN ('published','disabled')),
 metadata jsonb NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 UNIQUE(pack_id,media_id)
);
CREATE INDEX IF NOT EXISTS im_sticker_items_pack_idx ON im_sticker_items(pack_id,sort_order,id) WHERE status='published';
CREATE TABLE IF NOT EXISTS im_sticker_pack_favorites(
 user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 pack_id text NOT NULL REFERENCES im_sticker_packs(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(user_id,pack_id)
);
CREATE TABLE IF NOT EXISTS im_sticker_item_favorites(
 user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 sticker_id text NOT NULL REFERENCES im_sticker_items(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(user_id,sticker_id)
);
CREATE TABLE IF NOT EXISTS im_sticker_recent(
 user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 sticker_id text NOT NULL REFERENCES im_sticker_items(id) ON DELETE CASCADE,
 use_count integer NOT NULL DEFAULT 1 CHECK(use_count>0),
 used_at timestamptz NOT NULL,
 PRIMARY KEY(user_id,sticker_id)
);
CREATE INDEX IF NOT EXISTS im_sticker_recent_user_idx ON im_sticker_recent(user_id,used_at DESC,sticker_id);
CREATE TABLE IF NOT EXISTS im_group_announcement_reads(conversation_id text NOT NULL REFERENCES im_groups(conversation_id) ON DELETE CASCADE,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,announcement_version bigint NOT NULL,read_at timestamptz NOT NULL,PRIMARY KEY(conversation_id,user_id,announcement_version));
CREATE TABLE IF NOT EXISTS im_group_invites(id text PRIMARY KEY,conversation_id text NOT NULL REFERENCES im_groups(conversation_id) ON DELETE CASCADE,inviter_id text NOT NULL REFERENCES im_users(id),invitee_id text NOT NULL REFERENCES im_users(id),source text NOT NULL,status text NOT NULL,created_at timestamptz NOT NULL,expires_at timestamptz NOT NULL,updated_at timestamptz NOT NULL,resolved_at timestamptz);
CREATE UNIQUE INDEX IF NOT EXISTS im_group_invites_pending_idx ON im_group_invites(conversation_id,invitee_id) WHERE status='pending';
CREATE INDEX IF NOT EXISTS im_group_invites_expiry_idx ON im_group_invites(expires_at,id) WHERE status='pending';
-- Message payloads, ordering and idempotency are owned exclusively by
-- WuKongIM. PostgreSQL stores only business extensions keyed by the WuKong
-- message id; these tables intentionally have no payload-table foreign key.
CREATE TABLE IF NOT EXISTS im_message_edits(message_id text NOT NULL,version integer NOT NULL CHECK(version>=0),edit_id text,editor_id text NOT NULL REFERENCES im_users(id),body jsonb NOT NULL,created_at timestamptz NOT NULL,PRIMARY KEY(message_id,version));
CREATE UNIQUE INDEX IF NOT EXISTS im_message_edits_idempotency_idx ON im_message_edits(message_id,edit_id) WHERE edit_id IS NOT NULL;
CREATE TABLE IF NOT EXISTS im_message_edit_requests(message_id text NOT NULL,edit_id text NOT NULL,body jsonb NOT NULL,version integer NOT NULL CHECK(version>=0),created_at timestamptz NOT NULL,PRIMARY KEY(message_id,edit_id));
ALTER TABLE im_message_edits DROP CONSTRAINT IF EXISTS im_message_edits_message_id_fkey;
ALTER TABLE im_message_edit_requests DROP CONSTRAINT IF EXISTS im_message_edit_requests_message_id_fkey;
CREATE TABLE IF NOT EXISTS im_message_reactions(message_id text NOT NULL,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,emoji text NOT NULL,created_at timestamptz NOT NULL,PRIMARY KEY(message_id,user_id,emoji));
ALTER TABLE im_message_reactions DROP CONSTRAINT IF EXISTS im_message_reactions_message_id_fkey;
CREATE INDEX IF NOT EXISTS im_message_reactions_aggregate_idx ON im_message_reactions(message_id,emoji);
CREATE TABLE IF NOT EXISTS im_group_message_pins(conversation_id text NOT NULL REFERENCES im_groups(conversation_id) ON DELETE CASCADE,message_id text NOT NULL,pinned_by text NOT NULL REFERENCES im_users(id),pinned_at timestamptz NOT NULL,PRIMARY KEY(conversation_id,message_id));
ALTER TABLE im_group_message_pins DROP CONSTRAINT IF EXISTS im_group_message_pins_message_id_fkey;
CREATE INDEX IF NOT EXISTS im_group_message_pins_list_idx ON im_group_message_pins(conversation_id,pinned_at DESC,message_id);
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
CREATE TABLE IF NOT EXISTS im_scheduled_messages(
 id text PRIMARY KEY,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 conversation_id text NOT NULL REFERENCES im_conversations(id) ON DELETE CASCADE,
 client_msg_id text NOT NULL,message_type text NOT NULL,body jsonb NOT NULL,reply_to_id text,
 expires_in_seconds bigint NOT NULL DEFAULT 0,scheduled_at timestamptz NOT NULL,
 status text NOT NULL CHECK(status IN ('pending','processing','sent','cancelled','failed')),
 attempts integer NOT NULL DEFAULT 0,available_at timestamptz NOT NULL,locked_at timestamptz,
 last_error text NOT NULL DEFAULT '',sent_message_id text,
 created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL,
 UNIQUE(user_id,client_msg_id)
);
ALTER TABLE im_scheduled_messages DROP CONSTRAINT IF EXISTS im_scheduled_messages_sent_message_id_fkey;
CREATE INDEX IF NOT EXISTS im_scheduled_messages_due_idx ON im_scheduled_messages(available_at,scheduled_at,id) WHERE status IN ('pending','processing');
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
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_status text NOT NULL DEFAULT '';
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_locked_at timestamptz;
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_attempts integer NOT NULL DEFAULT 0;
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_last_error text NOT NULL DEFAULT '';
ALTER TABLE im_media ADD COLUMN IF NOT EXISTS cleanup_updated_at timestamptz;
CREATE INDEX IF NOT EXISTS im_media_owner_idx ON im_media(owner_id,created_at DESC);
CREATE TABLE IF NOT EXISTS im_wukong_media_channels(
  media_id text NOT NULL REFERENCES im_media(id) ON DELETE CASCADE,
  channel_id text NOT NULL,
  channel_type smallint NOT NULL CHECK(channel_type IN (1,2,3,4,5,6,9,10)),
  sender_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(media_id,channel_id,channel_type)
);
CREATE INDEX IF NOT EXISTS im_wukong_media_channels_channel_idx ON im_wukong_media_channels(channel_id,channel_type,created_at DESC);

CREATE TABLE IF NOT EXISTS im_wukong_credentials(
  user_id TEXT NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
  device_flag SMALLINT NOT NULL CHECK(device_flag BETWEEN 0 AND 2),
  device_level SMALLINT NOT NULL CHECK(device_level BETWEEN 0 AND 1),
  token_digest CHAR(64) NOT NULL,
  provisioned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(user_id,device_flag)
);
CREATE INDEX IF NOT EXISTS im_media_cleanup_idx ON im_media(created_at,id) WHERE cleanup_status IN ('','pending','processing');
CREATE TABLE IF NOT EXISTS im_call_sessions(
  id text PRIMARY KEY,
  conversation_id text NOT NULL REFERENCES im_conversations(id) ON DELETE CASCADE,
  caller_id text NOT NULL REFERENCES im_users(id),
  callee_id text REFERENCES im_users(id),
  call_kind text NOT NULL DEFAULT 'direct' CHECK(call_kind IN ('direct','group')),
  participant_ids text[] NOT NULL DEFAULT '{}',
  joined_user_ids text[] NOT NULL DEFAULT '{}',
  declined_user_ids text[] NOT NULL DEFAULT '{}',
  left_user_ids text[] NOT NULL DEFAULT '{}',
  media_type text NOT NULL CHECK(media_type IN ('audio','video')),
  status text NOT NULL CHECK(status IN ('invited','accepted','rejected','cancelled','ended','missed')),
  end_reason text NOT NULL DEFAULT '',
  ended_by text NOT NULL DEFAULT '',
  invited_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  accepted_at timestamptz,
  ended_at timestamptz,
  updated_at timestamptz NOT NULL,
  CONSTRAINT im_call_sessions_direct_callee_check CHECK(
    (call_kind='direct' AND callee_id IS NOT NULL AND caller_id<>callee_id)
    OR (call_kind='group' AND callee_id IS NULL)
  )
);
ALTER TABLE im_call_sessions ADD COLUMN IF NOT EXISTS call_kind text NOT NULL DEFAULT 'direct';
ALTER TABLE im_call_sessions ADD COLUMN IF NOT EXISTS participant_ids text[] NOT NULL DEFAULT '{}';
ALTER TABLE im_call_sessions ADD COLUMN IF NOT EXISTS joined_user_ids text[] NOT NULL DEFAULT '{}';
ALTER TABLE im_call_sessions ADD COLUMN IF NOT EXISTS declined_user_ids text[] NOT NULL DEFAULT '{}';
ALTER TABLE im_call_sessions ADD COLUMN IF NOT EXISTS left_user_ids text[] NOT NULL DEFAULT '{}';
UPDATE im_call_sessions SET participant_ids=ARRAY[caller_id,callee_id],joined_user_ids=CASE WHEN status='accepted' THEN ARRAY[caller_id,callee_id] ELSE ARRAY[caller_id] END
  WHERE cardinality(participant_ids)=0 AND callee_id IS NOT NULL;
ALTER TABLE im_call_sessions ALTER COLUMN callee_id DROP NOT NULL;
ALTER TABLE im_call_sessions DROP CONSTRAINT IF EXISTS im_call_sessions_check;
DO $$ BEGIN
  IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='im_call_sessions_direct_callee_check') THEN
    ALTER TABLE im_call_sessions ADD CONSTRAINT im_call_sessions_direct_callee_check CHECK(
      (call_kind='direct' AND callee_id IS NOT NULL AND caller_id<>callee_id)
      OR (call_kind='group' AND callee_id IS NULL)
    );
  END IF;
END $$;
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
CREATE TABLE IF NOT EXISTS im_favorites(user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,message_id text NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(user_id,message_id));
ALTER TABLE im_favorites DROP CONSTRAINT IF EXISTS im_favorites_message_id_fkey;
CREATE INDEX IF NOT EXISTS im_favorites_user_idx ON im_favorites(user_id,created_at DESC);
CREATE TABLE IF NOT EXISTS im_feedback(id text PRIMARY KEY,user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,category text NOT NULL DEFAULT 'other',content text NOT NULL,contact text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS im_feedback_created_idx ON im_feedback(created_at DESC);

CREATE TABLE IF NOT EXISTS im_wukong_webhook_events(
 id text PRIMARY KEY,
 event_type text NOT NULL CHECK(event_type IN ('msg.offline','msg.notify','user.onlinestatus','msg.stream')),
 payload jsonb NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','completed','failed')),
 attempts integer NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT now(),
 locked_at timestamptz,
 last_error text NOT NULL DEFAULT '',
 received_at timestamptz NOT NULL,
 completed_at timestamptz
);
CREATE INDEX IF NOT EXISTS im_wukong_webhook_pending_idx ON im_wukong_webhook_events(status,available_at,received_at) WHERE status IN ('pending','processing');
CREATE INDEX IF NOT EXISTS im_wukong_webhook_retention_idx ON im_wukong_webhook_events(completed_at,id) WHERE status='completed';

CREATE TABLE IF NOT EXISTS im_wukong_presence(
 user_id text PRIMARY KEY,
 online boolean NOT NULL DEFAULT false,
 total_online_count integer NOT NULL DEFAULT 0 CHECK(total_online_count>=0),
 device_flag smallint NOT NULL DEFAULT 0 CHECK(device_flag BETWEEN 0 AND 2),
 device_online_count integer NOT NULL DEFAULT 0 CHECK(device_online_count>=0),
 conn_id bigint NOT NULL DEFAULT 0,
 last_offline_at timestamptz,
 updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS im_wukong_presence_online_idx ON im_wukong_presence(online,updated_at DESC);

-- PostgreSQL keeps only the authorization/search metadata required by business
-- features. WuKongIM remains the sole owner of the physical message payload.
CREATE TABLE IF NOT EXISTS im_wukong_message_index(
 message_id bigint PRIMARY KEY,
 client_msg_no text NOT NULL,
 conversation_id text REFERENCES im_conversations(id) ON DELETE SET NULL,
 sender_id text NOT NULL,
 channel_id text NOT NULL,
 channel_type smallint NOT NULL CHECK(channel_type IN (1,2,3,4,5,6,9,10,11,12)),
 topic text NOT NULL DEFAULT '',
 message_seq bigint NOT NULL,
 content_type integer NOT NULL,
 expire_seconds bigint NOT NULL DEFAULT 0,
 media_id text NOT NULL DEFAULT '',
 expires_at timestamptz,
 expired_at timestamptz,
 payload_sha256 text NOT NULL,
 message_timestamp timestamptz NOT NULL,
 indexed_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS im_wukong_message_index_client_idx ON im_wukong_message_index(sender_id,client_msg_no) WHERE client_msg_no<>'';
CREATE INDEX IF NOT EXISTS im_wukong_message_index_conversation_idx ON im_wukong_message_index(conversation_id,message_seq DESC,message_id DESC);
CREATE INDEX IF NOT EXISTS im_wukong_message_index_channel_idx ON im_wukong_message_index(channel_id,channel_type,message_seq DESC);
CREATE INDEX IF NOT EXISTS im_wukong_message_index_timestamp_idx ON im_wukong_message_index(message_timestamp DESC);
ALTER TABLE im_wukong_message_index ADD COLUMN IF NOT EXISTS media_id text NOT NULL DEFAULT '';
ALTER TABLE im_wukong_message_index ADD COLUMN IF NOT EXISTS expires_at timestamptz;
ALTER TABLE im_wukong_message_index ADD COLUMN IF NOT EXISTS expired_at timestamptz;
UPDATE im_wukong_message_index
 SET expires_at=message_timestamp + expire_seconds * interval '1 second'
 WHERE expire_seconds>0 AND expires_at IS NULL;
CREATE INDEX IF NOT EXISTS im_wukong_message_index_expiry_idx
 ON im_wukong_message_index(expires_at,message_id)
 WHERE expires_at IS NOT NULL AND expired_at IS NULL;

CREATE TABLE IF NOT EXISTS im_wukong_outbox(
 id bigserial PRIMARY KEY,
 idempotency_key text NOT NULL UNIQUE,
 operation text NOT NULL,
 aggregate_type text NOT NULL,
 aggregate_id text NOT NULL,
 payload jsonb NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','completed','failed')),
 attempts integer NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT now(),
 locked_at timestamptz,
 last_error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz
);

CREATE TABLE IF NOT EXISTS im_wukong_plugin_releases(
  plugin_no text PRIMARY KEY,
  node_id bigint NOT NULL CHECK(node_id>0),
  name text NOT NULL,
  file_name text NOT NULL UNIQUE,
  version text NOT NULL,
  methods text[] NOT NULL DEFAULT '{}',
  sha256 text NOT NULL CHECK(length(sha256)=64),
  size_bytes bigint NOT NULL CHECK(size_bytes>0),
  key_id text NOT NULL,
  status text NOT NULL CHECK(status IN ('installing','active','disabled','failed','uninstalled')),
  manifest jsonb NOT NULL,
  last_actor text NOT NULL,
  last_reason text NOT NULL,
  installed_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS im_wukong_plugin_releases_status_idx ON im_wukong_plugin_releases(status,updated_at DESC);

CREATE TABLE IF NOT EXISTS im_wukong_plugin_events(
  id bigserial PRIMARY KEY,
  plugin_no text NOT NULL,
  action text NOT NULL,
  status text NOT NULL,
  actor_id text NOT NULL,
  reason text NOT NULL,
  details jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS im_wukong_plugin_events_plugin_idx ON im_wukong_plugin_events(plugin_no,id DESC);
CREATE INDEX IF NOT EXISTS im_wukong_outbox_pending_idx ON im_wukong_outbox(status,available_at,id) WHERE status IN ('pending','processing');
CREATE INDEX IF NOT EXISTS im_wukong_outbox_aggregate_idx ON im_wukong_outbox(aggregate_type,aggregate_id,id DESC);

-- The business database is authoritative while WuKongIM keeps the live cache.
-- Disabled rows remain only until their latest remove operation completes so
-- the management console can accurately display processing and failed states.
CREATE TABLE IF NOT EXISTS im_wukong_system_users(
 user_id text PRIMARY KEY REFERENCES im_users(id) ON DELETE CASCADE,
 enabled boolean NOT NULL,
 updated_by text NOT NULL,
 reason text NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS im_wukong_system_users_enabled_idx ON im_wukong_system_users(enabled,user_id);

CREATE SEQUENCE IF NOT EXISTS im_wukong_message_extension_sync_version_seq;
CREATE TABLE IF NOT EXISTS im_wukong_message_extensions(
 channel_id text NOT NULL,
 channel_type smallint NOT NULL CHECK(channel_type IN (1,2,3,4,5,6,9,10)),
 message_id bigint NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 sync_version bigint NOT NULL DEFAULT nextval('im_wukong_message_extension_sync_version_seq'),
 payload jsonb NOT NULL,
 updated_by text NOT NULL REFERENCES im_users(id),
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(channel_id,channel_type,message_id)
);
ALTER TABLE im_wukong_message_extensions ADD COLUMN IF NOT EXISTS sync_version bigint;
ALTER TABLE im_wukong_message_extensions ALTER COLUMN sync_version SET DEFAULT nextval('im_wukong_message_extension_sync_version_seq');
UPDATE im_wukong_message_extensions SET sync_version=nextval('im_wukong_message_extension_sync_version_seq') WHERE sync_version IS NULL;
ALTER TABLE im_wukong_message_extensions ALTER COLUMN sync_version SET NOT NULL;
CREATE INDEX IF NOT EXISTS im_wukong_message_extensions_sync_idx ON im_wukong_message_extensions(updated_at,channel_id,channel_type);
CREATE UNIQUE INDEX IF NOT EXISTS im_wukong_message_extensions_version_idx ON im_wukong_message_extensions(sync_version);
CREATE INDEX IF NOT EXISTS im_wukong_message_extensions_channel_version_idx ON im_wukong_message_extensions(channel_id,channel_type,sync_version);

-- Reminder rows use their own monotonically increasing version because both
-- creation and completion must be observable by every device. The official
-- SDKs resume globally from the largest reminder version, not per channel.
CREATE SEQUENCE IF NOT EXISTS im_wukong_reminder_version_seq;
CREATE TABLE IF NOT EXISTS im_wukong_reminders(
 id bigserial PRIMARY KEY,
 user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 conversation_id text NOT NULL REFERENCES im_conversations(id) ON DELETE CASCADE,
 message_id bigint NOT NULL,
 message_seq bigint NOT NULL,
 channel_id text NOT NULL,
 channel_type smallint NOT NULL CHECK(channel_type IN (1,2,3,4,5,6,9,10)),
 type smallint NOT NULL CHECK(type>0),
 is_locate boolean NOT NULL DEFAULT true,
 text text NOT NULL DEFAULT '',
 data jsonb NOT NULL DEFAULT '{}'::jsonb,
 version bigint NOT NULL DEFAULT nextval('im_wukong_reminder_version_seq'),
 done boolean NOT NULL DEFAULT false,
 need_upload boolean NOT NULL DEFAULT false,
 publisher text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(user_id,message_id,type)
);
CREATE UNIQUE INDEX IF NOT EXISTS im_wukong_reminders_version_idx ON im_wukong_reminders(version);
CREATE INDEX IF NOT EXISTS im_wukong_reminders_user_version_idx ON im_wukong_reminders(user_id,version);
CREATE INDEX IF NOT EXISTS im_wukong_reminders_channel_idx ON im_wukong_reminders(user_id,channel_id,channel_type,done,message_seq DESC);

-- Subscriber changes are append-only so Web and mobile SDK adapters can
-- resume from an exact version and still observe member removals.
CREATE TABLE IF NOT EXISTS im_wukong_channel_member_events(
 version bigserial PRIMARY KEY,
 conversation_id text NOT NULL REFERENCES im_conversations(id) ON DELETE CASCADE,
 user_id text NOT NULL REFERENCES im_users(id) ON DELETE CASCADE,
 role text NOT NULL,
 muted_until timestamptz,
 group_nickname text NOT NULL DEFAULT '',
 is_deleted boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS im_wukong_channel_member_events_sync_idx ON im_wukong_channel_member_events(conversation_id,version);

CREATE OR REPLACE FUNCTION im_wukong_record_member_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_row im_members%ROWTYPE;
BEGIN
  IF TG_OP='UPDATE' AND OLD.role=NEW.role
     AND OLD.muted_until IS NOT DISTINCT FROM NEW.muted_until
     AND OLD.group_nickname=NEW.group_nickname THEN
    RETURN NULL;
  END IF;
  IF TG_OP='DELETE' THEN source_row := OLD; ELSE source_row := NEW; END IF;
  INSERT INTO im_wukong_channel_member_events(
    conversation_id,user_id,role,muted_until,group_nickname,is_deleted,created_at,updated_at
  ) VALUES(
    source_row.conversation_id,source_row.user_id,source_row.role,source_row.muted_until,
    source_row.group_nickname,TG_OP='DELETE',source_row.joined_at,now()
  );
  RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS im_wukong_member_event ON im_members;
CREATE TRIGGER im_wukong_member_event AFTER INSERT OR UPDATE OR DELETE ON im_members
FOR EACH ROW EXECUTE FUNCTION im_wukong_record_member_event();

CREATE OR REPLACE FUNCTION im_wukong_record_user_member_events() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.name=NEW.name AND OLD.handle IS NOT DISTINCT FROM NEW.handle AND OLD.avatar_url=NEW.avatar_url THEN
    RETURN NULL;
  END IF;
  INSERT INTO im_wukong_channel_member_events(
    conversation_id,user_id,role,muted_until,group_nickname,is_deleted,created_at,updated_at
  ) SELECT conversation_id,user_id,role,muted_until,group_nickname,false,joined_at,now()
    FROM im_members WHERE user_id=NEW.id;
  RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS im_wukong_user_member_event ON im_users;
CREATE TRIGGER im_wukong_user_member_event AFTER UPDATE OF name,handle,avatar_url ON im_users
FOR EACH ROW EXECUTE FUNCTION im_wukong_record_user_member_events();

INSERT INTO im_wukong_channel_member_events(
 conversation_id,user_id,role,muted_until,group_nickname,is_deleted,created_at,updated_at
)
SELECT member.conversation_id,member.user_id,member.role,member.muted_until,
 member.group_nickname,false,member.joined_at,now()
FROM im_members member
WHERE COALESCE((
 SELECT event.is_deleted FROM im_wukong_channel_member_events event
 WHERE event.conversation_id=member.conversation_id AND event.user_id=member.user_id
 ORDER BY event.version DESC LIMIT 1
),true);

CREATE TABLE IF NOT EXISTS im_client_version_policies(
 platform text PRIMARY KEY CHECK(platform IN ('android','ios','web','macos')),
 minimum_version text NOT NULL,
 latest_version text NOT NULL,
 force_update boolean NOT NULL DEFAULT false,
 rollout_percentage integer NOT NULL DEFAULT 100 CHECK(rollout_percentage BETWEEN 0 AND 100),
 release_notes text NOT NULL DEFAULT '',
 download_url text NOT NULL DEFAULT '',
 updated_by text NOT NULL,
 updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS im_client_version_policies_updated_idx
 ON im_client_version_policies(updated_at DESC,platform);

-- WuKongIM owns durable message and conversation synchronization. These
-- legacy per-user cursor tables must not receive writes or survive upgrades.
DROP TABLE IF EXISTS im_sync_events;
DROP TABLE IF EXISTS im_user_cursors;
DROP TABLE IF EXISTS im_messages;
DROP TABLE IF EXISTS im_message_fanout;
