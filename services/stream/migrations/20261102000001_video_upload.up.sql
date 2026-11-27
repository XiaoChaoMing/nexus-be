-- Uploaded videos: a VOD without a stream (source UPLOAD) that never expires, and the upload sessions that feed it.
ALTER TABLE vods ALTER COLUMN stream_id DROP NOT NULL, ALTER COLUMN expires_at DROP NOT NULL;
ALTER TABLE vods
    ADD COLUMN source             varchar(8)  NOT NULL DEFAULT 'LIVE' CHECK (source IN ('LIVE', 'UPLOAD')),
    ADD COLUMN description        text        NOT NULL DEFAULT '',
    ADD COLUMN published_at       timestamptz,
    ADD COLUMN failure_reason     varchar(32),
    ADD COLUMN failure_retryable  boolean     NOT NULL DEFAULT false,
    ADD COLUMN processing_percent smallint    NOT NULL DEFAULT 0 CHECK (processing_percent BETWEEN 0 AND 100),
    ADD COLUMN processing_stage   varchar(12);
ALTER TABLE vods ADD CONSTRAINT vods_source_stream CHECK ((source = 'LIVE') = (stream_id IS NOT NULL));
ALTER TABLE vods ADD CONSTRAINT vods_live_expiry CHECK (source = 'UPLOAD' OR expires_at IS NOT NULL);
-- vods_status_check is the name Postgres generated for the inline CHECK on status in the init migration.
ALTER TABLE vods DROP CONSTRAINT vods_status_check;
ALTER TABLE vods ADD CONSTRAINT vods_status_check
    CHECK (status IN ('PROCESSING', 'READY', 'EXPIRED', 'DELETED', 'FAILED'));
UPDATE vods SET published_at = created_at WHERE status <> 'PROCESSING';
DROP INDEX vods_studio_idx;
CREATE INDEX vods_studio_idx ON vods (channel_id, created_at DESC, id DESC)
    WHERE status IN ('READY', 'EXPIRED', 'PROCESSING', 'FAILED');
CREATE INDEX vods_processing_idx ON vods (channel_id) WHERE status IN ('PROCESSING', 'FAILED');

CREATE TABLE video_uploads (
    id                uuid         PRIMARY KEY DEFAULT uuidv7(),
    channel_id        uuid         NOT NULL,
    user_id           uuid         NOT NULL,
    vod_id            uuid         REFERENCES vods (id),
    file_name         varchar(255) NOT NULL,
    content_type      varchar(100) NOT NULL,
    size_bytes        bigint       NOT NULL CHECK (size_bytes > 0),
    part_size         integer      NOT NULL CHECK (part_size > 0),
    part_count        integer      NOT NULL CHECK (part_count BETWEEN 1 AND 10000),
    s3_upload_id      text         NOT NULL,
    object_key        text         NOT NULL,
    title             varchar(140) NOT NULL,
    description       text         NOT NULL DEFAULT '',
    category_id       uuid,
    is_mature         boolean      NOT NULL DEFAULT false,
    visibility        varchar(10)  NOT NULL CHECK (visibility IN ('PUBLIC', 'HIDDEN')),
    status            varchar(10)  NOT NULL DEFAULT 'UPLOADING'
                                   CHECK (status IN ('UPLOADING', 'COMPLETED', 'ABORTED', 'EXPIRED')),
    -- {"1": {"etag": "…", "size": 8388608}, …}
    parts             jsonb        NOT NULL DEFAULT '{}',
    source_deleted_at timestamptz,
    created_at        timestamptz  NOT NULL DEFAULT now(),
    updated_at        timestamptz  NOT NULL DEFAULT now(),
    expires_at        timestamptz  NOT NULL,
    CHECK ((status = 'COMPLETED') = (vod_id IS NOT NULL))
);
CREATE INDEX video_uploads_channel_idx ON video_uploads (channel_id, created_at DESC);
CREATE INDEX video_uploads_expiry_idx ON video_uploads (expires_at) WHERE status = 'UPLOADING';
CREATE UNIQUE INDEX video_uploads_vod_idx ON video_uploads (vod_id) WHERE vod_id IS NOT NULL;
