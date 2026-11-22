-- +goose Up
-- Inventory, part 2: specific identification, warehouses, lots with expiry (FEFO), and production costing (work in process, finished goods, overhead allocation).
-- Accounting standard no. 8: para 25 specific identification, paras 9-20 cost of conversion and overhead.

ALTER TABLE mz_inv_items DROP CONSTRAINT mz_inv_items_method_check;
ALTER TABLE mz_inv_items ADD CONSTRAINT mz_inv_items_method_check CHECK (method IN ('fifo', 'weighted_average', 'specific_identification'));
ALTER TABLE mz_inv_items ADD COLUMN fefo boolean NOT NULL DEFAULT false;

CREATE TABLE mz_inv_warehouses (
    id         uuid PRIMARY KEY,
    code       text NOT NULL UNIQUE,
    name       text NOT NULL,
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);
-- The main warehouse: stock that existed before warehouses sits here.
INSERT INTO mz_inv_warehouses (id, code, name) VALUES ('00000000-0000-4000-8000-000000003001', 'MAIN', 'Main warehouse');

CREATE TABLE mz_inv_stock (
    item_id      uuid NOT NULL REFERENCES mz_inv_items (id),
    warehouse_id uuid NOT NULL REFERENCES mz_inv_warehouses (id),
    qty          bigint NOT NULL CHECK (qty >= 0),
    PRIMARY KEY (item_id, warehouse_id)
);
INSERT INTO mz_inv_stock (item_id, warehouse_id, qty) SELECT id, '00000000-0000-4000-8000-000000003001', qty FROM mz_inv_items WHERE qty > 0;

ALTER TABLE mz_inv_layers ADD COLUMN warehouse_id uuid NOT NULL DEFAULT '00000000-0000-4000-8000-000000003001' REFERENCES mz_inv_warehouses (id),
    ADD COLUMN lot_no text NOT NULL DEFAULT '', ADD COLUMN expires_on date;
ALTER TABLE mz_inv_movements DROP CONSTRAINT mz_inv_movements_kind_check;
ALTER TABLE mz_inv_movements ADD CONSTRAINT mz_inv_movements_kind_check CHECK (kind IN ('receipt', 'issue', 'count_gain', 'count_loss', 'write_down', 'write_down_reversal', 'transfer'));
ALTER TABLE mz_inv_movements ADD COLUMN warehouse_id uuid REFERENCES mz_inv_warehouses (id), ADD COLUMN lot_id bigint;

-- Production costing: the allocation rates are versioned data (standard 8 para 11: fixed overhead on the normal capacity).
CREATE TABLE mz_prod_overhead_rates (
    id             uuid PRIMARY KEY,
    effective_from date NOT NULL UNIQUE,
    basis          text NOT NULL CHECK (basis IN ('direct_labor_cost', 'material_cost', 'machine_units', 'output_units')),
    fixed_rate     bigint NOT NULL CHECK (fixed_rate >= 0),
    variable_rate  bigint NOT NULL CHECK (variable_rate >= 0),
    budget_fixed   bigint NOT NULL DEFAULT 0,
    normal_cap     bigint NOT NULL DEFAULT 0,
    verified       boolean NOT NULL DEFAULT false,
    source         text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mz_prod_orders (
    id             uuid PRIMARY KEY,
    code           text NOT NULL UNIQUE,
    product_item   uuid NOT NULL REFERENCES mz_inv_items (id),
    planned_qty    bigint NOT NULL CHECK (planned_qty > 0),
    status         text NOT NULL CHECK (status IN ('open', 'completed')),
    material_cost  bigint NOT NULL DEFAULT 0 CHECK (material_cost >= 0),
    labor_cost     bigint NOT NULL DEFAULT 0 CHECK (labor_cost >= 0),
    fixed_overhead bigint NOT NULL DEFAULT 0,
    var_overhead   bigint NOT NULL DEFAULT 0,
    machine_units  bigint NOT NULL DEFAULT 0,
    output_qty     bigint NOT NULL DEFAULT 0,
    abnormal_loss  bigint NOT NULL DEFAULT 0,
    finished_cost  bigint NOT NULL DEFAULT 0,
    completed_on   date,
    complete_key   text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mz_prod_cost_lines (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES mz_prod_orders (id),
    kind     text NOT NULL CHECK (kind IN ('material', 'labor')),
    key      text NOT NULL UNIQUE,
    item_id  uuid,
    qty      bigint NOT NULL DEFAULT 0,
    amount   bigint NOT NULL,
    ref      text NOT NULL DEFAULT ''
);

CREATE TABLE mz_prod_settlements (
    key        text PRIMARY KEY,
    entry_date date NOT NULL,
    actual     bigint NOT NULL,
    applied    bigint NOT NULL,
    journal_id uuid,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mz_prod_pool_events (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind       text NOT NULL CHECK (kind IN ('actual', 'applied')),
    key        text NOT NULL UNIQUE,
    amount     bigint NOT NULL CHECK (amount > 0),
    entry_date date NOT NULL,
    ref        text NOT NULL DEFAULT '',
    settled_in text
);
CREATE INDEX mz_prod_pool_unsettled ON mz_prod_pool_events (id) WHERE settled_in IS NULL;

-- +goose Down
DROP TABLE mz_prod_pool_events, mz_prod_settlements, mz_prod_cost_lines, mz_prod_orders, mz_prod_overhead_rates;
ALTER TABLE mz_inv_movements DROP COLUMN warehouse_id, DROP COLUMN lot_id;
ALTER TABLE mz_inv_movements DROP CONSTRAINT mz_inv_movements_kind_check;
ALTER TABLE mz_inv_movements ADD CONSTRAINT mz_inv_movements_kind_check CHECK (kind IN ('receipt', 'issue', 'count_gain', 'count_loss', 'write_down', 'write_down_reversal'));
ALTER TABLE mz_inv_layers DROP COLUMN warehouse_id, DROP COLUMN lot_no, DROP COLUMN expires_on;
DROP TABLE mz_inv_stock, mz_inv_warehouses;
ALTER TABLE mz_inv_items DROP COLUMN fefo;
ALTER TABLE mz_inv_items DROP CONSTRAINT mz_inv_items_method_check;
ALTER TABLE mz_inv_items ADD CONSTRAINT mz_inv_items_method_check CHECK (method IN ('fifo', 'weighted_average'));
