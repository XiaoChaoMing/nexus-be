-- Payout lifecycle REQUESTED → APPROVED → PROCESSING → PAID (REJECTED / FAILED exits) and the two-phase ledger:
-- DEBIT PAYOUT_HOLD at request, CREDIT PAYOUT_REVERSAL on REJECTED / FAILED, nothing on PAID.
ALTER TABLE payout_requests DROP CONSTRAINT payout_requests_status_check;
ALTER TABLE payout_requests ADD CONSTRAINT payout_requests_status_check
    CHECK (status IN ('REQUESTED', 'APPROVED', 'PROCESSING', 'PAID', 'REJECTED', 'FAILED'));
ALTER TABLE payout_requests
    ADD COLUMN provider       varchar(16) CHECK (provider IN ('MANUAL')),
    ADD COLUMN provider_ref   varchar(64),
    ADD COLUMN paid_at        timestamptz,
    ADD COLUMN settled_by     uuid,
    ADD COLUMN settled_at     timestamptz,
    ADD COLUMN failure_reason varchar(500);

-- An APPROVED payout of the old model was already debited: it is a PAID one.
UPDATE payout_requests SET status = 'PAID', provider = 'MANUAL', provider_ref = 'LEGACY', paid_at = reviewed_at,
    settled_by = reviewed_by, settled_at = reviewed_at
WHERE status = 'APPROVED';

-- provider_ref is required on PAID but allowed earlier: an asynchronous provider may return it at PROCESSING.
ALTER TABLE payout_requests
    ADD CONSTRAINT payout_requests_paid CHECK ((status = 'PAID') = (paid_at IS NOT NULL)
        AND (status <> 'PAID' OR (provider_ref IS NOT NULL AND provider IS NOT NULL))),
    ADD CONSTRAINT payout_requests_processing CHECK (status <> 'PROCESSING' OR provider IS NOT NULL),
    ADD CONSTRAINT payout_requests_failure CHECK ((status = 'FAILED') = (failure_reason IS NOT NULL)
        AND (failure_reason IS NULL OR char_length(failure_reason) >= 10)),
    ADD CONSTRAINT payout_requests_settled CHECK ((status IN ('PAID', 'FAILED')) = (settled_at IS NOT NULL)
        AND (settled_at IS NULL) = (settled_by IS NULL));

-- BR-H39: one open request per channel.
DROP INDEX payout_requests_one_open_idx;
CREATE UNIQUE INDEX payout_requests_one_open_idx ON payout_requests (channel_id)
    WHERE status IN ('REQUESTED', 'APPROVED', 'PROCESSING');

-- PAYOUT becomes PAYOUT_HOLD (same row, same sign). The append-only trigger is lifted for this one statement.
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only;
UPDATE ledger_entries SET kind = 'PAYOUT_HOLD' WHERE kind = 'PAYOUT';
ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('SUB_REVENUE', 'PLATFORM_FEE', 'PAYOUT_HOLD', 'PAYOUT_REVERSAL'));

-- A REQUESTED payout of the old model had no ledger row (the balance subtracted it as pending).
INSERT INTO ledger_entries (channel_id, payout_request_id, direction, kind, amount_vnd, created_at)
SELECT channel_id, id, 'DEBIT', 'PAYOUT_HOLD', amount_vnd, created_at FROM payout_requests WHERE status = 'REQUESTED';

-- At most one hold and one reversal per payout, even under a race.
CREATE UNIQUE INDEX ledger_entries_payout_kind_idx ON ledger_entries (payout_request_id, kind)
    WHERE payout_request_id IS NOT NULL;
