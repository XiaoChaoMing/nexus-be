DROP TABLE IF EXISTS user_channels;

ALTER TABLE creator_applications DROP COLUMN IF EXISTS updated_at;
-- The previous CHECKs do not know CANCELLED.
DELETE FROM creator_applications WHERE status = 'CANCELLED';
ALTER TABLE creator_applications DROP CONSTRAINT IF EXISTS creator_applications_reviewed_check;
ALTER TABLE creator_applications ADD CONSTRAINT creator_applications_check
    CHECK (status = 'PENDING' OR (reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL));
ALTER TABLE creator_applications DROP CONSTRAINT IF EXISTS creator_applications_status_check;
ALTER TABLE creator_applications ADD CONSTRAINT creator_applications_status_check
    CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED'));

DROP INDEX IF EXISTS email_tokens_user_purpose_idx;
ALTER TABLE email_tokens DROP COLUMN IF EXISTS created_at;

ALTER TABLE users
    DROP COLUMN IF EXISTS preferences,
    DROP COLUMN IF EXISTS last_failed_login_at,
    DROP COLUMN IF EXISTS failed_logins;
