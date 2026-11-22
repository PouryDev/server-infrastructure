-- +goose Up
-- Fixed assets, part 3: the idle-asset remark of art. 6 of the depreciation regulation (circular 200/95/78): idle for more than six consecutive months in a fiscal year ->
-- 30% of the depreciation for the idle months, 70% of the idle time added to the remaining life. Not for buildings, building installations, intangibles.
ALTER TABLE mz_fa_categories ADD COLUMN idle_exempt boolean NOT NULL DEFAULT false;
UPDATE mz_fa_categories SET idle_exempt = true WHERE source LIKE '% گروه 8 ردیف %' OR source LIKE '% گروه 25 ردیف %';
ALTER TABLE mz_fa_assets ADD COLUMN idle_exempt boolean NOT NULL DEFAULT false, ADD COLUMN progress_milli integer NOT NULL DEFAULT 0;
UPDATE mz_fa_assets SET progress_milli = months_done * 1000;
UPDATE mz_fa_assets a SET idle_exempt = c.idle_exempt FROM mz_fa_categories c WHERE c.id = a.category_id;
ALTER TABLE mz_fa_depreciations ADD COLUMN idle boolean NOT NULL DEFAULT false, ADD COLUMN progress_milli integer NOT NULL DEFAULT 1000;

CREATE TABLE mz_fa_idle_spans (
    id         uuid PRIMARY KEY,
    asset_id   uuid NOT NULL REFERENCES mz_fa_assets (id),
    from_index integer NOT NULL,
    to_index   integer NOT NULL CHECK (to_index >= from_index),
    note       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mz_fa_idle_spans_asset ON mz_fa_idle_spans (asset_id);

-- +goose Down
DROP TABLE mz_fa_idle_spans;
ALTER TABLE mz_fa_depreciations DROP COLUMN idle, DROP COLUMN progress_milli;
ALTER TABLE mz_fa_assets DROP COLUMN idle_exempt, DROP COLUMN progress_milli;
ALTER TABLE mz_fa_categories DROP COLUMN idle_exempt;
