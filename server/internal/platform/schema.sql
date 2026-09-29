-- This schema is exclusively for a separate platform database. Never execute
-- it in an enterprise database or copy an enterprise volume as a template.
CREATE TABLE IF NOT EXISTS platform_schema_migrations(version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS platform_tenants (
 id text PRIMARY KEY,
 display_name text NOT NULL,
 http_base_url text NOT NULL UNIQUE,
 status text NOT NULL DEFAULT 'provisioning' CHECK(status IN ('provisioning','active','suspended')),
 config_version bigint NOT NULL DEFAULT 1 CHECK(config_version>0),
 is_default boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_one_default ON platform_tenants(is_default) WHERE is_default;
CREATE TABLE IF NOT EXISTS platform_enterprise_codes (
 code_hash bytea PRIMARY KEY,
 tenant_id text NOT NULL REFERENCES platform_tenants(id),
 enabled boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS platform_accounts (
 id text PRIMARY KEY,
 phone text NOT NULL UNIQUE,
 password_hash text NOT NULL,
 state text NOT NULL CHECK(state IN ('provisioning','active','transferring','blocked','deleted')),
 tenant_id text NOT NULL REFERENCES platform_tenants(id),
 local_user_id text NOT NULL,
 assignment_version bigint NOT NULL DEFAULT 1 CHECK(assignment_version>0),
 auth_version bigint NOT NULL DEFAULT 1 CHECK(auth_version>0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,local_user_id)
);
CREATE TABLE IF NOT EXISTS platform_sessions (
 token_hash bytea PRIMARY KEY,
 account_id text NOT NULL REFERENCES platform_accounts(id),
 assignment_version bigint NOT NULL,
 auth_version bigint NOT NULL,
 expires_at timestamptz NOT NULL,
 revoked_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS platform_sessions_account ON platform_sessions(account_id);
CREATE TABLE IF NOT EXISTS platform_tickets (
 ticket_hash bytea PRIMARY KEY,
 session_hash bytea NOT NULL REFERENCES platform_sessions(token_hash),
 account_id text NOT NULL REFERENCES platform_accounts(id),
 tenant_id text NOT NULL REFERENCES platform_tenants(id),
 local_user_id text NOT NULL,
 assignment_version bigint NOT NULL,
 expires_at timestamptz NOT NULL,
 consumed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS platform_jobs (
 id text PRIMARY KEY,
 kind text NOT NULL CHECK(kind IN ('registration','transfer')),
 account_id text NOT NULL REFERENCES platform_accounts(id),
 source_tenant_id text REFERENCES platform_tenants(id),
 source_local_user_id text,
 source_version bigint,
 target_tenant_id text NOT NULL REFERENCES platform_tenants(id),
 target_local_user_id text NOT NULL,
 target_version bigint NOT NULL,
 step text NOT NULL CHECK(step IN ('revoke_source','prepare_target','activate','completed')),
 -- Only provisioning inputs, never passwords, OTPs, business history or tokens.
 input jsonb NOT NULL DEFAULT '{}',
 poll_hash bytea,
 actor_id text NOT NULL,
 reason text NOT NULL,
 attempts integer NOT NULL DEFAULT 0,
 retry_at timestamptz NOT NULL DEFAULT now(),
 lease_id text,
 lease_until timestamptz,
 error_code text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_one_identity_job ON platform_jobs(account_id) WHERE step<>'completed';
CREATE TABLE IF NOT EXISTS platform_audits (
 id bigserial PRIMARY KEY,
 actor_id text NOT NULL,
 action text NOT NULL,
 account_id text,
 tenant_id text,
 job_id text,
 reason text NOT NULL DEFAULT '',
 metadata jsonb NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO platform_schema_migrations(version) VALUES(1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS platform_admin_accounts (
 id text PRIMARY KEY,
 username text NOT NULL UNIQUE,
 password_hash text NOT NULL,
 role text NOT NULL CHECK(role IN ('operator','reader')),
 enabled boolean NOT NULL DEFAULT true,
 auth_version bigint NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS platform_admin_sessions (
 token_hash bytea PRIMARY KEY,
 admin_id text NOT NULL REFERENCES platform_admin_accounts(id),
 auth_version bigint NOT NULL,
 expires_at timestamptz NOT NULL,
 revoked_at timestamptz
);
-- Additive preview upgrades; keep older local preview databases repeatable.
ALTER TABLE platform_enterprise_codes ADD COLUMN IF NOT EXISTS id text NOT NULL DEFAULT gen_random_uuid()::text;
ALTER TABLE platform_enterprise_codes ADD COLUMN IF NOT EXISTS suffix text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS platform_enterprise_code_id ON platform_enterprise_codes(id);
ALTER TABLE platform_jobs ADD COLUMN IF NOT EXISTS blocked boolean NOT NULL DEFAULT false;
INSERT INTO platform_schema_migrations(version) VALUES(2) ON CONFLICT DO NOTHING;
ALTER TABLE platform_jobs ADD COLUMN IF NOT EXISTS request_id text;
CREATE UNIQUE INDEX IF NOT EXISTS platform_registration_request ON platform_jobs(target_tenant_id,request_id) WHERE kind='registration' AND request_id IS NOT NULL;
INSERT INTO platform_schema_migrations(version) VALUES(3) ON CONFLICT DO NOTHING;
ALTER TABLE platform_accounts ADD COLUMN IF NOT EXISTS credentials_pending boolean NOT NULL DEFAULT false;
-- Authentication material stays only in the platform auth database. No
-- plaintext password or OTP is persisted in jobs, audit or enterprise storage.
CREATE TABLE IF NOT EXISTS platform_credential_jobs (
 id text PRIMARY KEY,
 request_id text NOT NULL,
 account_id text NOT NULL REFERENCES platform_accounts(id),
 tenant_id text NOT NULL REFERENCES platform_tenants(id),
 local_user_id text NOT NULL,
 assignment_version bigint NOT NULL,
 auth_version bigint NOT NULL,
 new_password_hash text NOT NULL,
 requester_hash bytea NOT NULL,
 actor_id text NOT NULL,
 purpose text NOT NULL CHECK(purpose IN ('change','admin','reset')),
 reason text NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','completed')),
 attempts integer NOT NULL DEFAULT 0,
 retry_at timestamptz NOT NULL DEFAULT now(),
 lease_id text,
 lease_until timestamptz,
 error_code text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(actor_id,purpose,request_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_one_credential_job ON platform_credential_jobs(account_id) WHERE state='pending';
CREATE INDEX IF NOT EXISTS platform_credential_local ON platform_credential_jobs(tenant_id,local_user_id,created_at DESC);
INSERT INTO platform_schema_migrations(version) VALUES(4) ON CONFLICT DO NOTHING;
-- A recovery capability is independent of login/refresh tokens. The six digit
-- code is HMACed with that random capability, which is itself stored only hashed.
CREATE TABLE IF NOT EXISTS platform_password_recovery (
 id text PRIMARY KEY,
 capability_hash bytea NOT NULL UNIQUE,
 phone text NOT NULL,
 account_id text REFERENCES platform_accounts(id),
 assignment_version bigint,
 auth_version bigint,
 code_hash bytea NOT NULL,
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
 verified_at timestamptz,
 consumed_at timestamptz,
 delivery_status text NOT NULL DEFAULT 'sending' CHECK(delivery_status IN ('sending','sent','unconfirmed')),
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS platform_password_recovery_account ON platform_password_recovery(account_id);
INSERT INTO platform_schema_migrations(version) VALUES(5) ON CONFLICT DO NOTHING;
-- Global access is independent of an in-flight identity/password lifecycle.
ALTER TABLE platform_accounts ADD COLUMN IF NOT EXISTS globally_blocked boolean NOT NULL DEFAULT false;
UPDATE platform_accounts SET globally_blocked=true WHERE state='blocked' AND NOT globally_blocked;
CREATE TABLE IF NOT EXISTS platform_access_jobs (
 id text PRIMARY KEY,
 request_id text NOT NULL,
 account_id text NOT NULL REFERENCES platform_accounts(id),
 actor_id text NOT NULL,
 blocked boolean NOT NULL,
 expected_auth_version bigint NOT NULL,
 reason text NOT NULL,
 state text NOT NULL DEFAULT 'waiting' CHECK(state IN ('waiting','applying','completed')),
 tenant_id text,
 local_user_id text,
 assignment_version bigint,
 auth_version bigint,
 attempts integer NOT NULL DEFAULT 0,
 retry_at timestamptz NOT NULL DEFAULT now(),
 lease_id text,
 lease_until timestamptz,
 error_code text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(actor_id,request_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_one_access_job ON platform_access_jobs(account_id) WHERE state<>'completed';
INSERT INTO platform_schema_migrations(version) VALUES(6) ON CONFLICT DO NOTHING;
ALTER TABLE platform_tenants ADD COLUMN IF NOT EXISTS access_version bigint NOT NULL DEFAULT 1 CHECK(access_version>0);
ALTER TABLE platform_tenants DROP CONSTRAINT IF EXISTS platform_tenants_status_check;
ALTER TABLE platform_tenants ADD CONSTRAINT platform_tenants_status_check CHECK(status IN ('provisioning','active','suspending','suspended','resuming'));
ALTER TABLE platform_sessions ADD COLUMN IF NOT EXISTS realm_version bigint NOT NULL DEFAULT 1;
ALTER TABLE platform_password_recovery ADD COLUMN IF NOT EXISTS realm_version bigint;
CREATE TABLE IF NOT EXISTS platform_realm_jobs (
 id text PRIMARY KEY, request_id text NOT NULL, actor_id text NOT NULL,
 tenant_id text NOT NULL REFERENCES platform_tenants(id),
 enabled boolean NOT NULL, expected_version bigint NOT NULL, access_version bigint NOT NULL,
 reason text NOT NULL, state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','completed')),
 remaining integer NOT NULL DEFAULT 0, attempts integer NOT NULL DEFAULT 0,
 retry_at timestamptz NOT NULL DEFAULT now(),lease_id text,lease_until timestamptz,
 error_code text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(actor_id,request_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_one_realm_job ON platform_realm_jobs(tenant_id) WHERE state<>'completed';
INSERT INTO platform_schema_migrations(version) VALUES(7) ON CONFLICT DO NOTHING;

-- Independent global client rollout policies. No enterprise business table.
CREATE TABLE IF NOT EXISTS platform_client_version_policies (
 platform text PRIMARY KEY CHECK(platform IN ('android','ios','web','macos')),
 enabled boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0),
 policy jsonb NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO platform_client_version_policies(platform,policy)
 SELECT p,jsonb_build_object('platform',p,'minimumVersion','0.0.0','latestVersion','0.0.0',
 'forceUpdate',false,'rolloutPercentage',100,'releaseNotes','','downloadUrl','','updatedBy','')
 FROM unnest(ARRAY['android','ios','web','macos']) p ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS platform_client_version_releases (
 id text PRIMARY KEY, platform text NOT NULL REFERENCES platform_client_version_policies(platform),
 actor_id text NOT NULL REFERENCES platform_admin_accounts(id), request_id text NOT NULL,
 expected_revision bigint NOT NULL, revision bigint NOT NULL CHECK(revision>0),
 input jsonb NOT NULL, snapshot jsonb NOT NULL, reason text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(actor_id,request_id), UNIQUE(platform,revision)
);
INSERT INTO platform_schema_migrations(version) VALUES(8) ON CONFLICT DO NOTHING;

ALTER TABLE platform_admin_accounts ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE platform_admin_accounts ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();
-- Fail on a pre-existing case collision instead of silently merging accounts.
CREATE UNIQUE INDEX IF NOT EXISTS platform_admin_username_folded ON platform_admin_accounts(lower(username));
CREATE TABLE IF NOT EXISTS platform_admin_operations (
 actor_id text NOT NULL REFERENCES platform_admin_accounts(id), request_id text NOT NULL,
 target_id text NOT NULL REFERENCES platform_admin_accounts(id),
 input jsonb NOT NULL, password_hash text NOT NULL DEFAULT '', snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(actor_id,request_id)
);
INSERT INTO platform_schema_migrations(version) VALUES(9) ON CONFLICT DO NOTHING;
-- Device credentials belong only to the platform, encrypted with an independent
-- runtime key. No message body, arbitrary payload or plaintext token is stored.
CREATE TABLE IF NOT EXISTS platform_push_devices (
 id text PRIMARY KEY,
 provider text NOT NULL CHECK(provider IN ('getui','apns_voip')),
 token_hash bytea NOT NULL,
 token_cipher bytea NOT NULL,
 device_id text NOT NULL,
 platform text NOT NULL CHECK(platform IN ('android','ios')),
 account_id text NOT NULL REFERENCES platform_accounts(id),
 tenant_id text NOT NULL REFERENCES platform_tenants(id),
 local_user_id text NOT NULL,
 assignment_version bigint NOT NULL,
 auth_version bigint NOT NULL,
 realm_version bigint NOT NULL,
 session_hash bytea NOT NULL REFERENCES platform_sessions(token_hash),
 revision bigint NOT NULL DEFAULT 1,
 notifications_enabled boolean NOT NULL,
 preview_enabled boolean NOT NULL,
 sound_enabled boolean NOT NULL,
 vibration_enabled boolean NOT NULL,
 revoked_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(provider,token_hash)
);
CREATE INDEX IF NOT EXISTS platform_push_devices_account ON platform_push_devices(account_id);
CREATE UNIQUE INDEX IF NOT EXISTS platform_push_devices_session_device ON platform_push_devices(session_hash,provider,device_id) WHERE revoked_at IS NULL;
CREATE TABLE IF NOT EXISTS platform_push_requests (
 id bigserial PRIMARY KEY,
 tenant_id text NOT NULL REFERENCES platform_tenants(id),
 request_id text NOT NULL,
 account_id text NOT NULL REFERENCES platform_accounts(id),
 input_hash bytea NOT NULL,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,request_id)
);
CREATE TABLE IF NOT EXISTS platform_push_deliveries (
 id bigserial PRIMARY KEY,
 request_id bigint NOT NULL REFERENCES platform_push_requests(id),
 device_id text NOT NULL REFERENCES platform_push_devices(id),
 binding_revision bigint NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sent','skipped','invalid')),
 attempts integer NOT NULL DEFAULT 0,
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(request_id,device_id)
);
INSERT INTO platform_schema_migrations(version) VALUES(10) ON CONFLICT DO NOTHING;

DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM platform_schema_migrations WHERE version=11) THEN
  ALTER TABLE platform_push_devices DROP CONSTRAINT platform_push_devices_provider_check;
  ALTER TABLE platform_push_devices ADD CONSTRAINT platform_push_devices_provider_check CHECK(provider IN ('getui','apns_voip','webpush'));
  ALTER TABLE platform_push_devices DROP CONSTRAINT platform_push_devices_platform_check;
  ALTER TABLE platform_push_devices ADD CONSTRAINT platform_push_devices_platform_check CHECK(platform IN ('android','ios','web'));
  ALTER TABLE platform_push_devices ADD COLUMN credential_hash bytea;
  UPDATE platform_push_devices SET credential_hash=token_hash;
  ALTER TABLE platform_push_devices ALTER COLUMN credential_hash SET NOT NULL;
  INSERT INTO platform_schema_migrations(version) VALUES(11);
 END IF;
END $$;

CREATE TABLE IF NOT EXISTS platform_servers (
 id text PRIMARY KEY,
 tenant_id text NOT NULL UNIQUE REFERENCES platform_tenants(id),
 display_name text NOT NULL,
 host_fingerprint text NOT NULL UNIQUE CHECK(host_fingerprint ~ '^[a-f0-9]{64}$'),
 isolation_mode text NOT NULL CHECK(isolation_mode IN ('local_preview','dedicated_host')),
 runtime text NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 verified_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS platform_server_operations (
 actor_id text NOT NULL REFERENCES platform_admin_accounts(id),request_id text NOT NULL,
 server_id text NOT NULL REFERENCES platform_servers(id),
 input jsonb NOT NULL,snapshot jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(actor_id,request_id)
);
INSERT INTO platform_schema_migrations(version) VALUES(12) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS platform_legacy_import_batches (
 id text PRIMARY KEY,
 tenant_id text NOT NULL REFERENCES platform_tenants(id),
 actor_id text NOT NULL, reason text NOT NULL,
 fingerprint text NOT NULL CHECK(fingerprint ~ '^[a-f0-9]{64}$'),
 allow_passwordless boolean NOT NULL,
 realm_version bigint NOT NULL CHECK(realm_version>=2),
 total integer NOT NULL CHECK(total>=0),
 state text NOT NULL CHECK(state IN ('running','completed')),
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_one_legacy_import ON platform_legacy_import_batches(tenant_id) WHERE state<>'completed';
CREATE TABLE IF NOT EXISTS platform_legacy_import_items (
 id text PRIMARY KEY,
 batch_id text NOT NULL REFERENCES platform_legacy_import_batches(id),
 account_id text NOT NULL UNIQUE REFERENCES platform_accounts(id),
 local_user_id text NOT NULL,
 source_fingerprint text NOT NULL CHECK(source_fingerprint ~ '^[a-f0-9]{64}$'),
 banned boolean NOT NULL, banned_until timestamptz,
 step text NOT NULL CHECK(step IN ('reserved','bound','revoked','completed')),
 lease_id text,lease_until timestamptz,
 attempts integer NOT NULL DEFAULT 0,retry_at timestamptz NOT NULL DEFAULT now(),
 error_code text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(batch_id,local_user_id)
);
-- Temporary source bans expire via the same durable, acknowledged access path
-- as operator unblocks. A later ban/auth generation supersedes this schedule.
CREATE TABLE IF NOT EXISTS platform_legacy_ban_expiries (
 account_id text PRIMARY KEY REFERENCES platform_accounts(id),
 request_id text NOT NULL UNIQUE,
 expires_at timestamptz NOT NULL,
 expected_auth_version bigint NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','queued','completed','superseded')),
 access_job_id text REFERENCES platform_access_jobs(id),
 retry_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO platform_schema_migrations(version) VALUES(13) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS platform_deployment_jobs (
 id text PRIMARY KEY, actor_id text NOT NULL REFERENCES platform_admin_accounts(id), request_id text NOT NULL,
 tenant_id text NOT NULL REFERENCES platform_tenants(id), server_id text NOT NULL REFERENCES platform_servers(id),
 input jsonb NOT NULL, operation jsonb NOT NULL, release jsonb NOT NULL, binding jsonb NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','unconfirmed','completed')),
 phase text NOT NULL DEFAULT 'queued', error_code text NOT NULL DEFAULT '',
 agent_attempts integer NOT NULL DEFAULT 0 CHECK(agent_attempts>=0), retry_from integer NOT NULL DEFAULT -1, agent_seen boolean NOT NULL DEFAULT false,
 generation bigint NOT NULL DEFAULT 0 CHECK(generation>=0),
 attempts integer NOT NULL DEFAULT 0, retry_at timestamptz NOT NULL DEFAULT now(),lease_id text,lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(actor_id,request_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_one_deployment ON platform_deployment_jobs(tenant_id) WHERE state<>'completed';
CREATE UNIQUE INDEX IF NOT EXISTS platform_one_server_deployment ON platform_deployment_jobs(server_id) WHERE state<>'completed';
CREATE TABLE IF NOT EXISTS platform_deployment_retries (
 actor_id text NOT NULL REFERENCES platform_admin_accounts(id),request_id text NOT NULL,
 job_id text NOT NULL REFERENCES platform_deployment_jobs(id),input jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(actor_id,request_id)
);
INSERT INTO platform_schema_migrations(version) VALUES(14) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS platform_backup_jobs (
 id text PRIMARY KEY, actor_id text NOT NULL REFERENCES platform_admin_accounts(id), request_id text NOT NULL,
 tenant_id text NOT NULL REFERENCES platform_tenants(id), server_id text NOT NULL REFERENCES platform_servers(id),
 input jsonb NOT NULL, operation jsonb NOT NULL, release jsonb NOT NULL, binding jsonb NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','unconfirmed','completed','failed','cancelled')),
 phase text NOT NULL DEFAULT 'queued', error_code text NOT NULL DEFAULT '',
 receipt jsonb NOT NULL DEFAULT '{}', agent_seen boolean NOT NULL DEFAULT false, dispatch_started boolean NOT NULL DEFAULT false,
 control_action text NOT NULL DEFAULT '' CHECK(control_action IN ('','retry','cancel')), control_revision bigint NOT NULL DEFAULT 0,
 attempts integer NOT NULL DEFAULT 0, retry_at timestamptz NOT NULL DEFAULT now(),lease_id text,lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(actor_id,request_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_one_backup ON platform_backup_jobs(tenant_id) WHERE state IN ('pending','unconfirmed');
CREATE UNIQUE INDEX IF NOT EXISTS platform_one_server_backup ON platform_backup_jobs(server_id) WHERE state IN ('pending','unconfirmed');
CREATE TABLE IF NOT EXISTS platform_backup_controls (
 actor_id text NOT NULL REFERENCES platform_admin_accounts(id),request_id text NOT NULL,
 job_id text NOT NULL REFERENCES platform_backup_jobs(id),input jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(actor_id,request_id)
);
INSERT INTO platform_schema_migrations(version) VALUES(15) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS platform_backup_schedules (
 tenant_id text PRIMARY KEY REFERENCES platform_tenants(id), enabled boolean NOT NULL,
 start_minute_utc integer NOT NULL CHECK(start_minute_utc BETWEEN 0 AND 1439),
 window_minutes integer NOT NULL CHECK(window_minutes BETWEEN 15 AND 180),
 version bigint NOT NULL CHECK(version>0), actor_id text NOT NULL REFERENCES platform_admin_accounts(id),
 reason text NOT NULL, updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS platform_backup_schedule_operations (
 actor_id text NOT NULL REFERENCES platform_admin_accounts(id),request_id text NOT NULL,
 tenant_id text NOT NULL REFERENCES platform_tenants(id),input jsonb NOT NULL,snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(actor_id,request_id)
);
CREATE TABLE IF NOT EXISTS platform_maintenance_runs (
 id text PRIMARY KEY, tenant_id text NOT NULL REFERENCES platform_tenants(id),
 actor_id text NOT NULL REFERENCES platform_admin_accounts(id),schedule_version bigint NOT NULL,
 slot_date date NOT NULL, window_end timestamptz NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','completed','failed','skipped')),
 phase text NOT NULL DEFAULT 'prepare' CHECK(phase IN ('prepare','pause','backup','resume','finished')),
 binding jsonb NOT NULL,release jsonb NOT NULL,generation bigint NOT NULL CHECK(generation>0),
 pause_id text NOT NULL DEFAULT '',backup_id text NOT NULL DEFAULT '',resume_id text NOT NULL DEFAULT '',
 backup_result text NOT NULL DEFAULT '',error_code text NOT NULL DEFAULT '',
 attempts integer NOT NULL DEFAULT 0,retry_at timestamptz NOT NULL DEFAULT now(),lease_id text,lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),UNIQUE(tenant_id,slot_date)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_one_maintenance ON platform_maintenance_runs(tenant_id) WHERE state='pending';
INSERT INTO platform_schema_migrations(version) VALUES(16) ON CONFLICT DO NOTHING;

-- A platform archive is bound to this directory, never to a pretend tenant.
CREATE TABLE IF NOT EXISTS platform_directory_identity (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 directory_id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
 created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO platform_directory_identity(singleton) VALUES(true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS platform_directory_backups (
 id text PRIMARY KEY, directory_id uuid NOT NULL, binding jsonb NOT NULL,
 archive_path_hash text NOT NULL CHECK(archive_path_hash ~ '^[a-f0-9]{64}$'),
 actor_id text NOT NULL, reason text NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','completed')),
 manifest_sha256 text NOT NULL DEFAULT '', manifest_size bigint NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz,
 CHECK((state='pending' AND completed_at IS NULL AND manifest_sha256='' AND manifest_size=0)
    OR (state='completed' AND completed_at IS NOT NULL AND manifest_sha256 ~ '^[a-f0-9]{64}$' AND manifest_size>0))
);
INSERT INTO platform_schema_migrations(version) VALUES(17) ON CONFLICT DO NOTHING;

-- Operator-only platform snapshot worker. No HTTP endpoint accepts backup
-- credentials, paths, commands or a database URL. Disabled until configured.
CREATE TABLE IF NOT EXISTS platform_directory_schedule (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton), directory_id uuid NOT NULL,
 enabled boolean NOT NULL, start_minute_utc integer NOT NULL CHECK(start_minute_utc BETWEEN 0 AND 1439),
 window_minutes integer NOT NULL CHECK(window_minutes BETWEEN 15 AND 180),
 version bigint NOT NULL CHECK(version>0), configuration_hash text NOT NULL CHECK(configuration_hash ~ '^[a-f0-9]{64}$'),
 actor_id text NOT NULL, reason text NOT NULL, updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS platform_directory_daily_runs (
 id text PRIMARY KEY, directory_id uuid NOT NULL, slot_date date NOT NULL,
 schedule_version bigint NOT NULL, configuration_hash text NOT NULL,
 window_end timestamptz NOT NULL, request jsonb NOT NULL,
 attempt integer NOT NULL DEFAULT 1 CHECK(attempt>0),
 state text NOT NULL CHECK(state IN ('queued','capturing','captured','delivering','completed','needs_attention','skipped')),
 proof jsonb NOT NULL DEFAULT '{}', delivery jsonb NOT NULL DEFAULT '{}',
 delivery_attempts integer NOT NULL DEFAULT 0, error_code text NOT NULL DEFAULT '',
 retry_at timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(directory_id,slot_date)
);
CREATE TABLE IF NOT EXISTS platform_directory_schedule_operations (
 actor_id text NOT NULL,request_id text NOT NULL,input jsonb NOT NULL,snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(actor_id,request_id)
);
INSERT INTO platform_schema_migrations(version) VALUES(18) ON CONFLICT DO NOTHING;

-- Restored work remains evidence, never an automatic authorization to replay.
CREATE TABLE IF NOT EXISTS platform_recovery_holds (
 kind text NOT NULL, object_id text NOT NULL, recovery_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(kind,object_id)
);
INSERT INTO platform_schema_migrations(version) VALUES(19) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS platform_legacy_cutovers (
 id text PRIMARY KEY, tenant_id text NOT NULL UNIQUE REFERENCES platform_tenants(id),
 input jsonb NOT NULL, phase text NOT NULL, receipts jsonb NOT NULL DEFAULT '{}',
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK(phase IN ('planned','stopped','backed_up','adopted','imported','routed','opening','completed','rolled_back'))
);
INSERT INTO platform_schema_migrations(version) VALUES(20) ON CONFLICT DO NOTHING;

DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM platform_schema_migrations WHERE version=21) THEN
  ALTER TABLE platform_push_devices DROP CONSTRAINT platform_push_devices_provider_check;
  ALTER TABLE platform_push_devices ADD CONSTRAINT platform_push_devices_provider_check CHECK(provider IN ('getui','getui_voip','webpush','apns_voip'));
  UPDATE platform_push_devices SET revoked_at=clock_timestamp(),token_cipher=''::bytea,revision=revision+1 WHERE provider='apns_voip';
  UPDATE platform_push_deliveries SET status='invalid' WHERE status='pending' AND device_id IN (SELECT id FROM platform_push_devices WHERE provider='apns_voip');
 END IF;
END $$;
INSERT INTO platform_schema_migrations(version) VALUES(21) ON CONFLICT DO NOTHING;
