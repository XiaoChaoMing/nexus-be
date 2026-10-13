-- Written in the same transaction as the business change; a worker publishes to NATS and sets published_at.
CREATE TABLE outbox (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    aggregate_type varchar(30) NOT NULL,
    aggregate_id   uuid        NOT NULL,
    event_type     varchar(60) NOT NULL,
    payload        jsonb       NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    published_at   timestamptz
);

CREATE INDEX outbox_unpublished_idx ON outbox (created_at) WHERE published_at IS NULL;

-- Consumers record handled events so a NATS redelivery is not processed twice.
CREATE TABLE processed_events (
    event_id     uuid        NOT NULL,
    consumer     varchar(40) NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, consumer)
);
