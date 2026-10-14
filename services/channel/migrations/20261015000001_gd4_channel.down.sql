DROP INDEX IF EXISTS search_history_user_query_idx;
DELETE FROM search_documents WHERE entity_type = 'CHANNEL';
DROP INDEX IF EXISTS search_documents_channel_idx;
DROP INDEX IF EXISTS search_documents_tsv_idx;
ALTER TABLE search_documents DROP COLUMN tsv, DROP COLUMN expires_at, DROP COLUMN is_mature, DROP COLUMN keywords,
    DROP COLUMN target, DROP COLUMN image_url, DROP COLUMN subtitle, DROP COLUMN category_id, DROP COLUMN channel_id;
ALTER TABLE search_documents ADD COLUMN tsv tsvector NOT NULL
    GENERATED ALWAYS AS (to_tsvector('simple', lower(immutable_unaccent(title)))) STORED;
CREATE INDEX search_documents_tsv_idx ON search_documents USING gin (tsv);
DROP TABLE IF EXISTS occurrence_streams;
DROP TABLE IF EXISTS live_streams;
DROP INDEX IF EXISTS reminders_occurrence_idx;
ALTER TABLE reminders DROP COLUMN created_at;
DROP INDEX IF EXISTS schedules_upcoming_idx;
ALTER TABLE schedules DROP CONSTRAINT schedules_duration_range, DROP COLUMN updated_at, DROP COLUMN upcoming_notified_until,
    DROP COLUMN last_occurrence_at, DROP COLUMN parent_id, DROP COLUMN exdates, DROP COLUMN timezone;
ALTER TABLE categories DROP COLUMN tags, DROP COLUMN position;
DROP INDEX IF EXISTS follows_user_idx;
ALTER TABLE follows DROP COLUMN updated_at;
ALTER TABLE channels DROP CONSTRAINT channels_social_links_array;
ALTER TABLE channels ALTER COLUMN social_links SET DEFAULT '{}'::jsonb;
UPDATE channels SET social_links = '{}'::jsonb WHERE social_links = '[]'::jsonb;
ALTER TABLE channels DROP COLUMN handle_changed_at, DROP COLUMN last_live_at, DROP COLUMN follower_count;
