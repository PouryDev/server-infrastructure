-- +goose Up
-- Double-entry ledger. journals and journal lines are append-only: a trigger refuses UPDATE, DELETE and
-- TRUNCATE, and a deferred constraint trigger refuses any journal whose debits and credits differ.

CREATE TABLE mz_coding (
    code        text PRIMARY KEY,
    parent_code text REFERENCES mz_coding (code),
    level       smallint NOT NULL CHECK (level BETWEEN 1 AND 4),   -- 1 group, 2 kol, 3 moein, 4 tafsili
    title       text NOT NULL,
    type_hint   text NOT NULL DEFAULT '' CHECK (type_hint IN ('', 'asset', 'liability', 'equity', 'revenue', 'expense'))
);

CREATE TABLE mz_accounts (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code         text NOT NULL UNIQUE,
    name         text NOT NULL,
    type         text NOT NULL CHECK (type IN ('asset', 'liability', 'equity', 'revenue', 'expense')),
    currency     text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    non_negative boolean NOT NULL DEFAULT false,
    coding_code  text REFERENCES mz_coding (code),
    owner_kind   text NOT NULL DEFAULT '',
    owner_id     text NOT NULL DEFAULT '',
    active       boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mz_accounts_owner ON mz_accounts (owner_kind, owner_id) WHERE owner_kind <> '';
CREATE INDEX mz_accounts_coding ON mz_accounts (coding_code) WHERE coding_code IS NOT NULL;

CREATE TABLE mz_fiscal_years (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name      text NOT NULL UNIQUE,                 -- "1405"
    starts_on date NOT NULL,
    ends_before date NOT NULL,                      -- exclusive: first day of the next fiscal year
    status    text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    CHECK (ends_before > starts_on)
);

CREATE TABLE mz_periods (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    fiscal_year_id bigint NOT NULL REFERENCES mz_fiscal_years (id),
    number         smallint NOT NULL,
    starts_on      date NOT NULL,
    ends_before    date NOT NULL,
    status         text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    UNIQUE (fiscal_year_id, number),
    CHECK (ends_before > starts_on)
);
CREATE INDEX mz_periods_range ON mz_periods (starts_on, ends_before);

CREATE TABLE mz_sequences (
    name text PRIMARY KEY,
    last bigint NOT NULL
);

CREATE TABLE mz_journals (
    id             uuid PRIMARY KEY,
    fiscal_year_id bigint NOT NULL REFERENCES mz_fiscal_years (id),
    period_id      bigint NOT NULL REFERENCES mz_periods (id),
    number         bigint NOT NULL,                  -- gap-free per fiscal year (legal document number)
    kind           text NOT NULL CHECK (kind IN ('normal', 'reversal', 'opening', 'closing')),
    entry_date     date NOT NULL,
    description    text NOT NULL,
    currency       text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    total          bigint NOT NULL CHECK (total > 0),
    ref_type       text NOT NULL DEFAULT '',
    ref_id         text NOT NULL DEFAULT '',
    idem_key       text NOT NULL UNIQUE,
    fingerprint    text NOT NULL,
    reverses_id    uuid UNIQUE REFERENCES mz_journals (id),
    created_by     text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (fiscal_year_id, number),
    CHECK ((kind = 'reversal') = (reverses_id IS NOT NULL))
);
CREATE INDEX mz_journals_ref ON mz_journals (ref_type, ref_id);
CREATE INDEX mz_journals_date ON mz_journals (entry_date, id);

CREATE TABLE mz_journal_lines (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    journal_id uuid NOT NULL REFERENCES mz_journals (id),
    line_no    smallint NOT NULL,
    account_id uuid NOT NULL REFERENCES mz_accounts (id),
    debit      bigint NOT NULL DEFAULT 0 CHECK (debit >= 0),
    credit     bigint NOT NULL DEFAULT 0 CHECK (credit >= 0),
    memo       text NOT NULL DEFAULT '',
    UNIQUE (journal_id, line_no),
    CHECK ((debit > 0) <> (credit > 0))
);
CREATE INDEX mz_journal_lines_account ON mz_journal_lines (account_id, id);

-- Running balance of accounts that must not go negative (customer wallets). Everything else is
-- computed from the lines, which stay the single source of truth.
CREATE TABLE mz_balances (
    account_id uuid PRIMARY KEY REFERENCES mz_accounts (id),
    balance    bigint NOT NULL,                      -- in the account's natural direction
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION mz_forbid_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only: correct it with a reversing entry', TG_TABLE_NAME
        USING ERRCODE = 'integrity_constraint_violation';
END $$;

CREATE FUNCTION mz_check_journal_balanced() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE d bigint; c bigint; n int; t bigint;
BEGIN
    SELECT COALESCE(sum(debit), 0), COALESCE(sum(credit), 0), count(*) INTO d, c, n
      FROM mz_journal_lines WHERE journal_id = NEW.journal_id;
    SELECT total INTO t FROM mz_journals WHERE id = NEW.journal_id;
    IF d <> c OR n < 2 OR d <> t THEN
        RAISE EXCEPTION 'journal % is not balanced (debit %, credit %, total %)', NEW.journal_id, d, c, t
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END $$;

CREATE FUNCTION mz_protect_account() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.code <> OLD.code OR NEW.type <> OLD.type OR NEW.currency <> OLD.currency
       OR NEW.non_negative <> OLD.non_negative THEN
        RAISE EXCEPTION 'account code, type, currency and non_negative cannot change'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TRIGGER mz_journals_immutable BEFORE UPDATE OR DELETE ON mz_journals
    FOR EACH ROW EXECUTE FUNCTION mz_forbid_mutation();
CREATE TRIGGER mz_journals_no_truncate BEFORE TRUNCATE ON mz_journals
    FOR EACH STATEMENT EXECUTE FUNCTION mz_forbid_mutation();
CREATE TRIGGER mz_lines_immutable BEFORE UPDATE OR DELETE ON mz_journal_lines
    FOR EACH ROW EXECUTE FUNCTION mz_forbid_mutation();
CREATE TRIGGER mz_lines_no_truncate BEFORE TRUNCATE ON mz_journal_lines
    FOR EACH STATEMENT EXECUTE FUNCTION mz_forbid_mutation();
CREATE CONSTRAINT TRIGGER mz_lines_balanced AFTER INSERT ON mz_journal_lines
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION mz_check_journal_balanced();
CREATE TRIGGER mz_accounts_protect BEFORE UPDATE ON mz_accounts
    FOR EACH ROW EXECUTE FUNCTION mz_protect_account();

-- +goose Down
DROP TABLE mz_balances, mz_journal_lines, mz_journals, mz_sequences, mz_periods, mz_fiscal_years, mz_accounts, mz_coding;
DROP FUNCTION mz_protect_account(), mz_check_journal_balanced(), mz_forbid_mutation();
