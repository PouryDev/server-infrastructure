-- +goose Up
-- Cheques: a received cheque may be endorsed to a supplier (status 'endorsed'), and a bounced one is presented again (the existing statuses are reused).
ALTER TABLE mz_cheques DROP CONSTRAINT mz_cheques_status_check;
ALTER TABLE mz_cheques ADD CONSTRAINT mz_cheques_status_check CHECK (
    (direction = 'receivable' AND status IN ('in_hand', 'deposited', 'cleared', 'bounced', 'returned', 'endorsed'))
    OR (direction = 'payable' AND status IN ('issued', 'cleared', 'bounced', 'returned')));
ALTER TABLE mz_cheques DROP CONSTRAINT mz_cheques_bank_check;
ALTER TABLE mz_cheques ADD CONSTRAINT mz_cheques_bank_check CHECK (
    (direction = 'payable' AND bank_account_id IS NOT NULL)
    OR (direction = 'receivable' AND (status IN ('in_hand', 'returned', 'endorsed') OR bank_account_id IS NOT NULL)));

-- +goose Down
ALTER TABLE mz_cheques DROP CONSTRAINT mz_cheques_bank_check;
ALTER TABLE mz_cheques ADD CONSTRAINT mz_cheques_bank_check CHECK (
    (direction = 'payable' AND bank_account_id IS NOT NULL)
    OR (direction = 'receivable' AND (status IN ('in_hand', 'returned') OR bank_account_id IS NOT NULL)));
ALTER TABLE mz_cheques DROP CONSTRAINT mz_cheques_status_check;
ALTER TABLE mz_cheques ADD CONSTRAINT mz_cheques_status_check CHECK (
    (direction = 'receivable' AND status IN ('in_hand', 'deposited', 'cleared', 'bounced', 'returned'))
    OR (direction = 'payable' AND status IN ('issued', 'cleared', 'bounced', 'returned')));
