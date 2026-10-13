CREATE TABLE subscription_tiers (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    channel_id uuid        NOT NULL,
    level      smallint    NOT NULL CHECK (level BETWEEN 1 AND 3),
    price_vnd  bigint      NOT NULL CHECK (price_vnd > 0),
    benefits   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    active     boolean     NOT NULL DEFAULT true,
    UNIQUE (channel_id, level)
);

CREATE TABLE subscriptions (
    id                   uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id              uuid        NOT NULL,
    channel_id           uuid        NOT NULL,
    tier_id              uuid        NOT NULL REFERENCES subscription_tiers (id),
    status               varchar(24) NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'ACTIVE', 'CANCELLED_PENDING_END', 'EXPIRED')),
    current_period_start timestamptz,
    current_period_end   timestamptz,
    auto_renew           boolean     NOT NULL DEFAULT true,
    gifted_by            uuid,
    cancelled_at         timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    CHECK (status = 'PENDING' OR (current_period_start IS NOT NULL AND current_period_end > current_period_start))
);

-- One live subscription per viewer and channel; a new one can only start after the old one expires.
CREATE UNIQUE INDEX subscriptions_one_live_idx ON subscriptions (user_id, channel_id)
    WHERE status IN ('ACTIVE', 'CANCELLED_PENDING_END');
CREATE INDEX subscriptions_renewal_idx ON subscriptions (current_period_end) WHERE status = 'ACTIVE' AND auto_renew;
CREATE INDEX subscriptions_channel_idx ON subscriptions (channel_id) WHERE status IN ('ACTIVE', 'CANCELLED_PENDING_END');

CREATE TABLE checkout_orders (
    id              uuid         PRIMARY KEY DEFAULT uuidv7(),
    user_id         uuid         NOT NULL,
    tier_id         uuid         NOT NULL REFERENCES subscription_tiers (id),
    subscription_id uuid         REFERENCES subscriptions (id),
    amount_vnd      bigint       NOT NULL CHECK (amount_vnd > 0),
    idempotency_key varchar(64)  NOT NULL UNIQUE,
    psp             varchar(20)  NOT NULL,
    psp_ref         varchar(100) UNIQUE,
    payment_url     text,
    status          varchar(10)  NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PAID', 'FAILED', 'EXPIRED')),
    expires_at      timestamptz  NOT NULL,
    created_at      timestamptz  NOT NULL DEFAULT now(),
    paid_at         timestamptz,
    CHECK ((status = 'PAID') = (paid_at IS NOT NULL))
);

CREATE INDEX checkout_orders_pending_idx ON checkout_orders (expires_at) WHERE status = 'PENDING';
CREATE INDEX checkout_orders_user_idx ON checkout_orders (user_id, created_at DESC);

CREATE TABLE transactions (
    id          uuid         PRIMARY KEY DEFAULT uuidv7(),
    order_id    uuid         NOT NULL REFERENCES checkout_orders (id),
    type        varchar(10)  NOT NULL CHECK (type IN ('CHARGE', 'REFUND')),
    amount_vnd  bigint       NOT NULL CHECK (amount_vnd > 0),
    psp_ref     varchar(100) NOT NULL,
    raw_webhook jsonb        NOT NULL,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    -- A redelivered PSP webhook hits this constraint instead of recording the payment twice.
    UNIQUE (psp_ref, type)
);

CREATE TABLE payout_requests (
    id                  uuid        PRIMARY KEY DEFAULT uuidv7(),
    channel_id          uuid        NOT NULL,
    amount_vnd          bigint      NOT NULL CHECK (amount_vnd > 0),
    bank_info_encrypted bytea       NOT NULL,
    status              varchar(10) NOT NULL DEFAULT 'REQUESTED' CHECK (status IN ('REQUESTED', 'APPROVED', 'REJECTED')),
    reviewed_by         uuid,
    reviewed_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CHECK (status = 'REQUESTED' OR (reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL))
);

CREATE INDEX payout_requests_channel_idx ON payout_requests (channel_id, created_at DESC);

CREATE TABLE ledger_entries (
    id                uuid        PRIMARY KEY DEFAULT uuidv7(),
    channel_id        uuid        NOT NULL,
    transaction_id    uuid        REFERENCES transactions (id),
    payout_request_id uuid        REFERENCES payout_requests (id),
    direction         varchar(6)  NOT NULL CHECK (direction IN ('CREDIT', 'DEBIT')),
    kind              varchar(16) NOT NULL CHECK (kind IN ('SUB_REVENUE', 'PLATFORM_FEE', 'PAYOUT')),
    amount_vnd        bigint      NOT NULL CHECK (amount_vnd > 0),
    created_at        timestamptz NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(transaction_id, payout_request_id) = 1)
);

CREATE INDEX ledger_entries_channel_idx ON ledger_entries (channel_id, created_at);

-- Financial records are append-only; corrections are new entries.
CREATE FUNCTION forbid_update_delete() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME;
END;
$$;

CREATE TRIGGER transactions_append_only BEFORE UPDATE OR DELETE ON transactions
    FOR EACH ROW EXECUTE FUNCTION forbid_update_delete();
CREATE TRIGGER ledger_entries_append_only BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION forbid_update_delete();
