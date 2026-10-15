-- Set by safety-svc's suspension (safety.channel_actioned.v1); cleared on restore.
ALTER TABLE channels
    ADD COLUMN suspended_at   timestamptz,
    ADD COLUMN suspend_reason text;

UPDATE channels SET suspended_at = now() WHERE status = 'SUSPENDED';

ALTER TABLE channels ADD CONSTRAINT channels_suspended_at CHECK ((status = 'SUSPENDED') = (suspended_at IS NOT NULL));
