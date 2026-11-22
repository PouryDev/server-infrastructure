-- +goose Up
CREATE TABLE mz_wallets (
    id         uuid PRIMARY KEY,
    owner_ref  text NOT NULL,
    currency   text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    status     text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'frozen', 'closed')),
    account_id uuid NOT NULL UNIQUE REFERENCES mz_accounts (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_ref, currency)
);

CREATE TABLE mz_invoices (
    id             uuid PRIMARY KEY,
    idem_key       text NOT NULL UNIQUE,
    number         bigint,
    fiscal_year_id bigint REFERENCES mz_fiscal_years (id),
    status         text NOT NULL CHECK (status IN ('draft', 'issued', 'partially_paid', 'paid', 'void')),
    customer_ref   text NOT NULL,
    buyer          jsonb NOT NULL DEFAULT '{}',
    currency       text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    subtotal       bigint NOT NULL,                  -- sum of quantity * unit price
    discount_total bigint NOT NULL,
    tax_total      bigint NOT NULL,
    total          bigint NOT NULL CHECK (total >= 0),
    paid_total     bigint NOT NULL DEFAULT 0 CHECK (paid_total >= 0),
    issue_date     date,
    due_date       date,
    memo           text NOT NULL DEFAULT '',
    issue_journal  uuid,
    void_journal   uuid,
    version        int NOT NULL DEFAULT 1,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (paid_total <= total)
);
CREATE UNIQUE INDEX mz_invoices_number ON mz_invoices (fiscal_year_id, number) WHERE number IS NOT NULL;
CREATE INDEX mz_invoices_customer ON mz_invoices (customer_ref, created_at);
CREATE INDEX mz_invoices_status ON mz_invoices (status, created_at);

CREATE TABLE mz_invoice_items (
    invoice_id  uuid NOT NULL REFERENCES mz_invoices (id),
    line_no     smallint NOT NULL,
    description text NOT NULL,
    quantity    bigint NOT NULL CHECK (quantity > 0),
    unit_price  bigint NOT NULL CHECK (unit_price >= 0),
    discount    bigint NOT NULL DEFAULT 0 CHECK (discount >= 0),
    tax_bp      int NOT NULL DEFAULT 0 CHECK (tax_bp BETWEEN 0 AND 10000),
    net         bigint NOT NULL,
    tax         bigint NOT NULL,
    total       bigint NOT NULL,
    PRIMARY KEY (invoice_id, line_no)
);

CREATE TABLE mz_invoice_payments (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    invoice_id uuid NOT NULL REFERENCES mz_invoices (id),
    source     text NOT NULL CHECK (source IN ('gateway', 'wallet')),
    source_ref text NOT NULL,                        -- payment id or ledger idempotency key
    amount     bigint NOT NULL,                      -- negative for a refund
    journal_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, source_ref)
);
CREATE INDEX mz_invoice_payments_invoice ON mz_invoice_payments (invoice_id, id);

CREATE TABLE mz_recon_runs (
    id          uuid PRIMARY KEY,
    gateway     text NOT NULL,
    from_time   timestamptz NOT NULL,
    to_time     timestamptz NOT NULL,
    summary     jsonb NOT NULL DEFAULT '{}',
    created_by  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mz_recon_items (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id         uuid NOT NULL REFERENCES mz_recon_runs (id),
    kind           text NOT NULL CHECK (kind IN ('missing_internal', 'missing_gateway', 'amount_mismatch', 'status_mismatch')),
    payment_id     uuid,
    gateway_ref    text NOT NULL DEFAULT '',
    our_amount     bigint,
    gateway_amount bigint,
    details        text NOT NULL DEFAULT ''
);
CREATE INDEX mz_recon_items_run ON mz_recon_items (run_id, id);

CREATE TABLE mz_disputes (
    id          uuid PRIMARY KEY,
    payment_id  uuid REFERENCES mz_payments (id),
    recon_item  bigint REFERENCES mz_recon_items (id),
    kind        text NOT NULL,
    status      text NOT NULL CHECK (status IN ('open', 'investigating', 'resolved', 'rejected')),
    opened_by   text NOT NULL DEFAULT '',
    notes       jsonb NOT NULL DEFAULT '[]',
    resolution  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mz_disputes_status ON mz_disputes (status, created_at);

-- +goose Down
DROP TABLE mz_disputes, mz_recon_items, mz_recon_runs, mz_invoice_payments, mz_invoice_items, mz_invoices, mz_wallets;
