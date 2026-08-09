DROP INDEX IF EXISTS im_message_fanout_retention_idx;
DROP INDEX IF EXISTS im_message_fanout_pending_idx;
DROP TABLE IF EXISTS im_message_fanout;
DROP INDEX IF EXISTS im_event_outbox_retention_idx;
DROP INDEX IF EXISTS im_push_outbox_retention_idx;
