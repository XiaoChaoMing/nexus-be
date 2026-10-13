CREATE TABLE reports (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    reporter_id uuid        NOT NULL,
    target_type varchar(10) NOT NULL CHECK (target_type IN ('USER', 'CHANNEL', 'STREAM', 'MESSAGE')),
    target_id   uuid        NOT NULL,
    reason      varchar(12) NOT NULL CHECK (reason IN ('SPAM', 'HARASSMENT', 'VIOLENCE', 'SENSITIVE', 'COPYRIGHT', 'OTHER')),
    -- Up to the last 10 chat messages, captured when the report is filed (FR-MD-05).
    evidence    jsonb       NOT NULL DEFAULT '[]'::jsonb,
    note        text,
    status      varchar(10) NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'IN_REVIEW', 'RESOLVED', 'DISMISSED')),
    assigned_to uuid,
    created_at  timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    CHECK ((status IN ('RESOLVED', 'DISMISSED')) = (resolved_at IS NOT NULL))
);

CREATE INDEX reports_queue_idx ON reports (status, created_at) WHERE status IN ('OPEN', 'IN_REVIEW');
CREATE INDEX reports_target_idx ON reports (target_type, target_id);

CREATE TABLE admin_actions (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    admin_id    uuid        NOT NULL,
    report_id   uuid        REFERENCES reports (id),
    type        varchar(20) NOT NULL CHECK (type IN ('LOCK_USER', 'UNLOCK_USER', 'SUSPEND_CHANNEL', 'TAKEDOWN_STREAM')),
    target_type varchar(10) NOT NULL CHECK (target_type IN ('USER', 'CHANNEL', 'STREAM')),
    target_id   uuid        NOT NULL,
    reason      text        NOT NULL,
    expires_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX admin_actions_target_idx ON admin_actions (target_type, target_id, created_at DESC);

CREATE TABLE audit_logs (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    actor_id       uuid,
    action         varchar(50) NOT NULL,
    resource_type  varchar(30) NOT NULL,
    resource_id    uuid,
    metadata       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    source_service varchar(20) NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_logs_resource_idx ON audit_logs (resource_type, resource_id, created_at DESC);
CREATE INDEX audit_logs_actor_idx ON audit_logs (actor_id, created_at DESC);

-- PRD NFR-BE-07: audit records are append-only.
CREATE FUNCTION audit_logs_append_only() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs is append-only';
END;
$$;

CREATE TRIGGER audit_logs_no_update_delete
    BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_append_only();
