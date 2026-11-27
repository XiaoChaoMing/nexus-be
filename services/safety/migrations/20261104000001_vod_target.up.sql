-- Reports and admin actions on a VOD (a broadcast recording or an uploaded video).
ALTER TABLE reports DROP CONSTRAINT reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('USER', 'CHANNEL', 'STREAM', 'MESSAGE', 'VOD'));

ALTER TABLE admin_actions DROP CONSTRAINT admin_actions_type_check;
ALTER TABLE admin_actions ADD CONSTRAINT admin_actions_type_check
    CHECK (type IN ('LOCK_USER', 'UNLOCK_USER', 'SUSPEND_CHANNEL', 'RESTORE_CHANNEL', 'TAKEDOWN_STREAM', 'TAKEDOWN_VOD'));
ALTER TABLE admin_actions DROP CONSTRAINT admin_actions_target_type_check;
ALTER TABLE admin_actions ADD CONSTRAINT admin_actions_target_type_check
    CHECK (target_type IN ('USER', 'CHANNEL', 'STREAM', 'VOD'));

-- Copy of the VODs (stream.vod_ready, vod.published, vod.updated, vod.deleted, or stream-svc's internal API on a miss).
CREATE TABLE vods (
    id         uuid         PRIMARY KEY,
    channel_id uuid         NOT NULL,
    stream_id  uuid,
    source     varchar(8)   NOT NULL DEFAULT 'LIVE' CHECK (source IN ('LIVE', 'UPLOAD')),
    title      varchar(140) NOT NULL DEFAULT '',
    deleted    boolean      NOT NULL DEFAULT false,
    synced_at  timestamptz  NOT NULL
);
CREATE INDEX vods_channel_idx ON vods (channel_id);
