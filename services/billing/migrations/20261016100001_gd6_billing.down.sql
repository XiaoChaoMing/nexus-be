TRUNCATE ledger_entries, transactions, payout_requests, checkout_orders, subscriptions, subscription_tiers;
DROP TABLE idempotency_keys;
DROP INDEX ledger_entries_transaction_idx;
DROP INDEX payout_requests_status_idx, payout_requests_one_open_idx;
ALTER TABLE payout_requests DROP CONSTRAINT payout_requests_reject_reason, DROP CONSTRAINT payout_requests_number_key,
    DROP COLUMN reject_reason, DROP COLUMN account_last4, DROP COLUMN bank_code, DROP COLUMN number;
DROP INDEX subscriptions_user_idx, subscriptions_expiry_idx;
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_expired_reason, DROP COLUMN updated_at, DROP COLUMN expired_reason,
    DROP COLUMN expiring_notified_for;
DROP INDEX transactions_order_idx;
ALTER TABLE transactions DROP COLUMN source, DROP CONSTRAINT transactions_type_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_type_check CHECK (type IN ('CHARGE', 'REFUND'));
DROP INDEX checkout_orders_invoice_prefix_idx, checkout_orders_paid_idx, checkout_orders_review_idx,
    checkout_orders_one_pending_idx;
ALTER TABLE checkout_orders DROP CONSTRAINT checkout_orders_failure, DROP CONSTRAINT checkout_orders_paid_at,
    DROP CONSTRAINT checkout_orders_status_check, DROP CONSTRAINT checkout_orders_psp_check,
    DROP CONSTRAINT checkout_orders_invoice_key;
ALTER TABLE checkout_orders ADD CONSTRAINT checkout_orders_status_check
    CHECK (status IN ('PENDING', 'PAID', 'FAILED', 'EXPIRED'));
ALTER TABLE checkout_orders ADD CONSTRAINT checkout_orders_check CHECK ((status = 'PAID') = (paid_at IS NOT NULL));
ALTER TABLE checkout_orders DROP COLUMN updated_at, DROP COLUMN needs_review, DROP COLUMN failure_reason,
    DROP COLUMN payment_method, DROP COLUMN invoice_number, DROP COLUMN period_days, DROP COLUMN sub_action,
    DROP COLUMN channel_id;
ALTER TABLE subscription_tiers DROP COLUMN updated_at;
DROP TABLE channels;
