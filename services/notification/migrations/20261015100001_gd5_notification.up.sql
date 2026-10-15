ALTER TABLE notifications DROP CONSTRAINT notifications_type_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_type_check CHECK (type IN ('CREATOR_LIVE', 'STREAM_STARTING',
    'SCHEDULE', 'MENTION', 'NEW_FOLLOWER', 'CREATOR_APPLICATION', 'REPORT_RESULT', 'SUBSCRIPTION', 'SYSTEM'));
ALTER TABLE notifications
    ADD COLUMN group_key       varchar(120),
    ADD COLUMN group_count     integer     NOT NULL DEFAULT 1 CHECK (group_count >= 1),
    ADD COLUMN updated_at      timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN source_event_id uuid;
-- BR-E14: a redelivered fan-out event inserts nothing twice. A grouped row keeps the id of its first event.
ALTER TABLE notifications ADD CONSTRAINT notifications_source_event_key UNIQUE (user_id, source_event_id);
ALTER TABLE notifications ADD CONSTRAINT notifications_link_internal CHECK (link IS NULL OR link LIKE '/%');
DROP INDEX notifications_user_idx;
CREATE INDEX notifications_user_idx ON notifications (user_id, updated_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX notifications_group_idx ON notifications (user_id, group_key, updated_at DESC)
    WHERE group_key IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX notifications_created_idx ON notifications (created_at);

ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_type_check;
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_type_check CHECK (type IN ('CREATOR_LIVE',
    'STREAM_STARTING', 'SCHEDULE', 'MENTION', 'NEW_FOLLOWER', 'CREATOR_APPLICATION', 'REPORT_RESULT', 'SUBSCRIPTION', 'SYSTEM'));

ALTER TABLE email_outbox
    ADD COLUMN user_id         uuid,
    ADD COLUMN notification_id uuid,
    ADD COLUMN locale          varchar(10) NOT NULL DEFAULT 'vi',
    ADD COLUMN mandatory       boolean     NOT NULL DEFAULT false,
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN source_event_id uuid;
-- One e-mail per recipient and source event, also for recipients without an in-app row (in-app turned off).
CREATE UNIQUE INDEX email_outbox_source_event_key ON email_outbox (user_id, source_event_id)
    WHERE user_id IS NOT NULL AND source_event_id IS NOT NULL;
DROP INDEX email_outbox_pending_idx;
CREATE INDEX email_outbox_due_idx ON email_outbox (next_attempt_at) WHERE status = 'PENDING';
CREATE INDEX email_outbox_done_idx ON email_outbox (created_at) WHERE status IN ('SENT', 'FAILED');

-- Copies kept from events (BR-E05) and a one-off backfill.
CREATE TABLE users (
    id             uuid        PRIMARY KEY,
    username       varchar(25) NOT NULL,
    display_name   varchar(50) NOT NULL DEFAULT '',
    email          citext      NOT NULL,
    email_verified boolean     NOT NULL DEFAULT false,
    locale         varchar(10) NOT NULL DEFAULT 'vi',
    timezone       varchar(50) NOT NULL DEFAULT 'Asia/Ho_Chi_Minh',
    status         varchar(10) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'LOCKED', 'DELETED')),
    locked_until   timestamptz,
    synced_at      timestamptz NOT NULL
);

CREATE TABLE channels (
    id           uuid        PRIMARY KEY,
    owner_id     uuid        NOT NULL,
    handle       varchar(25) NOT NULL,
    display_name varchar(50) NOT NULL,
    avatar_url   text,
    status       varchar(10) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUSPENDED')),
    synced_at    timestamptz NOT NULL
);
CREATE INDEX channels_owner_idx ON channels (owner_id);

CREATE TABLE follows (
    channel_id   uuid        NOT NULL,
    user_id      uuid        NOT NULL,
    notify_level varchar(10) NOT NULL CHECK (notify_level IN ('ALL', 'LIVE_ONLY', 'OFF')),
    created_at   timestamptz NOT NULL,
    synced_at    timestamptz NOT NULL,
    PRIMARY KEY (channel_id, user_id)
);
CREATE INDEX follows_user_idx ON follows (user_id);

CREATE TABLE user_blocks (
    blocker_id uuid NOT NULL,
    blocked_id uuid NOT NULL,
    PRIMARY KEY (blocker_id, blocked_id)
);

CREATE TABLE replica_backfills (
    name         varchar(30) PRIMARY KEY,
    completed_at timestamptz NOT NULL DEFAULT now()
);
