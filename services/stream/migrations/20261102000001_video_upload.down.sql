-- Removes every uploaded video (and the FAILED status) so the old constraints hold again.
DELETE FROM playback_sessions WHERE vod_id IN (SELECT id FROM vods WHERE source = 'UPLOAD' OR status = 'FAILED');
DELETE FROM watch_progress WHERE vod_id IN (SELECT id FROM vods WHERE source = 'UPLOAD' OR status = 'FAILED');
DROP TABLE video_uploads;
DELETE FROM vods WHERE source = 'UPLOAD' OR status = 'FAILED';

DROP INDEX vods_processing_idx;
DROP INDEX vods_studio_idx;
CREATE INDEX vods_studio_idx ON vods (channel_id, created_at DESC, id DESC) WHERE status IN ('READY', 'EXPIRED');

ALTER TABLE vods DROP CONSTRAINT vods_status_check;
ALTER TABLE vods ADD CONSTRAINT vods_status_check CHECK (status IN ('PROCESSING', 'READY', 'EXPIRED', 'DELETED'));
ALTER TABLE vods DROP CONSTRAINT vods_live_expiry;
ALTER TABLE vods DROP CONSTRAINT vods_source_stream;
ALTER TABLE vods
    DROP COLUMN processing_stage,
    DROP COLUMN processing_percent,
    DROP COLUMN failure_retryable,
    DROP COLUMN failure_reason,
    DROP COLUMN published_at,
    DROP COLUMN description,
    DROP COLUMN source;
ALTER TABLE vods ALTER COLUMN stream_id SET NOT NULL, ALTER COLUMN expires_at SET NOT NULL;
