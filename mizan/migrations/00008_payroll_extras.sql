-- +goose Up
-- Payroll, part 2: labour-law terms (overtime, holiday and night work, leave payout, severance) as rule-version data,
-- employee loans repaid from the net pay, and payments of withheld tax and social security to their authorities.
ALTER TABLE mz_pay_rule_versions ADD COLUMN terms jsonb NOT NULL DEFAULT '{}';

-- Starting data for the 1405 version, from secondary sites (not verified against the official texts):
-- overtime and holiday work 140% of the hourly wage (Labour Code art. 59), night work premium 35%, hourly wage = daily wage / 7.33,
-- a monthly wage is 30 days, severance 30 days of wage per year from one year of service, not taxable or insurable when paid at the
-- end of service. Unused-leave payout: one day's wage per day, not insurable (SSA circular 4195 per the sources); whether it is
-- taxable is DISPUTED between the sources, so it is seeded as taxable (the cautious side): confirm and add a version if it is not.
UPDATE mz_pay_rule_versions
   SET terms = '{"hours_per_day_x100":733,"days_per_month":30,"overtime":{"bp":14000,"taxable":true,"insurable":true},"holiday":{"bp":14000,"taxable":true,"insurable":true},"night":{"bp":3500,"taxable":true,"insurable":true},"leave_payout":{"bp":10000,"taxable":true,"insurable":false},"severance":{"days_per_year":30,"min_years":1,"taxable":false,"insurable":false}}',
       source = source || '; terms (overtime, leave, severance) also UNVERIFIED, leave payout tax treatment disputed'
 WHERE id = '00000000-0000-4000-8000-000000001405';

ALTER TABLE mz_pay_payslips ADD COLUMN deductions jsonb NOT NULL DEFAULT '[]', ADD COLUMN deduction_total bigint NOT NULL DEFAULT 0 CHECK (deduction_total >= 0);

CREATE TABLE mz_pay_loans (
    id          uuid PRIMARY KEY,
    idem_key    text NOT NULL UNIQUE,
    employee_id uuid NOT NULL REFERENCES mz_pay_employees (id),
    currency    text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    principal   bigint NOT NULL CHECK (principal > 0),
    installment bigint NOT NULL CHECK (installment > 0 AND installment <= principal),
    remaining   bigint NOT NULL CHECK (remaining >= 0 AND remaining <= principal),
    start_year  int NOT NULL,
    start_month int NOT NULL CHECK (start_month BETWEEN 1 AND 12),
    status      text NOT NULL CHECK (status IN ('active', 'closed')),
    pay_from    text NOT NULL,
    journal_id  uuid,
    note        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'closed') = (remaining = 0))
);
CREATE INDEX mz_pay_loans_employee ON mz_pay_loans (employee_id, created_at) WHERE status = 'active';

CREATE TABLE mz_pay_remittances (
    id           uuid PRIMARY KEY,
    idem_key     text NOT NULL UNIQUE,
    kind         text NOT NULL CHECK (kind IN ('tax', 'insurance')),
    period_year  int NOT NULL DEFAULT 0,
    period_month int NOT NULL DEFAULT 0 CHECK (period_month BETWEEN 0 AND 12),
    amount       bigint NOT NULL CHECK (amount > 0),
    currency     text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    pay_from     text NOT NULL,
    reference    text NOT NULL DEFAULT '',
    journal_id   uuid,
    actor        text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE mz_pay_remittances, mz_pay_loans;
ALTER TABLE mz_pay_payslips DROP COLUMN deductions, DROP COLUMN deduction_total;
ALTER TABLE mz_pay_rule_versions DROP COLUMN terms;
