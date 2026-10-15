ALTER TABLE channels DROP CONSTRAINT IF EXISTS channels_suspended_at;
ALTER TABLE channels DROP COLUMN IF EXISTS suspend_reason, DROP COLUMN IF EXISTS suspended_at;
