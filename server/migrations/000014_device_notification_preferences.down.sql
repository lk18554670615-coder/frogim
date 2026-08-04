ALTER TABLE im_devices
  DROP COLUMN IF EXISTS vibration_enabled,
  DROP COLUMN IF EXISTS sound_enabled,
  DROP COLUMN IF EXISTS preview_enabled,
  DROP COLUMN IF EXISTS notifications_enabled;
