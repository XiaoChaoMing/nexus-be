CREATE EXTENSION IF NOT EXISTS citext;

ALTER TABLE reports
    ADD COLUMN stream_id       uuid,
    -- The user behind the target (message author, the user, channel owner): groups reports and admin history.
    ADD COLUMN target_user_id  uuid,
    ADD COLUMN evidence_status varchar(12) NOT NULL DEFAULT 'NONE'
        CHECK (evidence_status IN ('CAPTURED', 'NONE', 'UNAVAILABLE')),
    ADD COLUMN resolved_by     uuid,
    ADD COLUMN resolution_note text,
    ADD COLUMN updated_at      timestamptz NOT NULL DEFAULT now();
ALTER TABLE reports
    ADD CONSTRAINT reports_note_length CHECK (note IS NULL OR char_length(note) <= 500),
    ADD CONSTRAINT reports_resolved_by CHECK ((status IN ('RESOLVED', 'DISMISSED')) = (resolved_by IS NOT NULL)),
    ADD CONSTRAINT reports_message_stream CHECK (target_type <> 'MESSAGE' OR stream_id IS NOT NULL);
-- BR-F15: one open report per reporter and target.
CREATE UNIQUE INDEX reports_open_dedupe_idx ON reports (reporter_id, target_type, target_id)
    WHERE status IN ('OPEN', 'IN_REVIEW');
CREATE INDEX reports_target_user_idx ON reports (target_user_id, created_at DESC) WHERE target_user_id IS NOT NULL;

ALTER TABLE admin_actions DROP CONSTRAINT admin_actions_type_check;
ALTER TABLE admin_actions ADD CONSTRAINT admin_actions_type_check
    CHECK (type IN ('LOCK_USER', 'UNLOCK_USER', 'SUSPEND_CHANNEL', 'RESTORE_CHANNEL', 'TAKEDOWN_STREAM'));
ALTER TABLE admin_actions ADD CONSTRAINT admin_actions_reason_length CHECK (char_length(reason) BETWEEN 10 AND 500);

-- Redelivered events must not add a second audit row; rows written by safety itself have no source event.
ALTER TABLE audit_logs ADD COLUMN source_event_id uuid;
CREATE UNIQUE INDEX audit_logs_source_event_idx ON audit_logs (source_event_id) WHERE source_event_id IS NOT NULL;
CREATE INDEX audit_logs_created_idx ON audit_logs (created_at DESC, id DESC);
CREATE INDEX audit_logs_action_idx ON audit_logs (action, created_at DESC);

-- Copies kept from events (and a one-off backfill): report targets, admin lookups, labels.
CREATE TABLE users (
    id           uuid        PRIMARY KEY,
    username     citext      NOT NULL,
    display_name varchar(50) NOT NULL DEFAULT '',
    global_role  varchar(10) NOT NULL DEFAULT 'USER' CHECK (global_role IN ('USER', 'ADMIN')),
    status       varchar(10) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'LOCKED', 'DELETED')),
    locked_until timestamptz,
    created_at   timestamptz,
    -- Occurred-at of the newest event applied; older replays do not overwrite newer data.
    synced_at    timestamptz NOT NULL
);
CREATE UNIQUE INDEX users_username_idx ON users (username);

CREATE TABLE channels (
    id           uuid        PRIMARY KEY,
    owner_id     uuid        NOT NULL,
    handle       citext      NOT NULL,
    display_name varchar(50) NOT NULL,
    avatar_url   text,
    status       varchar(10) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUSPENDED')),
    suspended_at timestamptz,
    synced_at    timestamptz NOT NULL
);
CREATE INDEX channels_owner_idx ON channels (owner_id);
CREATE INDEX channels_handle_idx ON channels (handle);

CREATE TABLE streams (
    id          uuid         PRIMARY KEY,
    channel_id  uuid         NOT NULL,
    title       varchar(140) NOT NULL DEFAULT '',
    status      varchar(12)  NOT NULL CHECK (status IN ('SCHEDULED', 'LIVE', 'INTERRUPTED', 'ENDED', 'CANCELLED')),
    end_reason  varchar(16),
    vod_id      uuid,
    vod_deleted boolean      NOT NULL DEFAULT false,
    started_at  timestamptz,
    ended_at    timestamptz,
    synced_at   timestamptz  NOT NULL
);
CREATE INDEX streams_channel_idx ON streams (channel_id, started_at DESC);

CREATE TABLE replica_backfills (
    name         varchar(30) PRIMARY KEY,
    completed_at timestamptz NOT NULL DEFAULT now()
);
