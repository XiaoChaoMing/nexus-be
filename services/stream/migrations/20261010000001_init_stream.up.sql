CREATE TABLE stream_keys (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    channel_id uuid        NOT NULL,
    key_hash   text        NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);

CREATE UNIQUE INDEX stream_keys_one_active_idx ON stream_keys (channel_id) WHERE revoked_at IS NULL;

CREATE TABLE streams (
    id                 uuid         PRIMARY KEY DEFAULT uuidv7(),
    channel_id         uuid         NOT NULL,
    schedule_id        uuid,
    title              varchar(140) NOT NULL,
    category_id        uuid,
    tags               text[]       NOT NULL DEFAULT '{}',
    language           varchar(10)  NOT NULL DEFAULT 'vi',
    is_mature          boolean      NOT NULL DEFAULT false,
    status             varchar(12)  NOT NULL
        CHECK (status IN ('SCHEDULED', 'LIVE', 'INTERRUPTED', 'ENDED', 'CANCELLED')),
    scheduled_start_at timestamptz,
    started_at         timestamptz,
    interrupted_at     timestamptz,
    ended_at           timestamptz,
    end_reason         varchar(16)  CHECK (end_reason IN ('creator', 'ingest_timeout', 'admin_takedown')),
    peak_viewers       integer      NOT NULL DEFAULT 0 CHECK (peak_viewers >= 0),
    created_at         timestamptz  NOT NULL DEFAULT now(),
    CHECK ((status = 'ENDED') = (end_reason IS NOT NULL AND ended_at IS NOT NULL)),
    CHECK (status <> 'INTERRUPTED' OR interrupted_at IS NOT NULL)
);

-- NXL-A §7A: a channel has at most one stream that is on air.
CREATE UNIQUE INDEX streams_one_on_air_idx ON streams (channel_id) WHERE status IN ('LIVE', 'INTERRUPTED');
CREATE INDEX streams_channel_created_idx ON streams (channel_id, created_at DESC);
CREATE INDEX streams_status_idx ON streams (status) WHERE status IN ('SCHEDULED', 'LIVE', 'INTERRUPTED');

CREATE TABLE vods (
    id            uuid         PRIMARY KEY DEFAULT uuidv7(),
    stream_id     uuid         NOT NULL UNIQUE REFERENCES streams (id),
    channel_id    uuid         NOT NULL,
    title         varchar(140) NOT NULL,
    duration_sec  integer      CHECK (duration_sec >= 0),
    playlist_path text,
    thumbnail_url text,
    visibility    varchar(10)  NOT NULL DEFAULT 'PUBLIC' CHECK (visibility IN ('PUBLIC', 'HIDDEN')),
    status        varchar(12)  NOT NULL DEFAULT 'PROCESSING' CHECK (status IN ('PROCESSING', 'READY', 'EXPIRED', 'DELETED')),
    created_at    timestamptz  NOT NULL DEFAULT now(),
    expires_at    timestamptz  NOT NULL
);

CREATE INDEX vods_channel_idx ON vods (channel_id, created_at DESC) WHERE status = 'READY';
CREATE INDEX vods_expiry_idx ON vods (expires_at) WHERE status = 'READY';

CREATE TABLE playback_sessions (
    id                  uuid        PRIMARY KEY DEFAULT uuidv7(),
    stream_id           uuid        REFERENCES streams (id),
    vod_id              uuid        REFERENCES vods (id),
    user_id             uuid,
    anon_id             varchar(40),
    mature_acknowledged boolean     NOT NULL DEFAULT false,
    created_at          timestamptz NOT NULL DEFAULT now(),
    expires_at          timestamptz NOT NULL,
    CHECK (num_nonnulls(stream_id, vod_id) = 1),
    CHECK (user_id IS NOT NULL OR anon_id IS NOT NULL)
);

CREATE INDEX playback_sessions_expiry_idx ON playback_sessions (expires_at);

CREATE TABLE watch_progress (
    user_id      uuid        NOT NULL,
    vod_id       uuid        NOT NULL REFERENCES vods (id) ON DELETE CASCADE,
    position_sec integer     NOT NULL CHECK (position_sec >= 0),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, vod_id)
);

CREATE INDEX watch_progress_recent_idx ON watch_progress (user_id, updated_at DESC);

CREATE TABLE mature_consents (
    user_id    uuid        NOT NULL,
    channel_id uuid        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, channel_id)
);
