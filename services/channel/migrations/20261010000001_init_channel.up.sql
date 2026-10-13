CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS unaccent;

-- unaccent() is STABLE, so it cannot be used in an index; pinning the dictionary makes this wrapper safe to mark IMMUTABLE.
CREATE FUNCTION immutable_unaccent(text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
    RETURN public.unaccent('public.unaccent'::regdictionary, $1);

CREATE TABLE categories (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    slug       varchar(60) NOT NULL UNIQUE,
    name       varchar(80) NOT NULL,
    poster_url text,
    group_code varchar(10) NOT NULL CHECK (group_code IN ('GAMES', 'ESPORTS', 'MUSIC', 'IRL', 'CREATIVE')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE channels (
    id                uuid        PRIMARY KEY DEFAULT uuidv7(),
    owner_id          uuid        NOT NULL UNIQUE,
    handle            citext      NOT NULL UNIQUE CHECK (handle ~ '^[A-Za-z0-9_]{3,25}$'),
    display_name      varchar(50) NOT NULL,
    bio               text,
    avatar_url        text,
    banner_url        text,
    social_links      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    verified          boolean     NOT NULL DEFAULT false,
    mature_default    boolean     NOT NULL DEFAULT false,
    status            varchar(10) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUSPENDED')),
    -- Copies of stream-svc state, kept in sync by stream.started / stream.ended.
    is_live           boolean     NOT NULL DEFAULT false,
    current_stream_id uuid,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX channels_live_idx ON channels (is_live) WHERE is_live;

CREATE TABLE follows (
    user_id      uuid        NOT NULL,
    channel_id   uuid        NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    notify_level varchar(10) NOT NULL DEFAULT 'ALL' CHECK (notify_level IN ('ALL', 'LIVE_ONLY', 'OFF')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, channel_id)
);

CREATE INDEX follows_channel_idx ON follows (channel_id, created_at);

CREATE TABLE schedules (
    id           uuid         PRIMARY KEY DEFAULT uuidv7(),
    channel_id   uuid         NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    category_id  uuid         REFERENCES categories (id) ON DELETE SET NULL,
    title        varchar(140) NOT NULL,
    start_at     timestamptz  NOT NULL,
    duration_min integer      NOT NULL CHECK (duration_min > 0),
    rrule        text,
    status       varchar(10)  NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'CANCELLED')),
    created_at   timestamptz  NOT NULL DEFAULT now()
);

CREATE INDEX schedules_channel_start_idx ON schedules (channel_id, start_at) WHERE status = 'ACTIVE';

CREATE TABLE reminders (
    user_id       uuid        NOT NULL,
    schedule_id   uuid        NOT NULL REFERENCES schedules (id) ON DELETE CASCADE,
    occurrence_at timestamptz NOT NULL,
    notified_at   timestamptz,
    PRIMARY KEY (user_id, schedule_id, occurrence_at)
);

CREATE INDEX reminders_due_idx ON reminders (occurrence_at) WHERE notified_at IS NULL;

CREATE TABLE search_documents (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    entity_type    varchar(10) NOT NULL CHECK (entity_type IN ('CHANNEL', 'STREAM', 'CATEGORY', 'VOD')),
    entity_id      uuid        NOT NULL,
    title          text        NOT NULL,
    title_unaccent text        NOT NULL GENERATED ALWAYS AS (lower(immutable_unaccent(title))) STORED,
    tsv            tsvector    NOT NULL GENERATED ALWAYS AS (to_tsvector('simple', lower(immutable_unaccent(title)))) STORED,
    is_live        boolean     NOT NULL DEFAULT false,
    viewers        integer     NOT NULL DEFAULT 0 CHECK (viewers >= 0),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (entity_type, entity_id)
);

CREATE INDEX search_documents_tsv_idx ON search_documents USING gin (tsv);
CREATE INDEX search_documents_prefix_idx ON search_documents (title_unaccent text_pattern_ops);

CREATE TABLE search_history (
    id         uuid         PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid         NOT NULL,
    query      varchar(100) NOT NULL,
    created_at timestamptz  NOT NULL DEFAULT now()
);

CREATE INDEX search_history_user_idx ON search_history (user_id, created_at DESC);
