-- 74: password changes preserve the business identity and assignment, while
-- fencing consumed-but-delayed tickets and old business/IM sessions.
ALTER TABLE im_users ADD COLUMN IF NOT EXISTS platform_auth_version bigint NOT NULL DEFAULT 1 CHECK(platform_auth_version>0);
ALTER TABLE im_users DROP CONSTRAINT IF EXISTS im_users_local_identity_state_check;
ALTER TABLE im_users ADD CONSTRAINT im_users_local_identity_state_check CHECK(local_identity_state IN ('prepared','active','credentials_resetting','retired'));
CREATE TABLE IF NOT EXISTS im_tenant_credential_operations (
 operation_id text PRIMARY KEY,
 account_id text NOT NULL,
 local_user_id text NOT NULL REFERENCES im_users(id),
 assignment_version bigint NOT NULL,
 auth_version bigint NOT NULL,
 state text NOT NULL CHECK(state IN ('revoking','completed')),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
