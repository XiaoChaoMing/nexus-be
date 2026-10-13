CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE notifications (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid        NOT NULL,
    type       varchar(20) NOT NULL
        CHECK (type IN ('CREATOR_LIVE', 'STREAM_STARTING', 'MENTION', 'REPORT_RESULT', 'SUBSCRIPTION', 'SYSTEM')),
    payload    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    link       text,
    read_at    timestamptz,
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX notifications_user_idx ON notifications (user_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX notifications_unread_idx ON notifications (user_id) WHERE read_at IS NULL AND deleted_at IS NULL;

CREATE TABLE notification_settings (
    user_id uuid        NOT NULL,
    type    varchar(20) NOT NULL
        CHECK (type IN ('CREATOR_LIVE', 'STREAM_STARTING', 'MENTION', 'REPORT_RESULT', 'SUBSCRIPTION', 'SYSTEM')),
    in_app  boolean     NOT NULL DEFAULT true,
    email   boolean     NOT NULL DEFAULT true,
    PRIMARY KEY (user_id, type)
);

CREATE TABLE email_outbox (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    to_email   citext      NOT NULL,
    template   varchar(40) NOT NULL,
    data       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status     varchar(10) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'SENT', 'FAILED')),
    attempts   smallint    NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at    timestamptz
);

CREATE INDEX email_outbox_pending_idx ON email_outbox (created_at) WHERE status = 'PENDING';
