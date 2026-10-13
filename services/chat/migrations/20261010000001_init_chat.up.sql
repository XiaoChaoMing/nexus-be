CREATE TABLE chat_rooms (
    stream_id  uuid        PRIMARY KEY,
    channel_id uuid        NOT NULL,
    status     varchar(10) NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'READ_ONLY', 'CLOSED')),
    opened_at  timestamptz,
    closed_at  timestamptz
);

CREATE INDEX chat_rooms_channel_idx ON chat_rooms (channel_id);

CREATE TABLE chat_settings (
    channel_id    uuid        PRIMARY KEY,
    slow_sec      smallint    NOT NULL DEFAULT 0 CHECK (slow_sec IN (0, 2, 5, 10, 30, 60, 120)),
    -- NULL means Followers-only is off; values are minutes (NXL-B API 04).
    followers_min integer     CHECK (followers_min IN (0, 10, 30, 60, 1440, 10080, 43200, 129600)),
    subs_only     boolean     NOT NULL DEFAULT false,
    updated_by    uuid,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Partitioned by day; chat-svc creates daily partitions ahead of time and drops those past retention.
-- The DEFAULT partition keeps inserts working when a daily partition is missing.
CREATE TABLE chat_messages (
    id            uuid         NOT NULL DEFAULT uuidv7(),
    stream_id     uuid         NOT NULL REFERENCES chat_rooms (stream_id) ON DELETE CASCADE,
    user_id       uuid         NOT NULL,
    client_msg_id uuid         NOT NULL,
    body          varchar(500) NOT NULL,
    fragments     jsonb        NOT NULL DEFAULT '[]'::jsonb,
    type          varchar(10)  NOT NULL DEFAULT 'NORMAL' CHECK (type IN ('NORMAL', 'SYSTEM')),
    pinned        boolean      NOT NULL DEFAULT false,
    deleted_at    timestamptz,
    deleted_by    uuid,
    created_at    timestamptz  NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE chat_messages_default PARTITION OF chat_messages DEFAULT;

CREATE INDEX chat_messages_stream_idx ON chat_messages (stream_id, created_at DESC);
-- A unique index on a partitioned table must contain created_at, so "one pinned message per room" and
-- client_msg_id de-duplication cannot be enforced here; chat-svc enforces both (de-dup through Redis).
CREATE INDEX chat_messages_client_msg_idx ON chat_messages (user_id, client_msg_id);
CREATE INDEX chat_messages_pinned_idx ON chat_messages (stream_id) WHERE pinned AND deleted_at IS NULL;

CREATE TABLE channel_roles (
    channel_id uuid        NOT NULL,
    user_id    uuid        NOT NULL,
    role       varchar(10) NOT NULL CHECK (role IN ('MODERATOR', 'VIP')),
    granted_by uuid        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, user_id)
);

CREATE INDEX channel_roles_user_idx ON channel_roles (user_id);

-- message_id has no foreign key: chat_messages is partitioned and its rows expire with their partition.
CREATE TABLE mod_actions (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    channel_id     uuid        NOT NULL,
    actor_id       uuid        NOT NULL,
    target_user_id uuid,
    message_id     uuid,
    type           varchar(10) NOT NULL CHECK (type IN ('DELETE', 'TIMEOUT', 'BAN', 'UNBAN', 'PIN', 'UNPIN')),
    reason         text,
    expires_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (type <> 'TIMEOUT' OR (target_user_id IS NOT NULL AND expires_at IS NOT NULL)),
    CHECK (type NOT IN ('BAN', 'UNBAN') OR target_user_id IS NOT NULL),
    CHECK (type NOT IN ('DELETE', 'PIN', 'UNPIN') OR message_id IS NOT NULL)
);

CREATE INDEX mod_actions_channel_idx ON mod_actions (channel_id, created_at DESC);
CREATE INDEX mod_actions_target_idx ON mod_actions (channel_id, target_user_id, created_at DESC) WHERE target_user_id IS NOT NULL;

CREATE TABLE blocked_terms (
    id         uuid         PRIMARY KEY DEFAULT uuidv7(),
    channel_id uuid         NOT NULL,
    term       varchar(100) NOT NULL,
    created_by uuid         NOT NULL,
    created_at timestamptz  NOT NULL DEFAULT now(),
    UNIQUE (channel_id, term)
);

CREATE TABLE emotes (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    scope         varchar(10) NOT NULL CHECK (scope IN ('GLOBAL', 'CHANNEL')),
    channel_id    uuid,
    code          varchar(30) NOT NULL CHECK (code ~ '^[A-Za-z0-9_]{2,30}$'),
    image_url     text        NOT NULL,
    animated      boolean     NOT NULL DEFAULT false,
    tier_required smallint    CHECK (tier_required BETWEEN 1 AND 3),
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK ((scope = 'GLOBAL') = (channel_id IS NULL)),
    CHECK (scope = 'CHANNEL' OR tier_required IS NULL)
);

CREATE UNIQUE INDEX emotes_global_code_idx ON emotes (code) WHERE scope = 'GLOBAL';
CREATE UNIQUE INDEX emotes_channel_code_idx ON emotes (channel_id, code) WHERE scope = 'CHANNEL';
