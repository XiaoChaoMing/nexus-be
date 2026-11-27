DROP TABLE IF EXISTS vods;

DELETE FROM admin_actions WHERE type = 'TAKEDOWN_VOD' OR target_type = 'VOD';
UPDATE admin_actions SET report_id = NULL WHERE report_id IN (SELECT id FROM reports WHERE target_type = 'VOD');
DELETE FROM reports WHERE target_type = 'VOD';

ALTER TABLE admin_actions DROP CONSTRAINT admin_actions_target_type_check;
ALTER TABLE admin_actions ADD CONSTRAINT admin_actions_target_type_check
    CHECK (target_type IN ('USER', 'CHANNEL', 'STREAM'));
ALTER TABLE admin_actions DROP CONSTRAINT admin_actions_type_check;
ALTER TABLE admin_actions ADD CONSTRAINT admin_actions_type_check
    CHECK (type IN ('LOCK_USER', 'UNLOCK_USER', 'SUSPEND_CHANNEL', 'RESTORE_CHANNEL', 'TAKEDOWN_STREAM'));

ALTER TABLE reports DROP CONSTRAINT reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('USER', 'CHANNEL', 'STREAM', 'MESSAGE'));
