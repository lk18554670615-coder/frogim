ALTER TABLE im_users DROP CONSTRAINT IF EXISTS im_users_handle_change_count_check;
ALTER TABLE im_users DROP COLUMN IF EXISTS handle_change_count;
