DROP INDEX IF EXISTS user_blocks_blocker_idx;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_lock_kind;
ALTER TABLE users DROP COLUMN IF EXISTS lock_reason, DROP COLUMN IF EXISTS lock_kind;
