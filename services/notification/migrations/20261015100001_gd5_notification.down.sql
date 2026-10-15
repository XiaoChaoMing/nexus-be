DROP TABLE replica_backfills, user_blocks, follows, channels, users;

DROP INDEX email_outbox_done_idx, email_outbox_due_idx, email_outbox_source_event_key;
CREATE INDEX email_outbox_pending_idx ON email_outbox (created_at) WHERE status = 'PENDING';
ALTER TABLE email_outbox
    DROP COLUMN source_event_id,
    DROP COLUMN next_attempt_at,
    DROP COLUMN mandatory,
    DROP COLUMN locale,
    DROP COLUMN notification_id,
    DROP COLUMN user_id;

DELETE FROM notification_settings WHERE type IN ('SCHEDULE', 'NEW_FOLLOWER', 'CREATOR_APPLICATION');
ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_type_check;
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_type_check CHECK (type IN ('CREATOR_LIVE',
    'STREAM_STARTING', 'MENTION', 'REPORT_RESULT', 'SUBSCRIPTION', 'SYSTEM'));

DROP INDEX notifications_created_idx, notifications_group_idx, notifications_user_idx;
CREATE INDEX notifications_user_idx ON notifications (user_id, created_at DESC) WHERE deleted_at IS NULL;
ALTER TABLE notifications
    DROP CONSTRAINT notifications_link_internal,
    DROP CONSTRAINT notifications_source_event_key,
    DROP COLUMN source_event_id,
    DROP COLUMN updated_at,
    DROP COLUMN group_count,
    DROP COLUMN group_key;
DELETE FROM notifications WHERE type IN ('SCHEDULE', 'NEW_FOLLOWER', 'CREATOR_APPLICATION');
ALTER TABLE notifications DROP CONSTRAINT notifications_type_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_type_check CHECK (type IN ('CREATOR_LIVE', 'STREAM_STARTING',
    'MENTION', 'REPORT_RESULT', 'SUBSCRIPTION', 'SYSTEM'));
