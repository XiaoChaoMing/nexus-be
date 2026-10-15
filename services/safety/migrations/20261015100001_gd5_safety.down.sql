DROP TABLE IF EXISTS replica_backfills;
DROP TABLE IF EXISTS streams;
DROP TABLE IF EXISTS channels;
DROP TABLE IF EXISTS users;

DROP INDEX IF EXISTS audit_logs_action_idx;
DROP INDEX IF EXISTS audit_logs_created_idx;
DROP INDEX IF EXISTS audit_logs_source_event_idx;
-- The append-only trigger blocks UPDATE / DELETE of rows, not DROP COLUMN.
ALTER TABLE audit_logs DROP COLUMN IF EXISTS source_event_id;

ALTER TABLE admin_actions DROP CONSTRAINT IF EXISTS admin_actions_reason_length;
DELETE FROM admin_actions WHERE type = 'RESTORE_CHANNEL';
ALTER TABLE admin_actions DROP CONSTRAINT admin_actions_type_check;
ALTER TABLE admin_actions ADD CONSTRAINT admin_actions_type_check
    CHECK (type IN ('LOCK_USER', 'UNLOCK_USER', 'SUSPEND_CHANNEL', 'TAKEDOWN_STREAM'));

DROP INDEX IF EXISTS reports_target_user_idx;
DROP INDEX IF EXISTS reports_open_dedupe_idx;
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_message_stream, DROP CONSTRAINT IF EXISTS reports_resolved_by,
    DROP CONSTRAINT IF EXISTS reports_note_length;
ALTER TABLE reports DROP COLUMN IF EXISTS updated_at, DROP COLUMN IF EXISTS resolution_note, DROP COLUMN IF EXISTS resolved_by,
    DROP COLUMN IF EXISTS evidence_status, DROP COLUMN IF EXISTS target_user_id, DROP COLUMN IF EXISTS stream_id;
