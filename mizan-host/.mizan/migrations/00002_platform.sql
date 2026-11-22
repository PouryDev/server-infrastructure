-- +goose Up
-- Cross-cutting tables: HTTP idempotency, transactional outbox, audit log.

CREATE TABLE mz_idempotency (
    scope        text NOT NULL,
    key          text NOT NULL,
    request_hash text NOT NULL,
    status_code  int NOT NULL,
    response     bytea NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, key)
);
CREATE INDEX mz_idempotency_created ON mz_idempotency (created_at);

CREATE TABLE mz_outbox (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id        uuid NOT NULL UNIQUE,
    topic           text NOT NULL,                  -- routing key, e.g. payment.paid
    aggregate_type  text NOT NULL DEFAULT '',
    aggregate_id    text NOT NULL DEFAULT '',
    payload         jsonb NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    published_at    timestamptz,
    attempts        int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text NOT NULL DEFAULT ''
);
CREATE INDEX mz_outbox_pending ON mz_outbox (id) WHERE published_at IS NULL;

CREATE TABLE mz_audit_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    actor       text NOT NULL,
    action      text NOT NULL,
    entity_type text NOT NULL,
    entity_id   text NOT NULL,
    details     jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX mz_audit_entity ON mz_audit_log (entity_type, entity_id, id);
CREATE TRIGGER mz_audit_immutable BEFORE UPDATE OR DELETE ON mz_audit_log
    FOR EACH ROW EXECUTE FUNCTION mz_forbid_mutation();

-- +goose Down
DROP TABLE mz_audit_log, mz_outbox, mz_idempotency;
