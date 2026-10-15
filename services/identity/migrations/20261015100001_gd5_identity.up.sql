-- FAILED_LOGIN locks expire and are cleared by a password reset; ADMIN locks are lifted only by an admin (or expiry).
ALTER TABLE users
    ADD COLUMN lock_kind   varchar(12) CHECK (lock_kind IN ('FAILED_LOGIN', 'ADMIN')),
    ADD COLUMN lock_reason text;

UPDATE users SET lock_kind = CASE WHEN locked_until IS NULL THEN 'ADMIN' ELSE 'FAILED_LOGIN' END WHERE status = 'LOCKED';

ALTER TABLE users ADD CONSTRAINT users_lock_kind CHECK ((status = 'LOCKED') = (lock_kind IS NOT NULL));

-- GET /v1/me/blocks lists the newest blocks first.
CREATE INDEX user_blocks_blocker_idx ON user_blocks (blocker_id, created_at DESC);
