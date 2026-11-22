-- +goose Up
-- Coupons (limits are data, redemption is idempotent) and subscriptions (plans, subscriptions, billing periods).
CREATE TABLE mz_cpn_coupons (
    id               uuid PRIMARY KEY,
    code             text NOT NULL UNIQUE CHECK (code ~ '^[A-Z0-9_-]{3,40}$'),
    name             text NOT NULL,
    kind             text NOT NULL CHECK (kind IN ('percent', 'fixed')),
    percent_bp       int NOT NULL DEFAULT 0 CHECK (percent_bp BETWEEN 0 AND 10000),
    currency         text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    fixed_amount     bigint NOT NULL DEFAULT 0 CHECK (fixed_amount >= 0),
    min_amount       bigint NOT NULL DEFAULT 0 CHECK (min_amount >= 0),
    valid_from       date,
    valid_until      date,
    max_redemptions  int CHECK (max_redemptions IS NULL OR max_redemptions > 0),
    max_per_customer int CHECK (max_per_customer IS NULL OR max_per_customer > 0),
    active           boolean NOT NULL DEFAULT true,
    redeemed         int NOT NULL DEFAULT 0 CHECK (redeemed >= 0),
    created_by       text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CHECK ((kind = 'percent' AND percent_bp > 0 AND fixed_amount = 0) OR (kind = 'fixed' AND fixed_amount > 0 AND percent_bp = 0)),
    CHECK (valid_until IS NULL OR valid_from IS NULL OR valid_until >= valid_from),
    CHECK (max_redemptions IS NULL OR redeemed <= max_redemptions)
);

CREATE TABLE mz_cpn_redemptions (
    id              uuid PRIMARY KEY,
    idem_key        text NOT NULL UNIQUE,
    coupon_id       uuid NOT NULL REFERENCES mz_cpn_coupons (id),
    customer_ref    text NOT NULL,
    currency        text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    amount          bigint NOT NULL CHECK (amount > 0),
    discount        bigint NOT NULL CHECK (discount >= 0),
    status          text NOT NULL CHECK (status IN ('redeemed', 'released')),
    subject_type    text NOT NULL DEFAULT '',
    subject_id      text NOT NULL DEFAULT '',
    released_reason text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mz_cpn_redemptions_customer ON mz_cpn_redemptions (coupon_id, customer_ref) WHERE status = 'redeemed';

CREATE TABLE mz_sub_plans (
    id            uuid PRIMARY KEY,
    code          text NOT NULL UNIQUE,
    name          text NOT NULL,
    currency      text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    price         bigint NOT NULL CHECK (price > 0),
    interval_unit text NOT NULL CHECK (interval_unit IN ('day', 'month', 'year')),
    interval_count int NOT NULL CHECK (interval_count BETWEEN 1 AND 366),
    trial_days    int NOT NULL DEFAULT 0 CHECK (trial_days BETWEEN 0 AND 366),
    tax_bp        int CHECK (tax_bp IS NULL OR tax_bp BETWEEN 0 AND 10000),
    active        boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- A subscription copies the plan's terms when it starts: a later price change never touches it.
CREATE TABLE mz_sub_subscriptions (
    id                  uuid PRIMARY KEY,
    idem_key            text NOT NULL UNIQUE,
    plan_id             uuid NOT NULL REFERENCES mz_sub_plans (id),
    customer_ref        text NOT NULL,
    buyer               jsonb NOT NULL DEFAULT '{}',
    status              text NOT NULL CHECK (status IN ('trialing', 'active', 'paused', 'canceled')),
    currency            text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    price               bigint NOT NULL CHECK (price > 0),
    interval_unit       text NOT NULL CHECK (interval_unit IN ('day', 'month', 'year')),
    interval_count      int NOT NULL CHECK (interval_count >= 1),
    tax_bp              int,
    coupon_code         text NOT NULL DEFAULT '',
    start_date          date NOT NULL,
    anchor_date         date NOT NULL,
    anchor_no           int NOT NULL CHECK (anchor_no >= 1),
    next_bill_date      date,
    periods_billed      int NOT NULL DEFAULT 0,
    cancel_at_period_end boolean NOT NULL DEFAULT false,
    canceled_at         date,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mz_sub_due ON mz_sub_subscriptions (next_bill_date) WHERE status IN ('trialing', 'active');

CREATE TABLE mz_sub_periods (
    id              uuid PRIMARY KEY,
    subscription_id uuid NOT NULL REFERENCES mz_sub_subscriptions (id),
    no              int NOT NULL CHECK (no >= 1),
    period_start    date NOT NULL,
    period_end      date NOT NULL,
    currency        text NOT NULL,
    amount          bigint NOT NULL,
    status          text NOT NULL CHECK (status IN ('pending', 'invoiced')),
    invoice_id      uuid,
    attempts        int NOT NULL DEFAULT 0,
    last_error      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (subscription_id, no), -- a period is created once, so it is billed once
    CHECK ((status = 'invoiced') = (invoice_id IS NOT NULL))
);
CREATE INDEX mz_sub_periods_pending ON mz_sub_periods (attempts, created_at) WHERE status = 'pending';

-- +goose Down
DROP TABLE mz_sub_periods, mz_sub_subscriptions, mz_sub_plans, mz_cpn_redemptions, mz_cpn_coupons;
