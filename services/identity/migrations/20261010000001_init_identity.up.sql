CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id                uuid        PRIMARY KEY DEFAULT uuidv7(),
    username          citext      NOT NULL UNIQUE CHECK (length(username) BETWEEN 3 AND 25),
    display_name      varchar(50) NOT NULL,
    email             citext      NOT NULL UNIQUE,
    email_verified_at timestamptz,
    birth_date        date,
    locale            varchar(10) NOT NULL DEFAULT 'vi',
    timezone          varchar(50) NOT NULL DEFAULT 'Asia/Ho_Chi_Minh',
    global_role       varchar(10) NOT NULL DEFAULT 'USER' CHECK (global_role IN ('USER', 'ADMIN')),
    status            varchar(10) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'LOCKED', 'DELETED')),
    -- NULL while LOCKED means the lock is permanent.
    locked_until      timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz
);

CREATE TABLE credentials (
    user_id       uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    password_hash text        NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id                 uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id            uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    refresh_token_hash text        NOT NULL UNIQUE,
    user_agent         text,
    ip                 inet,
    created_at         timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL,
    revoked_at         timestamptz
);

CREATE INDEX sessions_user_active_idx ON sessions (user_id) WHERE revoked_at IS NULL;

CREATE TABLE email_tokens (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash text        NOT NULL UNIQUE,
    purpose    varchar(10) NOT NULL CHECK (purpose IN ('VERIFY', 'RESET')),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);

CREATE INDEX email_tokens_user_idx ON email_tokens (user_id);

CREATE TABLE creator_applications (
    id            uuid         PRIMARY KEY DEFAULT uuidv7(),
    user_id       uuid         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    legal_name    varchar(100) NOT NULL,
    birth_date    date         NOT NULL,
    note          text,
    status        varchar(10)  NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED')),
    reject_reason text,
    reviewed_by   uuid         REFERENCES users (id),
    reviewed_at   timestamptz,
    created_at    timestamptz  NOT NULL DEFAULT now(),
    CHECK (status = 'PENDING' OR (reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL))
);

CREATE UNIQUE INDEX creator_applications_one_pending_idx ON creator_applications (user_id) WHERE status = 'PENDING';
CREATE INDEX creator_applications_status_idx ON creator_applications (status, created_at);

CREATE TABLE user_blocks (
    blocker_id uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    blocked_id uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker_id, blocked_id),
    CHECK (blocker_id <> blocked_id)
);

CREATE INDEX user_blocks_blocked_idx ON user_blocks (blocked_id);
