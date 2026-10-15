-- Set when an admin takes the stream down: a VOD that arrives later is created DELETED (BR-F23).
ALTER TABLE streams ADD COLUMN taken_down_at timestamptz;
ALTER TABLE streams ADD CONSTRAINT streams_taken_down CHECK (taken_down_at IS NULL OR status = 'ENDED');

-- channel.suspended / channel.restored: a suspended channel cannot go live, open playback or change Studio settings.
ALTER TABLE creator_channels ADD COLUMN suspended boolean NOT NULL DEFAULT false;
