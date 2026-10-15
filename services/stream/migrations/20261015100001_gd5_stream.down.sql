ALTER TABLE creator_channels DROP COLUMN IF EXISTS suspended;
ALTER TABLE streams DROP CONSTRAINT IF EXISTS streams_taken_down;
ALTER TABLE streams DROP COLUMN IF EXISTS taken_down_at;
