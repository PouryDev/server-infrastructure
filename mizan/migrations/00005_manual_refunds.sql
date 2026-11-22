-- +goose Up
-- Manual refunds: an agent pays the payer card-to-card outside the system and records the transfer. Only the
-- masked destination card and the bank's tracking number are stored: never a full card number or a CVV.
ALTER TABLE mz_refunds
    ADD COLUMN method                  text NOT NULL DEFAULT 'gateway',
    ADD COLUMN destination_card_masked text NOT NULL DEFAULT '',
    ADD COLUMN tracking_number         text NOT NULL DEFAULT '',
    ADD COLUMN paid_at                 timestamptz,
    ADD COLUMN claimed_by              text NOT NULL DEFAULT '',
    ADD COLUMN claimed_at              timestamptz,
    ADD COLUMN note                    text NOT NULL DEFAULT '';

-- Until now a refund the gateway could not make stayed pending and was finished by hand, so every refund that
-- is still pending is a manual one. Finished (done/failed) refunds keep method 'gateway': nothing about them changes.
UPDATE mz_refunds SET method = 'manual_card_to_card' WHERE status = 'pending';

ALTER TABLE mz_refunds DROP CONSTRAINT mz_refunds_status_check;
ALTER TABLE mz_refunds
    ADD CONSTRAINT mz_refunds_status_check CHECK (status IN ('pending', 'processing', 'done', 'failed', 'cancelled')),
    ADD CONSTRAINT mz_refunds_method_check CHECK (method IN ('gateway', 'manual_card_to_card')),
    -- first 6 and last 4 digits at most, 16 characters in all
    ADD CONSTRAINT mz_refunds_card_masked_check CHECK (
        destination_card_masked = '' OR (length(destination_card_masked) = 16 AND destination_card_masked ~ '^[0-9]{0,6}[*]{6,16}[0-9]{0,4}$')),
    ADD CONSTRAINT mz_refunds_processing_claimed_check CHECK (status <> 'processing' OR (claimed_by <> '' AND claimed_at IS NOT NULL)),
    -- a finished manual refund is only valid with the proof of the transfer
    ADD CONSTRAINT mz_refunds_manual_done_check CHECK (
        NOT (method = 'manual_card_to_card' AND status = 'done')
        OR (tracking_number <> '' AND paid_at IS NOT NULL AND destination_card_masked <> ''));

-- One bank transfer can prove one refund only.
CREATE UNIQUE INDEX mz_refunds_tracking ON mz_refunds (method, tracking_number) WHERE tracking_number <> '';
-- The agents' queue, oldest first.
CREATE INDEX mz_refunds_queue ON mz_refunds (created_at, id) WHERE status IN ('pending', 'processing');

-- +goose Down
DROP INDEX mz_refunds_queue;
DROP INDEX mz_refunds_tracking;
ALTER TABLE mz_refunds
    DROP CONSTRAINT mz_refunds_manual_done_check, DROP CONSTRAINT mz_refunds_processing_claimed_check,
    DROP CONSTRAINT mz_refunds_card_masked_check, DROP CONSTRAINT mz_refunds_method_check, DROP CONSTRAINT mz_refunds_status_check;
UPDATE mz_refunds SET status = 'failed' WHERE status IN ('processing', 'cancelled');
ALTER TABLE mz_refunds
    ADD CONSTRAINT mz_refunds_status_check CHECK (status IN ('pending', 'done', 'failed')),
    DROP COLUMN note, DROP COLUMN claimed_at, DROP COLUMN claimed_by, DROP COLUMN paid_at,
    DROP COLUMN tracking_number, DROP COLUMN destination_card_masked, DROP COLUMN method;
