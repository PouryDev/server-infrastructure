-- +goose Up
-- Inventory: items with a running quantity and carrying value, FIFO purchase layers, and an append-only movement log.
-- Every movement locks its item row (SELECT ... FOR UPDATE), so stock can never go negative and concurrent movements serialise.

CREATE TABLE mz_inv_items (
    id         uuid PRIMARY KEY,
    code       text NOT NULL UNIQUE,
    name       text NOT NULL,
    unit       text NOT NULL,
    method     text NOT NULL CHECK (method IN ('fifo', 'weighted_average')),
    qty        bigint NOT NULL DEFAULT 0 CHECK (qty >= 0),
    value      bigint NOT NULL DEFAULT 0 CHECK (value >= 0),
    currency   text NOT NULL CHECK (currency IN ('IRR', 'IRT')),
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (qty > 0 OR value = 0)
);

CREATE TABLE mz_inv_layers (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    item_id         uuid NOT NULL REFERENCES mz_inv_items (id),
    seq             integer NOT NULL,
    qty             bigint NOT NULL CHECK (qty > 0),
    remaining       bigint NOT NULL CHECK (remaining >= 0 AND remaining <= qty),
    remaining_value bigint NOT NULL CHECK (remaining_value >= 0),
    UNIQUE (item_id, seq)
);
CREATE INDEX mz_inv_layers_open ON mz_inv_layers (item_id, seq) WHERE remaining > 0;

CREATE TABLE mz_inv_movements (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    item_id     uuid NOT NULL REFERENCES mz_inv_items (id),
    kind        text NOT NULL CHECK (kind IN ('receipt', 'issue', 'count_gain', 'count_loss', 'write_down')),
    qty         bigint NOT NULL,
    value       bigint NOT NULL,
    qty_after   bigint NOT NULL,
    value_after bigint NOT NULL,
    entry_date  date NOT NULL,
    journal_id  uuid,
    idem_key    text NOT NULL UNIQUE,
    ref         text NOT NULL DEFAULT '',
    note        text NOT NULL DEFAULT '',
    actor       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mz_inv_movements_item ON mz_inv_movements (item_id, id);

-- +goose Down
DROP TABLE mz_inv_movements, mz_inv_layers, mz_inv_items;
