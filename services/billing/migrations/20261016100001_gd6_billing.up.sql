-- Channel copy (channel.created/updated/suspended/restored + backfill-channels). BR-H35.
CREATE TABLE channels (
    id           uuid        PRIMARY KEY,
    owner_id     uuid        NOT NULL,
    handle       varchar(25) NOT NULL,
    display_name varchar(50) NOT NULL,
    avatar_url   text,
    status       varchar(10) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUSPENDED')),
    -- Occurred-at of the newest event applied; older replays do not overwrite newer data.
    synced_at    timestamptz NOT NULL
);
CREATE INDEX channels_owner_idx ON channels (owner_id);

ALTER TABLE subscription_tiers ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE checkout_orders
    ADD COLUMN channel_id     uuid        NOT NULL,
    -- NEW / RENEW. Not called "kind": that name is kept for the order type (subscription / donation) later.
    ADD COLUMN sub_action     varchar(6)  NOT NULL CHECK (sub_action IN ('NEW', 'RENEW')),
    ADD COLUMN period_days    smallint    NOT NULL DEFAULT 30 CHECK (period_days > 0),
    ADD COLUMN invoice_number varchar(32) NOT NULL,
    ADD COLUMN payment_method varchar(24) CHECK (payment_method IN ('CARD', 'BANK_TRANSFER', 'NAPAS_BANK_TRANSFER')),
    ADD COLUMN failure_reason varchar(24) CHECK (failure_reason IN ('CANCELLED_BY_USER', 'DECLINED', 'EXPIRED',
        'AMOUNT_MISMATCH', 'PROVIDER_ERROR', 'UNKNOWN')),
    ADD COLUMN needs_review   boolean     NOT NULL DEFAULT false,
    ADD COLUMN updated_at     timestamptz NOT NULL DEFAULT now();
ALTER TABLE checkout_orders ADD CONSTRAINT checkout_orders_invoice_key UNIQUE (invoice_number);
ALTER TABLE checkout_orders ADD CONSTRAINT checkout_orders_psp_check CHECK (psp IN ('SEPAY', 'FAKE'));
ALTER TABLE checkout_orders DROP CONSTRAINT checkout_orders_status_check;
ALTER TABLE checkout_orders ADD CONSTRAINT checkout_orders_status_check
    CHECK (status IN ('PENDING', 'PAID', 'CANCELLED', 'FAILED', 'EXPIRED', 'VOIDED'));
ALTER TABLE checkout_orders DROP CONSTRAINT checkout_orders_check;
ALTER TABLE checkout_orders ADD CONSTRAINT checkout_orders_paid_at
    CHECK ((status IN ('PAID', 'VOIDED')) = (paid_at IS NOT NULL));
ALTER TABLE checkout_orders ADD CONSTRAINT checkout_orders_failure
    CHECK ((status IN ('CANCELLED', 'FAILED', 'EXPIRED')) = (failure_reason IS NOT NULL)
        AND (failure_reason IS DISTINCT FROM 'AMOUNT_MISMATCH' OR status = 'FAILED'));
-- BR-H19 / AF-H02 / AF-H03: at most one pending order per viewer and channel (race-proof).
CREATE UNIQUE INDEX checkout_orders_one_pending_idx ON checkout_orders (user_id, channel_id) WHERE status = 'PENDING';
CREATE INDEX checkout_orders_review_idx ON checkout_orders (created_at DESC, id DESC) WHERE needs_review;
CREATE INDEX checkout_orders_paid_idx ON checkout_orders (paid_at) WHERE paid_at IS NOT NULL;
CREATE INDEX checkout_orders_invoice_prefix_idx ON checkout_orders (invoice_number varchar_pattern_ops);

ALTER TABLE transactions DROP CONSTRAINT transactions_type_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_type_check CHECK (type IN ('CHARGE', 'REFUND', 'VOID'));
ALTER TABLE transactions ADD COLUMN source varchar(10) NOT NULL DEFAULT 'IPN' CHECK (source IN ('IPN', 'RECONCILE'));
CREATE INDEX transactions_order_idx ON transactions (order_id);

ALTER TABLE subscriptions
    ADD COLUMN expiring_notified_for timestamptz,
    ADD COLUMN expired_reason        varchar(16) CHECK (expired_reason IN ('PERIOD_ENDED', 'VOIDED')),
    ADD COLUMN updated_at            timestamptz NOT NULL DEFAULT now();
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_expired_reason
    CHECK ((status = 'EXPIRED') = (expired_reason IS NOT NULL));
CREATE INDEX subscriptions_expiry_idx ON subscriptions (current_period_end)
    WHERE status IN ('ACTIVE', 'CANCELLED_PENDING_END');
CREATE INDEX subscriptions_user_idx ON subscriptions (user_id, current_period_end DESC);

ALTER TABLE payout_requests
    ADD COLUMN number        bigint      GENERATED ALWAYS AS IDENTITY,
    ADD COLUMN bank_code     varchar(8)  NOT NULL CHECK (bank_code IN ('VCB', 'TCB', 'BIDV', 'VTB', 'ACB', 'MB', 'VPB',
        'TPB', 'STB', 'VIB', 'SHB', 'HDB')),
    ADD COLUMN account_last4 char(4)     NOT NULL CHECK (account_last4 ~ '^[0-9]{4}$'),
    ADD COLUMN reject_reason text;
ALTER TABLE payout_requests ADD CONSTRAINT payout_requests_number_key UNIQUE (number);
ALTER TABLE payout_requests ADD CONSTRAINT payout_requests_reject_reason
    CHECK ((status = 'REJECTED') = (reject_reason IS NOT NULL)
        AND (reject_reason IS NULL OR char_length(reject_reason) BETWEEN 10 AND 500));
-- BR-H33: one open request per channel.
CREATE UNIQUE INDEX payout_requests_one_open_idx ON payout_requests (channel_id) WHERE status = 'REQUESTED';
CREATE INDEX payout_requests_status_idx ON payout_requests (status, created_at, id);

CREATE INDEX ledger_entries_transaction_idx ON ledger_entries (transaction_id) WHERE transaction_id IS NOT NULL;

-- NXL-00 §8 / BR-H23. status_code NULL = the first request is still running.
CREATE TABLE idempotency_keys (
    user_id      uuid         NOT NULL,
    method       varchar(7)   NOT NULL,
    path         varchar(200) NOT NULL,
    key          varchar(64)  NOT NULL,
    request_hash bytea        NOT NULL,
    status_code  smallint,
    response     jsonb,
    created_at   timestamptz  NOT NULL DEFAULT now(),
    completed_at timestamptz,
    PRIMARY KEY (user_id, method, path, key),
    CHECK ((status_code IS NULL) = (completed_at IS NULL))
);
CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);
