-- Copy of identity accounts (user.registered / user.profile_updated / user.email_verified): names shown
-- next to messages and @mention lookups. Usernames are unique case-insensitively, as in identity.
CREATE TABLE users (
    id             uuid        PRIMARY KEY,
    username       varchar(25) NOT NULL,
    display_name   varchar(50) NOT NULL DEFAULT '',
    email_verified boolean     NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_username_idx ON users (lower(username));

-- Copy of channel owners (channel.created): the Streamer badge and who may change chat modes.
CREATE TABLE channels (
    channel_id uuid        PRIMARY KEY,
    owner_id   uuid        NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
