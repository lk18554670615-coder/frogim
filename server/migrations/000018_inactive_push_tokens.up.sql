ALTER TABLE im_devices DROP CONSTRAINT IF EXISTS im_devices_provider_push_token_key;
CREATE UNIQUE INDEX IF NOT EXISTS im_devices_active_provider_push_token_idx
  ON im_devices(provider,push_token)
  WHERE notifications_enabled AND push_token<>'';
