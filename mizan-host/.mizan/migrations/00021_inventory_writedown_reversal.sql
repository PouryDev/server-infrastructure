-- +goose Up
-- Reversal of an inventory write-down (accounting standard 8, para. 46: a rise of the net realizable value is recognised, at most up to the earlier reduction).
ALTER TABLE mz_inv_items ADD COLUMN writedown_balance bigint NOT NULL DEFAULT 0 CHECK (writedown_balance >= 0);
ALTER TABLE mz_inv_movements DROP CONSTRAINT mz_inv_movements_kind_check;
ALTER TABLE mz_inv_movements ADD CONSTRAINT mz_inv_movements_kind_check CHECK (kind IN ('receipt', 'issue', 'count_gain', 'count_loss', 'write_down', 'write_down_reversal'));

-- +goose Down
ALTER TABLE mz_inv_movements DROP CONSTRAINT mz_inv_movements_kind_check;
ALTER TABLE mz_inv_movements ADD CONSTRAINT mz_inv_movements_kind_check CHECK (kind IN ('receipt', 'issue', 'count_gain', 'count_loss', 'write_down'));
ALTER TABLE mz_inv_items DROP COLUMN writedown_balance;
