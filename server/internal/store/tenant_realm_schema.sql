-- 77: enterprise runtime access is independent of user identity and local bans.
ALTER TABLE im_tenant_identity ADD COLUMN IF NOT EXISTS access_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE im_tenant_identity ADD COLUMN IF NOT EXISTS access_version bigint NOT NULL DEFAULT 1 CHECK(access_version>0);
CREATE TABLE IF NOT EXISTS im_tenant_realm_operations (
 operation_id text PRIMARY KEY,access_version bigint NOT NULL UNIQUE,enabled boolean NOT NULL,
 state text NOT NULL CHECK(state IN ('revoking','completed')),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS im_tenant_realm_targets (
 operation_id text NOT NULL REFERENCES im_tenant_realm_operations(operation_id),
 local_user_id text NOT NULL REFERENCES im_users(id),completed boolean NOT NULL DEFAULT false,
 PRIMARY KEY(operation_id,local_user_id)
);
