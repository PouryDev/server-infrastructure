-- +goose Up
-- +goose StatementBegin
CREATE TABLE users (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    username        text        NOT NULL CONSTRAINT users_username_key UNIQUE,
    name            text        NOT NULL,
    title           text        NOT NULL DEFAULT '',
    password_hash   text        NOT NULL,
    role            text        NOT NULL DEFAULT 'user' CONSTRAINT users_role_check CHECK (role IN ('user', 'finance', 'admin')),
    failed_attempts integer     NOT NULL DEFAULT 0,
    locked_until    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX users_created_at_idx ON users (created_at DESC, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE users;
-- +goose StatementEnd
