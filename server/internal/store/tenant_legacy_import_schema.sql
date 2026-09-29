-- 79: operator-driven adoption keeps historical business IDs. Credentials are
-- held in the platform directory, not copied into a migration payload table.
CREATE TABLE IF NOT EXISTS im_tenant_legacy_imports (
 operation_id text PRIMARY KEY,
 account_id text NOT NULL UNIQUE,
 local_user_id text NOT NULL UNIQUE REFERENCES im_users(id),
 assignment_version bigint NOT NULL CHECK(assignment_version=1),
 auth_version bigint NOT NULL CHECK(auth_version=2),
 source_fingerprint text NOT NULL CHECK(source_fingerprint ~ '^[a-f0-9]{64}$'),
 state text NOT NULL CHECK(state IN ('bound','completed')),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
