-- +goose Up
ALTER TABLE shop_orders ADD COLUMN gateway_config text NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE shop_orders DROP COLUMN gateway_config;
