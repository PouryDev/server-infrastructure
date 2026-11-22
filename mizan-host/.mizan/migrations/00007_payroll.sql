-- +goose Up
-- Payroll: employees, contracts, versioned social-security / income-tax rules, monthly payslips and payment runs.
CREATE TABLE mz_pay_employees (
    id           uuid PRIMARY KEY,
    code         text NOT NULL UNIQUE,
    name         text NOT NULL,
    national_id  text NOT NULL DEFAULT '' CHECK (national_id = '' OR national_id ~ '^[0-9]{10}$'),
    insurance_no text NOT NULL DEFAULT '',
    active       boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mz_pay_contracts (
    id          uuid PRIMARY KEY,
    employee_id uuid NOT NULL REFERENCES mz_pay_employees (id),
    base_salary bigint NOT NULL CHECK (base_salary > 0),
    currency    text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    allowances  jsonb NOT NULL DEFAULT '[]',
    insured     boolean NOT NULL DEFAULT true,
    start_date  date NOT NULL,
    end_date    date,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK (end_date IS NULL OR end_date >= start_date)
);
CREATE INDEX mz_pay_contracts_employee ON mz_pay_contracts (employee_id, start_date);

-- A rule version is data: finance adds a new one when the law changes. Versions are never updated.
CREATE TABLE mz_pay_rule_versions (
    id                        uuid PRIMARY KEY,
    name                      text NOT NULL,
    effective_from            date NOT NULL UNIQUE,
    currency                  text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    employee_bp               int NOT NULL CHECK (employee_bp BETWEEN 0 AND 10000),
    employer_bp               int NOT NULL CHECK (employer_bp BETWEEN 0 AND 10000),
    unemployment_bp           int NOT NULL CHECK (unemployment_bp BETWEEN 0 AND 10000),
    ceiling_daily             bigint NOT NULL DEFAULT 0 CHECK (ceiling_daily >= 0),
    floor_daily               bigint NOT NULL DEFAULT 0 CHECK (floor_daily >= 0),
    deduct_employee_insurance boolean NOT NULL,
    brackets                  jsonb NOT NULL,
    source                    text NOT NULL DEFAULT '',
    created_by                text NOT NULL DEFAULT '',
    created_at                timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mz_pay_runs (
    id         uuid PRIMARY KEY,
    idem_key   text NOT NULL UNIQUE,
    year       int NOT NULL,
    month      int NOT NULL CHECK (month BETWEEN 1 AND 12),
    pay_from   text NOT NULL,
    total      bigint NOT NULL DEFAULT 0,
    currency   text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    count      int NOT NULL DEFAULT 0,
    journal_id uuid,
    actor      text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mz_pay_payslips (
    id            uuid PRIMARY KEY,
    employee_id   uuid NOT NULL REFERENCES mz_pay_employees (id),
    contract_id   uuid NOT NULL REFERENCES mz_pay_contracts (id),
    rule_id       uuid NOT NULL REFERENCES mz_pay_rule_versions (id), -- restricts deleting a version that was used
    year          int NOT NULL,
    month         int NOT NULL CHECK (month BETWEEN 1 AND 12),
    status        text NOT NULL CHECK (status IN ('issued', 'paid', 'void')),
    currency      text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    days          int NOT NULL,
    month_days    int NOT NULL,
    lines         jsonb NOT NULL,
    gross         bigint NOT NULL,
    insurable     bigint NOT NULL,
    employee_ins  bigint NOT NULL,
    employer_ins  bigint NOT NULL,
    unemployment  bigint NOT NULL,
    taxable       bigint NOT NULL,
    tax           bigint NOT NULL,
    net           bigint NOT NULL CHECK (net >= 0),
    journal_id    uuid,
    void_journal  uuid,
    run_id        uuid REFERENCES mz_pay_runs (id),
    void_reason   text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'paid') = (run_id IS NOT NULL))
);
-- one live payslip per employee and Jalali month: this is the idempotency of issuing
CREATE UNIQUE INDEX mz_pay_payslips_live ON mz_pay_payslips (employee_id, year, month) WHERE status <> 'void';
CREATE INDEX mz_pay_payslips_month ON mz_pay_payslips (year, month, status);

-- Starting data: the 1405 figures gathered from secondary sources (see mizan/README.md, Payroll). They are rows like
-- any other version: confirm them against the official texts and add a new version to change anything.
-- Amounts are rials. Daily ceiling = 7 x the 1405 daily minimum wage (5,541,850); floor = the daily minimum wage.
-- Tax brackets are monthly: 400,000,000 rials (40M toman) exempt, then 10/15/20/25/30%.
INSERT INTO mz_pay_rule_versions (id, name, effective_from, currency, employee_bp, employer_bp, unemployment_bp, ceiling_daily, floor_daily,
                                  deduct_employee_insurance, brackets, source, created_by)
VALUES ('00000000-0000-4000-8000-000000001405', 'Iran 1405', DATE '2026-03-21', 'IRR', 700, 2000, 300, 38792950, 5541850, true,
        '[{"up_to":400000000,"rate_bp":0},{"up_to":800000000,"rate_bp":1000},{"up_to":1000000000,"rate_bp":1500},{"up_to":1200000000,"rate_bp":2000},{"up_to":1400000000,"rate_bp":2500},{"up_to":0,"rate_bp":3000}]',
        'UNVERIFIED: compiled 2026-11 from secondary sites (minimum-wage circular of the Supreme Labour Council, budget law 1405); confirm against ssa.ir, mol.ir and nta.ir',
        'migration');

-- +goose Down
DROP TABLE mz_pay_payslips, mz_pay_runs, mz_pay_rule_versions, mz_pay_contracts, mz_pay_employees;
