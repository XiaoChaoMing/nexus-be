-- OBS overlay look of a channel (chat theme and gift alert). Validated by chat-svc, never by the database:
-- the allowed values change with the product, a CHECK would need a migration each time.
CREATE TABLE overlay_settings (
    channel_id uuid        PRIMARY KEY,
    chat       jsonb       NOT NULL,
    alert      jsonb       NOT NULL,
    updated_by uuid,
    updated_at timestamptz NOT NULL DEFAULT now()
);
