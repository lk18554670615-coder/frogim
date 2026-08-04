DROP TABLE IF EXISTS im_group_invites;
DROP TABLE IF EXISTS im_group_announcement_reads;
DROP TABLE IF EXISTS im_groups;
ALTER TABLE im_members DROP COLUMN IF EXISTS group_nickname;
