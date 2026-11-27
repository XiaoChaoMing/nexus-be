-- Back to the GĐ6 model: APPROVED = debited (PAYOUT), REQUESTED = subtracted as pending, no hold / reversal rows.
DROP INDEX ledger_entries_payout_kind_idx;
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only;
DELETE FROM ledger_entries l USING payout_requests p
WHERE l.payout_request_id = p.id
    AND (l.kind = 'PAYOUT_REVERSAL' OR p.status IN ('REQUESTED', 'REJECTED', 'FAILED'));
UPDATE ledger_entries SET kind = 'PAYOUT' WHERE kind = 'PAYOUT_HOLD';
ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('SUB_REVENUE', 'PLATFORM_FEE', 'PAYOUT'));

DROP INDEX payout_requests_one_open_idx;
ALTER TABLE payout_requests DROP CONSTRAINT payout_requests_settled, DROP CONSTRAINT payout_requests_failure,
    DROP CONSTRAINT payout_requests_processing, DROP CONSTRAINT payout_requests_paid,
    DROP CONSTRAINT payout_requests_status_check;
UPDATE payout_requests SET status = 'APPROVED' WHERE status IN ('PROCESSING', 'PAID');
UPDATE payout_requests SET status = 'REJECTED', reject_reason = failure_reason WHERE status = 'FAILED';
ALTER TABLE payout_requests DROP COLUMN failure_reason, DROP COLUMN settled_at, DROP COLUMN settled_by,
    DROP COLUMN paid_at, DROP COLUMN provider_ref, DROP COLUMN provider;
ALTER TABLE payout_requests ADD CONSTRAINT payout_requests_status_check
    CHECK (status IN ('REQUESTED', 'APPROVED', 'REJECTED'));
CREATE UNIQUE INDEX payout_requests_one_open_idx ON payout_requests (channel_id) WHERE status = 'REQUESTED';
