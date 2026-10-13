-- Settings a creator prepares for the next broadcast (Stream Manager); copied into streams on go-live.
CREATE TABLE stream_settings (
    channel_id uuid         PRIMARY KEY,
    title      varchar(140) NOT NULL DEFAULT '',
    tags       text[]       NOT NULL DEFAULT '{}' CHECK (cardinality(tags) <= 10),
    language   varchar(10)  NOT NULL DEFAULT 'vi',
    is_mature  boolean      NOT NULL DEFAULT false,
    updated_at timestamptz  NOT NULL DEFAULT now()
);
