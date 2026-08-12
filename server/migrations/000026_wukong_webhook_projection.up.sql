CREATE TABLE IF NOT EXISTS im_wukong_presence(
  user_id text PRIMARY KEY,
  online boolean NOT NULL DEFAULT false,
  total_online_count integer NOT NULL DEFAULT 0 CHECK(total_online_count>=0),
  device_flag smallint NOT NULL DEFAULT 0 CHECK(device_flag BETWEEN 0 AND 2),
  device_online_count integer NOT NULL DEFAULT 0 CHECK(device_online_count>=0),
  conn_id bigint NOT NULL DEFAULT 0,
  last_offline_at timestamptz,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS im_wukong_presence_online_idx
  ON im_wukong_presence(online,updated_at DESC);

UPDATE im_wukong_webhook_events
SET status='completed',payload='{}'::jsonb,completed_at=now(),locked_at=NULL,last_error='superseded before schema 44'
WHERE event_type IN ('msg.offline','user.onlinestatus','msg.stream')
  AND status IN ('pending','processing') AND received_at<now()-interval '5 minutes';
UPDATE im_wukong_webhook_events
SET payload='{}'::jsonb
WHERE status IN ('completed','failed') AND payload<>'{}'::jsonb;
