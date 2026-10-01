CREATE TABLE IF NOT EXISTS lp_tenants(id text PRIMARY KEY,name text NOT NULL,code text UNIQUE NOT NULL,enabled boolean NOT NULL DEFAULT true,is_default boolean NOT NULL DEFAULT false,version bigint NOT NULL DEFAULT 1,services jsonb NOT NULL,control_url text UNIQUE NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS lp_one_default ON lp_tenants(is_default) WHERE is_default;
CREATE TABLE IF NOT EXISTS lp_users(id text PRIMARY KEY,phone text UNIQUE NOT NULL,password_hash text NOT NULL DEFAULT '',tenant_id text NOT NULL REFERENCES lp_tenants(id),revision bigint NOT NULL DEFAULT 1,banned boolean NOT NULL DEFAULT false,pending text NOT NULL DEFAULT '',profile jsonb NOT NULL DEFAULT '{}',created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS lp_memberships(user_id text REFERENCES lp_users(id),tenant_id text REFERENCES lp_tenants(id),profile jsonb NOT NULL DEFAULT '{}',profile_version bigint NOT NULL DEFAULT 0,assignment_version bigint NOT NULL DEFAULT 0,synced_at timestamptz,sync_error text NOT NULL DEFAULT '',PRIMARY KEY(user_id,tenant_id));
CREATE TABLE IF NOT EXISTS lp_sessions(token_hash text PRIMARY KEY,user_id text NOT NULL REFERENCES lp_users(id),expires_at timestamptz NOT NULL,revoked boolean NOT NULL DEFAULT false);
CREATE TABLE IF NOT EXISTS lp_tickets(token_hash text PRIMARY KEY,user_id text NOT NULL REFERENCES lp_users(id),tenant_id text NOT NULL REFERENCES lp_tenants(id),revision bigint NOT NULL,expires_at timestamptz NOT NULL,used boolean NOT NULL DEFAULT false);
CREATE TABLE IF NOT EXISTS lp_operations(id text PRIMARY KEY,user_id text REFERENCES lp_users(id),actor text NOT NULL,kind text NOT NULL,source text NOT NULL,target text NOT NULL,revision bigint NOT NULL,phase text NOT NULL DEFAULT 'prepare',reason text NOT NULL,error text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS lp_audit(id bigserial PRIMARY KEY,actor text NOT NULL,action text NOT NULL,object_id text NOT NULL,reason text NOT NULL,result text NOT NULL,at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS lp_versions(platform text PRIMARY KEY,policy jsonb NOT NULL,version bigint NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS lp_version_history(id bigserial PRIMARY KEY,platform text NOT NULL,version bigint NOT NULL,policy jsonb NOT NULL,actor text NOT NULL,reason text NOT NULL,source text NOT NULL DEFAULT 'publish',recorded_at timestamptz NOT NULL DEFAULT now(),UNIQUE(platform,version));
INSERT INTO lp_version_history(platform,version,policy,actor,reason,source)
SELECT platform,version,policy,'','引入发布历史时保存的当前策略；此前发布信息不可追溯。','baseline' FROM lp_versions
ON CONFLICT(platform,version) DO NOTHING;
CREATE TABLE IF NOT EXISTS lp_otp(phone text NOT NULL,purpose text NOT NULL,code_hash text NOT NULL,expires_at timestamptz NOT NULL,attempts int NOT NULL DEFAULT 0,PRIMARY KEY(phone,purpose));
CREATE TABLE IF NOT EXISTS lp_rate(key text PRIMARY KEY,window_at timestamptz NOT NULL DEFAULT now(),attempts int NOT NULL DEFAULT 0);

ALTER TABLE lp_operations ADD COLUMN IF NOT EXISTS desired_phone text NOT NULL DEFAULT '';
ALTER TABLE lp_operations ADD COLUMN IF NOT EXISTS password_hash text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS lp_phone_reservations(phone text PRIMARY KEY,user_id text NOT NULL REFERENCES lp_users(id),operation_id text NOT NULL UNIQUE REFERENCES lp_operations(id));
