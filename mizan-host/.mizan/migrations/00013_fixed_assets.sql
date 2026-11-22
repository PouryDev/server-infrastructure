-- +goose Up
-- Fixed-asset register and depreciation. Rates and lives are data on the category (with the source they came from); an
-- asset copies them when it is acquired. Depreciation rows are append-only facts keyed by (asset, month).

CREATE TABLE mz_fa_categories (
    id             uuid PRIMARY KEY,
    code           text NOT NULL UNIQUE,
    name           text NOT NULL,
    method         text NOT NULL CHECK (method IN ('straight_line', 'declining_balance')),
    life_months    integer NOT NULL DEFAULT 0 CHECK (life_months >= 0),
    rate_bp        integer NOT NULL DEFAULT 0 CHECK (rate_bp BETWEEN 0 AND 10000),
    salvage_bp     integer NOT NULL DEFAULT 0 CHECK (salvage_bp BETWEEN 0 AND 9000),
    source         text NOT NULL,
    cost_account   text NOT NULL,
    accdep_account text NOT NULL,
    active         boolean NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK ((method = 'straight_line' AND life_months > 0) OR (method = 'declining_balance' AND rate_bp > 0))
);

CREATE TABLE mz_fa_assets (
    id                     uuid PRIMARY KEY,
    code                   text NOT NULL UNIQUE,
    name                   text NOT NULL,
    category_id            uuid NOT NULL REFERENCES mz_fa_categories (id),
    acquired_on            date NOT NULL,
    in_service_on          date NOT NULL,
    currency               text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    cost                   bigint NOT NULL CHECK (cost > 0),
    salvage                bigint NOT NULL CHECK (salvage >= 0 AND salvage < cost),
    method                 text NOT NULL CHECK (method IN ('straight_line', 'declining_balance')),
    life_months            integer NOT NULL,
    rate_bp                integer NOT NULL,
    cost_account           text NOT NULL,
    accdep_account         text NOT NULL,
    start_index            integer NOT NULL,
    accumulated            bigint NOT NULL DEFAULT 0 CHECK (accumulated >= 0 AND accumulated <= cost - salvage),
    months_done            integer NOT NULL DEFAULT 0,
    through_index          integer NOT NULL DEFAULT 0,
    dep_year               integer NOT NULL DEFAULT 0,
    year_start_accumulated bigint NOT NULL DEFAULT 0,
    months_in_year         integer NOT NULL DEFAULT 0,
    status                 text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disposed')),
    disposed_on            date,
    acquisition_journal    uuid,
    disposal_journal       uuid,
    disposal_key           text NOT NULL DEFAULT '',
    idem_key               text NOT NULL UNIQUE,
    created_at             timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mz_fa_runs (
    period_index integer NOT NULL,
    seq          integer NOT NULL,
    year         integer NOT NULL,
    month        integer NOT NULL,
    journal_id   uuid NOT NULL,
    total        bigint NOT NULL,
    currency     text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (period_index, seq)
);

CREATE TABLE mz_fa_depreciations (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    asset_id     uuid NOT NULL REFERENCES mz_fa_assets (id),
    period_index integer NOT NULL,
    run_seq      integer NOT NULL,
    amount       bigint NOT NULL CHECK (amount > 0),
    currency     text NOT NULL,
    UNIQUE (asset_id, period_index)
);

-- +goose Down
DROP TABLE mz_fa_depreciations, mz_fa_runs, mz_fa_assets, mz_fa_categories;
