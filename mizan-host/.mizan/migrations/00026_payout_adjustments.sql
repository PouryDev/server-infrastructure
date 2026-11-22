-- +goose Up
-- Payout, part 2: partial returns of a sale, clawback of a seller's money after a payout, and a transfer the bank rejected.

-- A fully returned sale that was already paid leaves its batch (batch_id becomes NULL, as the existing check requires; the clawback row keeps the link).
ALTER TABLE mz_po_batch_items ADD COLUMN rejected boolean NOT NULL DEFAULT false, ADD COLUMN reject_reason text NOT NULL DEFAULT '', ADD COLUMN reject_journal uuid;

-- Every change of a sale's figures after it was booked (partial returns); the sale row holds the CURRENT figures.
CREATE TABLE mz_po_adjustments (
    key         text PRIMARY KEY,
    sale_id     uuid NOT NULL REFERENCES mz_po_sales (id),
    gross       bigint NOT NULL CHECK (gross > 0),
    commission  bigint NOT NULL,
    vat         bigint NOT NULL,
    net         bigint NOT NULL,
    reason      text NOT NULL DEFAULT '',
    journal_id  uuid,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- What a seller owes the platform after money was paid out for a sale that was then returned (in full or in part).
CREATE TABLE mz_po_clawbacks (
    id         uuid PRIMARY KEY,
    sale_id    uuid NOT NULL REFERENCES mz_po_sales (id),
    seller_id  uuid NOT NULL REFERENCES mz_po_sellers (id),
    batch_id   uuid REFERENCES mz_po_batches (id),
    ref        text NOT NULL,
    amount     bigint NOT NULL CHECK (amount > 0),
    recovered  bigint NOT NULL DEFAULT 0 CHECK (recovered >= 0 AND recovered <= amount),
    reason     text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (sale_id, ref)
);
CREATE INDEX mz_po_clawbacks_seller ON mz_po_clawbacks (seller_id) WHERE recovered < amount;

CREATE TABLE mz_po_recoveries (
    key         text PRIMARY KEY,
    clawback_id uuid NOT NULL REFERENCES mz_po_clawbacks (id),
    method      text NOT NULL CHECK (method IN ('cash', 'offset')),
    amount      bigint NOT NULL CHECK (amount > 0),
    journal_id  uuid,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE mz_po_recoveries, mz_po_clawbacks, mz_po_adjustments;
ALTER TABLE mz_po_batch_items DROP COLUMN rejected, DROP COLUMN reject_reason, DROP COLUMN reject_journal;
