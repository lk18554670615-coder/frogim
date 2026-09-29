-- 76: platform access state is independent of the enterprise's banned flag.
ALTER TABLE im_users DROP CONSTRAINT IF EXISTS im_users_local_identity_state_check;
ALTER TABLE im_users ADD CONSTRAINT im_users_local_identity_state_check CHECK(local_identity_state IN ('prepared','active','credentials_resetting','platform_blocked','retired'));
CREATE TABLE IF NOT EXISTS im_tenant_access_operations (
 operation_id text PRIMARY KEY,
 account_id text NOT NULL,
 local_user_id text NOT NULL REFERENCES im_users(id),
 assignment_version bigint NOT NULL,
 auth_version bigint NOT NULL,
 blocked boolean NOT NULL,
 state text NOT NULL CHECK(state IN ('applying','completed')),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
