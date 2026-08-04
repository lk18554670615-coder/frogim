DROP INDEX IF EXISTS im_devices_active_provider_push_token_idx;
UPDATE im_devices SET push_token='invalidated:'||id WHERE push_token='';
ALTER TABLE im_devices ADD CONSTRAINT im_devices_provider_push_token_key UNIQUE(provider,push_token);
