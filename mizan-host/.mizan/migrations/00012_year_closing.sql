-- +goose Up
-- Fiscal-year closing. One row per fiscal year records the state of its closing; the closing and opening documents
-- themselves are ordinary append-only journals (kind 'closing' / 'opening', ref_type 'fiscal_year_closing').
-- generation counts the closings of the year: reopening reverses the documents and the next closing uses new keys.

CREATE TABLE mz_year_closings (
    fiscal_year_id   bigint PRIMARY KEY REFERENCES mz_fiscal_years (id),
    generation       integer NOT NULL CHECK (generation >= 1),
    state            text NOT NULL CHECK (state IN ('closed', 'reopened')),
    retained_account text NOT NULL,
    actor            text NOT NULL DEFAULT '',
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- A journal can never be inserted into a closed fiscal year. Service.Post already refuses this; the trigger closes
-- the race with a posting that checked the period just before the year was closed.
-- +goose StatementBegin
CREATE FUNCTION mz_forbid_closed_year() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE st text;
BEGIN
    SELECT status INTO st FROM mz_fiscal_years WHERE id = NEW.fiscal_year_id;
    IF st = 'closed' THEN
        RAISE EXCEPTION 'fiscal year % is closed', NEW.fiscal_year_id
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TRIGGER mz_journals_closed_year BEFORE INSERT ON mz_journals
    FOR EACH ROW EXECUTE FUNCTION mz_forbid_closed_year();

-- +goose Down
DROP TRIGGER mz_journals_closed_year ON mz_journals;
DROP FUNCTION mz_forbid_closed_year();
DROP TABLE mz_year_closings;
