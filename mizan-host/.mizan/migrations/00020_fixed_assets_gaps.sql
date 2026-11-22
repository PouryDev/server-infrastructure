-- +goose Up
-- Fixed assets, part 2: units-of-production depreciation (regulation art. 9-c), voiding a depreciation run, partial disposal, and the small-transaction
-- threshold of art. 12 as versioned data.

-- Units of production: life_months holds the asset's total capacity in UNITS for this method.
ALTER TABLE mz_fa_categories DROP CONSTRAINT mz_fa_categories_method_check;
ALTER TABLE mz_fa_categories ADD CONSTRAINT mz_fa_categories_method_check CHECK (method IN ('straight_line', 'declining_balance', 'units_of_production'));
ALTER TABLE mz_fa_categories DROP CONSTRAINT mz_fa_categories_check;
ALTER TABLE mz_fa_categories ADD CONSTRAINT mz_fa_categories_terms_check CHECK (
    (method = 'straight_line' AND life_months > 0) OR (method = 'declining_balance' AND rate_bp > 0) OR (method = 'units_of_production' AND life_months > 0));
ALTER TABLE mz_fa_assets DROP CONSTRAINT mz_fa_assets_method_check;
ALTER TABLE mz_fa_assets ADD CONSTRAINT mz_fa_assets_method_check CHECK (method IN ('straight_line', 'declining_balance', 'units_of_production'));

CREATE TABLE mz_fa_usage (
    asset_id     uuid NOT NULL REFERENCES mz_fa_assets (id),
    period_index integer NOT NULL,
    units        bigint NOT NULL CHECK (units >= 0),
    actor        text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, period_index)
);

-- Voiding a run: the run keeps its rows (history) and is marked; its items stop counting.
ALTER TABLE mz_fa_runs ADD COLUMN voided_journal uuid, ADD COLUMN voided_at timestamptz;
ALTER TABLE mz_fa_depreciations ADD COLUMN voided boolean NOT NULL DEFAULT false;
ALTER TABLE mz_fa_depreciations DROP CONSTRAINT mz_fa_depreciations_asset_id_period_index_key;
CREATE UNIQUE INDEX mz_fa_depreciations_live ON mz_fa_depreciations (asset_id, period_index) WHERE NOT voided;

CREATE TABLE mz_fa_partials (
    idem_key   text PRIMARY KEY,
    asset_id   uuid NOT NULL REFERENCES mz_fa_assets (id),
    portion_bp integer NOT NULL CHECK (portion_bp BETWEEN 1 AND 9999),
    journal_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Art. 12 (as told by the owner): an asset whose whole cost is below 10% of the small-transaction limit may be expensed. The limit is the cabinet decision on
-- government-transaction limits of each year (small transaction); versioned data.
CREATE TABLE mz_fa_thresholds (
    id             uuid PRIMARY KEY,
    effective_from date NOT NULL UNIQUE,
    small_limit    bigint NOT NULL CHECK (small_limit > 0),
    verified       boolean NOT NULL DEFAULT false,
    source         text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);
INSERT INTO mz_fa_thresholds (id, effective_from, small_limit, verified, source) VALUES
 ('00000000-0000-4000-8000-000000002004', '2025-03-21', 2850000000, false, '1404 small-transaction limit 2,850,000,000 rials, from tasnimnews/irib reports of the 1405 decision (secondary)'),
 ('00000000-0000-4000-8000-000000002005', '2026-03-21', 4230000000, false, 'Cabinet decision 20877/t25634 of 1405/03/11 on the 1405 limits of government transactions, small = 4,230,000,000 rials; listed on mefa.ir/news/741085 (headline only read) and reported by tasnimnews, irib, mahaksoft; one source dates it 1405/02/27');

-- +goose Down
DROP TABLE mz_fa_thresholds, mz_fa_partials;
DROP INDEX mz_fa_depreciations_live;
ALTER TABLE mz_fa_depreciations DROP COLUMN voided;
ALTER TABLE mz_fa_runs DROP COLUMN voided_journal, DROP COLUMN voided_at;
DROP TABLE mz_fa_usage;
