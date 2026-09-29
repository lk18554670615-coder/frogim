-- Migration 73: a business database is fixed to one enterprise, not a
-- collection of per-row tenant_id values. Existing identities stay active.
CREATE TABLE IF NOT EXISTS im_tenant_identity (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 tenant_id text NOT NULL UNIQUE,
 bound_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS platform_account_id text;
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS assignment_version bigint NOT NULL DEFAULT 1 CHECK(assignment_version>0);
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS local_identity_state text NOT NULL DEFAULT 'active' CHECK(local_identity_state IN ('prepared','active','retired'));
ALTER TABLE im_users DROP CONSTRAINT IF EXISTS im_users_phone_key;
CREATE UNIQUE INDEX IF NOT EXISTS im_users_current_phone ON im_users(phone) WHERE local_identity_state<>'retired';
CREATE UNIQUE INDEX IF NOT EXISTS im_users_current_platform_account ON im_users(platform_account_id) WHERE platform_account_id IS NOT NULL AND local_identity_state<>'retired';
CREATE UNIQUE INDEX IF NOT EXISTS im_users_platform_generation ON im_users(platform_account_id,assignment_version) WHERE platform_account_id IS NOT NULL;
CREATE TABLE IF NOT EXISTS im_tenant_operations (
 operation_id text NOT NULL,
 action text NOT NULL CHECK(action IN ('prepare','revoke')),
 account_id text NOT NULL,
 local_user_id text NOT NULL REFERENCES im_users(id),
 assignment_version bigint NOT NULL,
 state text NOT NULL CHECK(state IN ('prepared','revoking','revoked')),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(operation_id,action)
);
