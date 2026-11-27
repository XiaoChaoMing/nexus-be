-- Dev rollback: wallet, gifts and top-up orders are deleted (append-only triggers lifted for these statements only).
DROP TABLE wallet_entries;

ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only;
DELETE FROM ledger_entries WHERE gift_send_id IS NOT NULL;
ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only;
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('SUB_REVENUE', 'PLATFORM_FEE', 'PAYOUT_HOLD', 'PAYOUT_REVERSAL'));
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_source, DROP COLUMN gift_send_id;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_check CHECK (num_nonnulls(transaction_id, payout_request_id) = 1);

DROP TABLE gift_sends, gift_items;

ALTER TABLE transactions DISABLE TRIGGER transactions_append_only;
DELETE FROM transactions t USING checkout_orders o WHERE t.order_id = o.id AND o.order_type = 'TOPUP';
ALTER TABLE transactions ENABLE TRIGGER transactions_append_only;
DELETE FROM checkout_orders WHERE order_type = 'TOPUP';

DROP INDEX checkout_orders_one_pending_topup_idx;
ALTER TABLE checkout_orders DROP CONSTRAINT checkout_orders_type_fields;
ALTER TABLE checkout_orders ALTER COLUMN channel_id SET NOT NULL, ALTER COLUMN tier_id SET NOT NULL,
    ALTER COLUMN sub_action SET NOT NULL, DROP COLUMN order_type;
