-- Copy of channel-svc's channels, kept by channel.created.v1: maps a creator (JWT sub) to their channel.
CREATE TABLE creator_channels (
    owner_id     uuid        PRIMARY KEY,
    channel_id   uuid        NOT NULL UNIQUE,
    handle       varchar(25) NOT NULL,
    display_name varchar(50) NOT NULL,
    avatar_url   text,
    updated_at   timestamptz NOT NULL DEFAULT now()
);
