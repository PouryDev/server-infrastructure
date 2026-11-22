-- +goose Up
CREATE TABLE mz_payments (
    id                uuid PRIMARY KEY,
    idem_key          text NOT NULL UNIQUE,
    status            text NOT NULL CHECK (status IN ('created', 'pending', 'paid', 'failed', 'expired', 'partially_refunded', 'refunded')),
    purpose_type      text NOT NULL,                 -- invoice | wallet_topup
    purpose_id        text NOT NULL,
    payer_ref         text NOT NULL DEFAULT '',
    amount            bigint NOT NULL CHECK (amount > 0),
    currency          text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    gateway           text NOT NULL,
    gateway_ref       text,                          -- authority / token issued by the gateway
    gateway_txn_ref   text NOT NULL DEFAULT '',      -- reference number after verification (RRN / track id)
    card_pan          text NOT NULL DEFAULT '',      -- masked
    description       text NOT NULL DEFAULT '',
    callback_url      text NOT NULL,
    pay_url           text NOT NULL DEFAULT '',
    expires_at        timestamptz NOT NULL,
    paid_at           timestamptz,
    failure_code      text NOT NULL DEFAULT '',
    failure_reason    text NOT NULL DEFAULT '',
    refunded_amount   bigint NOT NULL DEFAULT 0 CHECK (refunded_amount >= 0 AND refunded_amount <= amount),
    settled_journal   uuid,
    metadata          jsonb NOT NULL DEFAULT '{}',
    version           int NOT NULL DEFAULT 1,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX mz_payments_gateway_ref ON mz_payments (gateway, gateway_ref) WHERE gateway_ref IS NOT NULL;
CREATE INDEX mz_payments_purpose ON mz_payments (purpose_type, purpose_id);
CREATE INDEX mz_payments_expiry ON mz_payments (expires_at) WHERE status IN ('created', 'pending');
CREATE INDEX mz_payments_created ON mz_payments (created_at, id);

CREATE TABLE mz_payment_transitions (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    payment_id  uuid NOT NULL REFERENCES mz_payments (id),
    from_status text NOT NULL,
    to_status   text NOT NULL,
    reason      text NOT NULL DEFAULT '',
    at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mz_payment_transitions_payment ON mz_payment_transitions (payment_id, id);

-- Every callback exactly as received, kept for disputes and replay. event_key makes a duplicate callback a no-op.
CREATE TABLE mz_gateway_events (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    gateway      text NOT NULL,
    event_key    text NOT NULL,
    payment_id   uuid REFERENCES mz_payments (id),
    raw          text NOT NULL,
    remote_ip    text NOT NULL DEFAULT '',
    received_at  timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    outcome      text NOT NULL DEFAULT '',
    UNIQUE (gateway, event_key)
);

CREATE TABLE mz_refunds (
    id          uuid PRIMARY KEY,
    payment_id  uuid NOT NULL REFERENCES mz_payments (id),
    idem_key    text NOT NULL UNIQUE,
    amount      bigint NOT NULL CHECK (amount > 0),
    reason      text NOT NULL DEFAULT '',
    status      text NOT NULL CHECK (status IN ('pending', 'done', 'failed')),
    gateway_ref text NOT NULL DEFAULT '',
    failure     text NOT NULL DEFAULT '',
    journal_id  uuid,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mz_refunds_payment ON mz_refunds (payment_id);

-- +goose Down
DROP TABLE mz_refunds, mz_gateway_events, mz_payment_transitions, mz_payments;
