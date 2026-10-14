ALTER TABLE channels
    ADD COLUMN follower_count    integer     NOT NULL DEFAULT 0 CHECK (follower_count >= 0),
    ADD COLUMN last_live_at      timestamptz,
    ADD COLUMN handle_changed_at timestamptz;
UPDATE channels SET social_links = '[]'::jsonb WHERE social_links = '{}'::jsonb;
ALTER TABLE channels ALTER COLUMN social_links SET DEFAULT '[]'::jsonb;
ALTER TABLE channels ADD CONSTRAINT channels_social_links_array CHECK (jsonb_typeof(social_links) = 'array');
UPDATE channels c SET follower_count = (SELECT count(*) FROM follows f WHERE f.channel_id = c.id);

ALTER TABLE follows ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX follows_user_idx ON follows (user_id, created_at DESC);

ALTER TABLE categories
    ADD COLUMN position smallint NOT NULL DEFAULT 0,
    ADD COLUMN tags     text[]   NOT NULL DEFAULT '{}';

ALTER TABLE schedules
    ADD COLUMN timezone                varchar(64)   NOT NULL DEFAULT 'Asia/Ho_Chi_Minh',
    ADD COLUMN exdates                 timestamptz[] NOT NULL DEFAULT '{}',
    ADD COLUMN parent_id               uuid          REFERENCES schedules (id) ON DELETE SET NULL,
    -- NULL = open-ended weekly series; used by SCHEDULE_MAX_ACTIVE (a past one-off no longer counts).
    ADD COLUMN last_occurrence_at      timestamptz,
    -- Occurrences up to this instant were announced (schedule.occurrence_upcoming).
    ADD COLUMN upcoming_notified_until timestamptz,
    ADD COLUMN updated_at              timestamptz   NOT NULL DEFAULT now();
ALTER TABLE schedules ADD CONSTRAINT schedules_duration_range CHECK (duration_min BETWEEN 15 AND 720);
CREATE INDEX schedules_upcoming_idx ON schedules (upcoming_notified_until NULLS FIRST) WHERE status = 'ACTIVE';

ALTER TABLE reminders ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX reminders_occurrence_idx ON reminders (schedule_id, occurrence_at);

-- Copy of stream-svc's on-air streams (stream.started / updated / ended). category_id has no foreign key:
-- stream-svc does not know categories and may hold an id that does not exist here.
CREATE TABLE live_streams (
    stream_id   uuid         PRIMARY KEY,
    channel_id  uuid         NOT NULL UNIQUE REFERENCES channels (id) ON DELETE CASCADE,
    title       varchar(140) NOT NULL,
    category_id uuid,
    tags        text[]       NOT NULL DEFAULT '{}',
    language    varchar(10)  NOT NULL DEFAULT 'vi',
    is_mature   boolean      NOT NULL DEFAULT false,
    started_at  timestamptz  NOT NULL
);
CREATE INDEX live_streams_category_idx ON live_streams (category_id);

-- stream.scheduled: which SCHEDULED stream represents an occurrence (link to the Upcoming page).
CREATE TABLE occurrence_streams (
    schedule_id   uuid        NOT NULL REFERENCES schedules (id) ON DELETE CASCADE,
    occurrence_at timestamptz NOT NULL,
    stream_id     uuid        NOT NULL UNIQUE,
    PRIMARY KEY (schedule_id, occurrence_at)
);

DROP INDEX search_documents_tsv_idx;
ALTER TABLE search_documents DROP COLUMN tsv;
ALTER TABLE search_documents
    ADD COLUMN channel_id  uuid,
    ADD COLUMN category_id uuid,
    ADD COLUMN subtitle    text,
    ADD COLUMN image_url   text,
    ADD COLUMN target      text    NOT NULL DEFAULT '',
    ADD COLUMN keywords    text    NOT NULL DEFAULT '',
    ADD COLUMN is_mature   boolean NOT NULL DEFAULT false,
    ADD COLUMN expires_at  timestamptz,
    ADD COLUMN tsv tsvector NOT NULL
        GENERATED ALWAYS AS (to_tsvector('simple', lower(immutable_unaccent(title || ' ' || keywords)))) STORED;
CREATE INDEX search_documents_tsv_idx ON search_documents USING gin (tsv);
CREATE INDEX search_documents_channel_idx ON search_documents (channel_id) WHERE channel_id IS NOT NULL;

INSERT INTO search_documents (entity_type, entity_id, title, keywords, channel_id, subtitle, image_url, target, is_live)
SELECT 'CHANNEL', id, display_name, handle::text, id, '@' || handle, avatar_url, '/' || handle, is_live
FROM channels WHERE status = 'ACTIVE'
ON CONFLICT (entity_type, entity_id) DO NOTHING;

CREATE UNIQUE INDEX search_history_user_query_idx ON search_history (user_id, lower(query));
