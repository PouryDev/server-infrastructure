-- +goose Up
-- VAT returns. The legal parameters (period length, filing deadline, late-payment penalty) are versioned DATA with their source; a return is filed once per period.

CREATE TABLE mz_vat_rules (
    id               uuid PRIMARY KEY,
    effective_from   date NOT NULL UNIQUE,
    period_months    integer NOT NULL CHECK (period_months IN (1, 2, 3)),
    filing_days      integer NOT NULL DEFAULT 0 CHECK (filing_days BETWEEN 0 AND 120),
    filing_month_end boolean NOT NULL DEFAULT false,
    late_penalty_bp  integer NOT NULL DEFAULT 0 CHECK (late_penalty_bp BETWEEN 0 AND 10000),
    source           text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CHECK ((filing_month_end AND filing_days = 0) OR (NOT filing_month_end AND filing_days > 0))
);

CREATE TABLE mz_vat_returns (
    id              uuid PRIMARY KEY,
    year            integer NOT NULL,
    period_months   integer NOT NULL,
    period_no       integer NOT NULL,
    from_date       date NOT NULL,
    to_date         date NOT NULL,
    output_tax      bigint NOT NULL,
    input_tax       bigint NOT NULL,
    net             bigint NOT NULL,
    opening_balance bigint NOT NULL,
    due_on          date,
    filed_on        date NOT NULL,
    journal_id      uuid,
    idem_key        text NOT NULL UNIQUE,
    actor           text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (year, period_months, period_no)
);

-- +goose Down
DROP TABLE mz_vat_returns, mz_vat_rules;
