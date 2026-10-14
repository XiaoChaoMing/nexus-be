ALTER TABLE stream_settings ADD COLUMN category_id uuid;

ALTER TABLE streams ADD CONSTRAINT streams_scheduled_fields
    CHECK (status <> 'SCHEDULED' OR (schedule_id IS NOT NULL AND scheduled_start_at IS NOT NULL));
-- BR-G2-16: one live row per occurrence, so a redelivered occurrence_upcoming is a no-op. A CANCELLED row
-- does not count: when a series is edited back to an instant it had, the occurrence is scheduled again.
CREATE UNIQUE INDEX streams_schedule_occurrence_idx ON streams (schedule_id, scheduled_start_at)
    WHERE schedule_id IS NOT NULL AND status <> 'CANCELLED';
CREATE INDEX streams_scheduled_idx ON streams (channel_id, scheduled_start_at) WHERE status = 'SCHEDULED';
CREATE INDEX streams_scheduled_due_idx ON streams (scheduled_start_at) WHERE status = 'SCHEDULED';
CREATE INDEX streams_on_air_category_idx ON streams (category_id) WHERE status IN ('LIVE', 'INTERRUPTED');
CREATE INDEX streams_channel_started_idx ON streams (channel_id, started_at DESC) WHERE status = 'ENDED';

ALTER TABLE vods
    ADD COLUMN category_id uuid,
    ADD COLUMN is_mature   boolean     NOT NULL DEFAULT false,
    ADD COLUMN updated_at  timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN deleted_at  timestamptz;
UPDATE vods v SET category_id = s.category_id, is_mature = s.is_mature FROM streams s WHERE s.id = v.stream_id;
ALTER TABLE vods ADD CONSTRAINT vods_deleted_at CHECK ((status = 'DELETED') = (deleted_at IS NOT NULL));
CREATE INDEX vods_public_idx ON vods (created_at DESC, id DESC) WHERE status = 'READY' AND visibility = 'PUBLIC';
CREATE INDEX vods_public_category_idx ON vods (category_id, created_at DESC, id DESC) WHERE status = 'READY' AND visibility = 'PUBLIC';
CREATE INDEX vods_studio_idx ON vods (channel_id, created_at DESC, id DESC) WHERE status IN ('READY', 'EXPIRED');
