-- +goose Up
-- Appropriation of a fiscal year's profit by the general meeting: one decision per fiscal year, dividend payments against it.

CREATE TABLE mz_appr_decisions (
    id                uuid PRIMARY KEY,
    fiscal_year       text NOT NULL UNIQUE,
    decided_on        date NOT NULL,
    pay_by            date NOT NULL,
    net_profit        bigint NOT NULL,
    retained_at_start bigint NOT NULL,
    capital           bigint NOT NULL,
    legal_reserve_before bigint NOT NULL,
    public_company    boolean NOT NULL,
    legal_reserve     bigint NOT NULL CHECK (legal_reserve >= 0),
    optional_reserve  bigint NOT NULL CHECK (optional_reserve >= 0),
    dividend          bigint NOT NULL CHECK (dividend >= 0),
    paid              bigint NOT NULL DEFAULT 0 CHECK (paid >= 0 AND paid <= dividend),
    journal_id        uuid,
    idem_key          text NOT NULL UNIQUE,
    actor             text NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mz_appr_payments (
    idem_key    text PRIMARY KEY,
    decision_id uuid NOT NULL REFERENCES mz_appr_decisions (id),
    amount      bigint NOT NULL CHECK (amount > 0),
    journal_id  uuid NOT NULL,
    paid_on     date NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE mz_appr_payments, mz_appr_decisions;
