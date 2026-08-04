ALTER TABLE im_users
  ADD COLUMN IF NOT EXISTS handle_change_count integer NOT NULL DEFAULT 0;

ALTER TABLE im_users DROP CONSTRAINT IF EXISTS im_users_handle_change_count_check;
ALTER TABLE im_users ADD CONSTRAINT im_users_handle_change_count_check
  CHECK(handle_change_count BETWEEN 0 AND 2);

UPDATE im_users
SET handle = 'll_' || right(md5(id || created_at::text), 20)
WHERE handle IS NULL OR handle = '' OR handle !~ '^[a-z0-9_]{6,24}$';

CREATE UNIQUE INDEX IF NOT EXISTS im_users_handle_unique_idx ON im_users(lower(handle));
