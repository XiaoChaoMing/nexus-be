ALTER TABLE users
    ADD COLUMN failed_logins        smallint    NOT NULL DEFAULT 0,
    ADD COLUMN last_failed_login_at timestamptz,
    ADD COLUMN preferences          jsonb       NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE email_tokens ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX email_tokens_user_purpose_idx ON email_tokens (user_id, purpose, created_at DESC);

ALTER TABLE creator_applications DROP CONSTRAINT creator_applications_status_check;
ALTER TABLE creator_applications ADD CONSTRAINT creator_applications_status_check
    CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED', 'CANCELLED'));
ALTER TABLE creator_applications DROP CONSTRAINT creator_applications_check;
ALTER TABLE creator_applications ADD CONSTRAINT creator_applications_reviewed_check
    CHECK (status IN ('PENDING', 'CANCELLED') OR (reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL));
ALTER TABLE creator_applications ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

-- Copy of channel-svc's channels, kept by channel.created.v1 (identity never calls channel-svc).
CREATE TABLE user_channels (
    user_id      uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    channel_id   uuid        NOT NULL UNIQUE,
    handle       citext      NOT NULL,
    display_name varchar(50) NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
