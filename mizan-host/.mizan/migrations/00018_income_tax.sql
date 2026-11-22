-- +goose Up
-- Corporate income tax of a fiscal year: versioned rules (data with sources), one assessment per fiscal year, tax-loss carry-forward, payments.

CREATE TABLE mz_it_rules (
    id                     uuid PRIMARY KEY,
    effective_from         date NOT NULL UNIQUE,
    rate_bp                integer NOT NULL CHECK (rate_bp BETWEEN 1 AND 10000),
    cooperative_discount_bp integer NOT NULL DEFAULT 0 CHECK (cooperative_discount_bp BETWEEN 0 AND 10000),
    loss_carry_years       integer CHECK (loss_carry_years IS NULL OR loss_carry_years > 0),
    filing_months          integer NOT NULL CHECK (filing_months BETWEEN 1 AND 12),
    source                 text NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mz_it_assessments (
    id                 uuid PRIMARY KEY,
    fiscal_year        text NOT NULL UNIQUE,
    year_from          date NOT NULL,
    year_to            date NOT NULL,
    book_on            date,
    profit             bigint NOT NULL,
    adjustments        jsonb NOT NULL,
    add_back_total     bigint NOT NULL,
    deduction_total    bigint NOT NULL,
    depreciation_adj   bigint NOT NULL,
    taxable_before_loss bigint NOT NULL,
    loss_used          bigint NOT NULL CHECK (loss_used >= 0),
    new_loss           bigint NOT NULL CHECK (new_loss >= 0),
    taxable            bigint NOT NULL CHECK (taxable >= 0),
    rate_bp            integer NOT NULL,
    tax                bigint NOT NULL CHECK (tax >= 0),
    credits            bigint NOT NULL CHECK (credits >= 0 AND credits <= tax),
    payable            bigint NOT NULL,
    paid               bigint NOT NULL DEFAULT 0 CHECK (paid >= 0 AND paid <= payable),
    due_on             date NOT NULL,
    rule_id            uuid NOT NULL REFERENCES mz_it_rules (id),
    journal_id         uuid,
    idem_key           text NOT NULL UNIQUE,
    actor              text NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now()
);

-- A loss is an assessment with new_loss > 0; its uses are the amounts later assessments set off.
CREATE TABLE mz_it_loss_uses (
    id            bigserial PRIMARY KEY,
    loss_id       uuid NOT NULL REFERENCES mz_it_assessments (id),
    used_by       uuid NOT NULL REFERENCES mz_it_assessments (id),
    amount        bigint NOT NULL CHECK (amount > 0)
);

CREATE TABLE mz_it_payments (
    idem_key      text PRIMARY KEY,
    assessment_id uuid NOT NULL REFERENCES mz_it_assessments (id),
    amount        bigint NOT NULL CHECK (amount > 0),
    journal_id    uuid NOT NULL,
    paid_on       date NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Rate of art. 105 of the Direct Taxes Law (25%), in this form since the amendment of 1380/11/27, kept by the 1394 reform and not changed in the text read
-- (budget laws 1403/1404 give a 5-point cut for licensed producers for 1403 only: an explicit per-declaration input, not a rule). Note 6: 25% discount for
-- ordinary cooperatives. No limit of years for carrying a loss: art. 148 item 12 as read has none. Art. 110: four months after the tax year.
INSERT INTO mz_it_rules (id, effective_from, rate_bp, cooperative_discount_bp, loss_carry_years, filing_months, source)
VALUES ('00000000-0000-4000-8000-000000001380', '2002-02-16', 2500, 2500, NULL, 4,
        'Direct Taxes Law art. 105 (25%; note 6 cooperative discount 25%), art. 110 (four months after the tax year), art. 148 item 12 (losses carried to later years, no year limit in the text read); thdorsan.com/law-doc/قانون-مالیات-های-مستقیم-ماده-105, -ماده-110, -ماده-148. Texts as consolidated on 1402/12/21 (art. 105) and 1401/05/09 (art. 148).');

-- +goose Down
DROP TABLE mz_it_payments, mz_it_loss_uses, mz_it_assessments, mz_it_rules;
