-- +goose Up
-- Employee profile and workshop data needed by the insurance and tax list files. Typed JSON documents: the fields are validated by the application and nothing has a default.
CREATE TABLE mz_pay_profiles (
    employee_id uuid PRIMARY KEY REFERENCES mz_pay_employees (id),
    data        jsonb NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE mz_pay_workshop (
    id         integer PRIMARY KEY CHECK (id = 1),
    data       jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE mz_pay_workshop, mz_pay_profiles;
