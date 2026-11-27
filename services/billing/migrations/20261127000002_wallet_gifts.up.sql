-- Wallet top-ups and virtual gifts (DL-24): a checkout order is a subscription (GĐ6) or a wallet top-up.
ALTER TABLE checkout_orders
    ADD COLUMN order_type varchar(12) NOT NULL DEFAULT 'SUBSCRIPTION' CHECK (order_type IN ('SUBSCRIPTION', 'TOPUP')),
    ALTER COLUMN channel_id DROP NOT NULL,
    ALTER COLUMN tier_id DROP NOT NULL,
    ALTER COLUMN sub_action DROP NOT NULL;
ALTER TABLE checkout_orders ADD CONSTRAINT checkout_orders_type_fields CHECK ((order_type = 'SUBSCRIPTION') =
    (channel_id IS NOT NULL AND tier_id IS NOT NULL AND sub_action IS NOT NULL));
-- checkout_orders_one_pending_idx (user_id, channel_id) skips top-ups (channel NULL): one pending top-up per user.
CREATE UNIQUE INDEX checkout_orders_one_pending_topup_idx ON checkout_orders (user_id)
    WHERE status = 'PENDING' AND order_type = 'TOPUP';

CREATE TABLE gift_items (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    name       varchar(40) NOT NULL CHECK (char_length(name) BETWEEN 1 AND 40),
    price_vnd  bigint      NOT NULL CHECK (price_vnd BETWEEN 1000 AND 5000000),
    icon       varchar(16) NOT NULL,
    active     boolean     NOT NULL DEFAULT true,
    sort_order integer     NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX gift_items_active_idx ON gift_items (sort_order, created_at) WHERE active;

-- One gift; the item is copied so later edits of the catalog do not change history.
CREATE TABLE gift_sends (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id        uuid        NOT NULL,
    channel_id     uuid        NOT NULL,
    gift_item_id   uuid        NOT NULL REFERENCES gift_items (id),
    item_name      varchar(40) NOT NULL,
    icon           varchar(16) NOT NULL,
    unit_price_vnd bigint      NOT NULL CHECK (unit_price_vnd > 0),
    quantity       smallint    NOT NULL CHECK (quantity BETWEEN 1 AND 99),
    total_vnd      bigint      NOT NULL CHECK (total_vnd = unit_price_vnd * quantity),
    fee_vnd        bigint      NOT NULL CHECK (fee_vnd BETWEEN 0 AND total_vnd),
    message        varchar(200),
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX gift_sends_channel_idx ON gift_sends (channel_id, created_at DESC);
CREATE INDEX gift_sends_user_idx ON gift_sends (user_id, created_at DESC);

CREATE TABLE wallet_entries (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id        uuid        NOT NULL,
    direction      varchar(6)  NOT NULL CHECK (direction IN ('CREDIT', 'DEBIT')),
    kind           varchar(12) NOT NULL CHECK (kind IN ('TOPUP', 'TOPUP_VOID', 'GIFT_SPEND')),
    amount_vnd     bigint      NOT NULL CHECK (amount_vnd > 0),
    transaction_id uuid        REFERENCES transactions (id),
    gift_send_id   uuid        REFERENCES gift_sends (id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(transaction_id, gift_send_id) = 1)
);
CREATE INDEX wallet_entries_user_idx ON wallet_entries (user_id, created_at DESC, id DESC);
-- A redelivered payment or a retried gift cannot move the wallet twice.
CREATE UNIQUE INDEX wallet_entries_source_idx ON wallet_entries (COALESCE(transaction_id, gift_send_id), kind);

ALTER TABLE ledger_entries ADD COLUMN gift_send_id uuid REFERENCES gift_sends (id);
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_source
    CHECK (num_nonnulls(transaction_id, payout_request_id, gift_send_id) = 1);
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('SUB_REVENUE', 'PLATFORM_FEE', 'PAYOUT_HOLD', 'PAYOUT_REVERSAL', 'GIFT_REVENUE'));

CREATE TRIGGER wallet_entries_append_only BEFORE UPDATE OR DELETE ON wallet_entries
    FOR EACH ROW EXECUTE FUNCTION forbid_update_delete();
CREATE TRIGGER gift_sends_append_only BEFORE UPDATE OR DELETE ON gift_sends
    FOR EACH ROW EXECUTE FUNCTION forbid_update_delete();
