-- +goose Up
-- Treasury: bank accounts, cheques (received and issued), bank statement lines and gateway settlements.
-- Every bank account is a ledger account (ledger_code); this schema holds what the ledger does not know.
CREATE TABLE mz_bank_accounts (
    id          uuid PRIMARY KEY,
    name        text NOT NULL,
    bank_name   text NOT NULL DEFAULT '',
    iban        text NOT NULL DEFAULT '' CHECK (iban = '' OR iban ~ '^IR[0-9]{24}$'),
    account_no  text NOT NULL DEFAULT '',
    currency    text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    ledger_code text NOT NULL UNIQUE,
    active      boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mz_cheques (
    id               uuid PRIMARY KEY,
    idem_key         text NOT NULL UNIQUE,
    direction        text NOT NULL CHECK (direction IN ('receivable', 'payable')),
    status           text NOT NULL,
    sayad_id         text NOT NULL DEFAULT '' CHECK (sayad_id = '' OR sayad_id ~ '^[0-9]{16}$'),
    number           text NOT NULL,
    amount           bigint NOT NULL CHECK (amount > 0),
    currency         text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    due_date         date NOT NULL,
    counterparty_ref text NOT NULL,
    drawer_bank      text NOT NULL DEFAULT '',
    note             text NOT NULL DEFAULT '',
    bank_account_id  uuid REFERENCES mz_bank_accounts (id), -- deposit account (received) / account it is drawn on (issued)
    counter_account  text NOT NULL,                         -- ledger account on the other side of receipt/issue
    open_journal     uuid,                                  -- the journal of receipt (received) or issue (issued)
    deposit_journal  uuid,
    final_journal    uuid,
    reason           text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT mz_cheques_status_check CHECK (
        (direction = 'receivable' AND status IN ('in_hand', 'deposited', 'cleared', 'bounced', 'returned'))
        OR (direction = 'payable' AND status IN ('issued', 'cleared', 'bounced', 'returned'))),
    -- an issued cheque is drawn on one of our accounts; a received one has an account from the moment it is deposited
    CONSTRAINT mz_cheques_bank_check CHECK (
        (direction = 'payable' AND bank_account_id IS NOT NULL)
        OR (direction = 'receivable' AND (status IN ('in_hand', 'returned') OR bank_account_id IS NOT NULL)))
);
CREATE UNIQUE INDEX mz_cheques_sayad ON mz_cheques (direction, sayad_id) WHERE sayad_id <> '';
CREATE INDEX mz_cheques_status_due ON mz_cheques (status, due_date);
CREATE INDEX mz_cheques_counterparty ON mz_cheques (counterparty_ref);

CREATE TABLE mz_cheque_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    cheque_id   uuid NOT NULL REFERENCES mz_cheques (id),
    from_status text NOT NULL,
    to_status   text NOT NULL,
    actor       text NOT NULL DEFAULT '',
    note        text NOT NULL DEFAULT '',
    journal_id  uuid,
    at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mz_cheque_events_cheque ON mz_cheque_events (cheque_id, id);

CREATE TABLE mz_gateway_settlements (
    id              uuid PRIMARY KEY,
    gateway         text NOT NULL,
    ref             text NOT NULL,                          -- the gateway's settlement/batch reference
    bank_account_id uuid NOT NULL REFERENCES mz_bank_accounts (id),
    gross           bigint NOT NULL CHECK (gross > 0),
    fee             bigint NOT NULL CHECK (fee >= 0),
    net             bigint NOT NULL CHECK (net > 0 AND net = gross - fee),
    settled_on      date NOT NULL,
    journal_id      uuid NOT NULL,
    line_id         uuid,                                   -- the bank statement line that confirmed it
    note            text NOT NULL DEFAULT '',
    actor           text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (gateway, ref)
);

CREATE TABLE mz_bank_statement_lines (
    id              uuid PRIMARY KEY,
    bank_account_id uuid NOT NULL REFERENCES mz_bank_accounts (id),
    line_date       date NOT NULL,
    amount          bigint NOT NULL CHECK (amount <> 0),    -- signed: positive money in, negative money out
    description     text NOT NULL DEFAULT '',
    reference       text NOT NULL DEFAULT '',
    balance         bigint,
    fingerprint     text NOT NULL,
    status          text NOT NULL DEFAULT 'unmatched' CHECK (status IN ('unmatched', 'matched', 'ignored')),
    match_kind      text NOT NULL DEFAULT '' CHECK (match_kind IN ('', 'cheque', 'settlement', 'fee', 'ignored')),
    match_id        text NOT NULL DEFAULT '',
    matched_by      text NOT NULL DEFAULT '',
    matched_at      timestamptz,
    journal_id      uuid,
    imported_by     text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (bank_account_id, fingerprint),
    CHECK ((status = 'unmatched') = (match_kind = ''))
);
CREATE INDEX mz_bank_lines_open ON mz_bank_statement_lines (bank_account_id, line_date, id) WHERE status = 'unmatched';
ALTER TABLE mz_gateway_settlements ADD CONSTRAINT mz_settlements_line_fk FOREIGN KEY (line_id) REFERENCES mz_bank_statement_lines (id);
CREATE UNIQUE INDEX mz_settlements_line ON mz_gateway_settlements (line_id) WHERE line_id IS NOT NULL;

-- +goose Down
DROP TABLE mz_gateway_settlements, mz_bank_statement_lines, mz_cheque_events, mz_cheques, mz_bank_accounts;
