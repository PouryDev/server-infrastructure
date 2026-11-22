-- +goose Up
-- Electronic invoices handed to a tax-authority provider (vendor neutral): a queue with the provider's receipt and verdict.
CREATE TABLE mz_ein_documents (
    id               uuid PRIMARY KEY,
    source_type      text NOT NULL,
    source_id        text NOT NULL,
    status           text NOT NULL CHECK (status IN ('queued', 'sent', 'success', 'failed')),
    trace_id         uuid NOT NULL UNIQUE, -- the request id the provider echoes back (Moadian: requestTraceId / uid)
    fiscal_id        text NOT NULL DEFAULT '',
    invoice          jsonb NOT NULL,
    reference_number text NOT NULL DEFAULT '',
    errors           jsonb NOT NULL DEFAULT '[]',
    warnings         jsonb NOT NULL DEFAULT '[]',
    attempts         int NOT NULL DEFAULT 0,
    last_error       text NOT NULL DEFAULT '',
    sent_at          timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_type, source_id)
);
CREATE INDEX mz_ein_documents_status ON mz_ein_documents (status, created_at);

-- +goose Down
DROP TABLE mz_ein_documents;
