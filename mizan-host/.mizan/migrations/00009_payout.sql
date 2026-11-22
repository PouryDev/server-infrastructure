-- +goose Up
-- Marketplace payout: sellers, versioned commission/hold terms, sales with their commission, and idempotent payout batches.
CREATE TABLE mz_po_sellers (
    id           uuid PRIMARY KEY,
    code         text NOT NULL UNIQUE,
    name         text NOT NULL,
    sheba        text NOT NULL DEFAULT '' CHECK (sheba = '' OR sheba ~ '^IR[0-9]{24}$'),
    status       text NOT NULL CHECK (status IN ('active', 'blocked')),
    block_reason text NOT NULL DEFAULT '',
    account_code text NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Terms are data: a rate and a hold period valid from a day, for everyone (default), a product category or one seller.
-- A seller's terms win over the category's, which win over the default. Versions are never updated.
CREATE TABLE mz_po_terms (
    id             uuid PRIMARY KEY,
    name           text NOT NULL,
    scope          text NOT NULL CHECK (scope IN ('default', 'category', 'seller')),
    scope_key      text NOT NULL DEFAULT '',
    effective_from date NOT NULL,
    rate_bp        int NOT NULL CHECK (rate_bp BETWEEN 0 AND 10000),
    hold_days      int NOT NULL CHECK (hold_days BETWEEN 0 AND 365),
    vat_bp         int CHECK (vat_bp IS NULL OR vat_bp BETWEEN 0 AND 10000), -- VAT on the commission; NULL = the accounting setting (mizan.Config.VATBasisPoints)
    source         text NOT NULL DEFAULT '',
    created_by     text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK ((scope = 'default') = (scope_key = '')),
    UNIQUE (scope, scope_key, effective_from)
);

CREATE TABLE mz_po_batches (
    id         uuid PRIMARY KEY,
    idem_key   text NOT NULL UNIQUE,
    pay_from   text NOT NULL,
    pay_date   date NOT NULL,
    currency   text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    total      bigint NOT NULL DEFAULT 0,
    sale_count int NOT NULL DEFAULT 0,
    journal_id uuid,
    actor      text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mz_po_batch_items (
    batch_id   uuid NOT NULL REFERENCES mz_po_batches (id),
    seller_id  uuid NOT NULL REFERENCES mz_po_sellers (id),
    sheba      text NOT NULL DEFAULT '', -- snapshot of the destination at payout time
    amount     bigint NOT NULL CHECK (amount > 0),
    sale_count int NOT NULL,
    PRIMARY KEY (batch_id, seller_id)
);

CREATE TABLE mz_po_sales (
    id             uuid PRIMARY KEY,
    idem_key       text NOT NULL UNIQUE,
    seller_id      uuid NOT NULL REFERENCES mz_po_sellers (id),
    terms_id       uuid NOT NULL REFERENCES mz_po_terms (id), -- restricts deleting terms that were used
    reference      text NOT NULL DEFAULT '',
    category       text NOT NULL DEFAULT '',
    currency       text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    gross          bigint NOT NULL CHECK (gross > 0),
    commission     bigint NOT NULL CHECK (commission >= 0),
    vat            bigint NOT NULL DEFAULT 0 CHECK (vat >= 0), -- VAT on the commission, deducted from the seller
    net            bigint NOT NULL CHECK (net >= 0),
    rate_bp        int NOT NULL,
    vat_bp         int NOT NULL DEFAULT 0,
    hold_days      int NOT NULL,
    status         text NOT NULL CHECK (status IN ('held', 'paid', 'returned')),
    sale_date      date NOT NULL,
    delivered_at   date,
    release_at     date,
    journal_id     uuid,
    return_journal uuid,
    return_reason  text NOT NULL DEFAULT '',
    batch_id       uuid REFERENCES mz_po_batches (id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (gross = commission + vat + net),
    CHECK ((status = 'paid') = (batch_id IS NOT NULL))
);
CREATE INDEX mz_po_sales_seller ON mz_po_sales (seller_id, status);
CREATE INDEX mz_po_sales_due ON mz_po_sales (release_at) WHERE status = 'held';

-- Starting value, chosen by the owner (not a legal figure): 10% commission, seller's money released 7 days after delivery, VAT on the
-- commission follows the accounting setting. It is a row like any other: add a newer version (POST /v1/payout/terms) to change anything.
INSERT INTO mz_po_terms (id, name, scope, scope_key, effective_from, rate_bp, hold_days, vat_bp, source, created_by)
VALUES ('00000000-0000-4000-8000-000000001405', 'Default 1405', 'default', '', DATE '2026-03-21', 1000, 7, NULL,
        'starting value chosen by the owner: 10% commission, 7 days hold after delivery; replace with a new version', 'migration');

-- +goose Down
DROP TABLE mz_po_sales, mz_po_batch_items, mz_po_batches, mz_po_terms, mz_po_sellers;
