-- +goose Up
CREATE TABLE shop_redirects (kind text NOT NULL CHECK(kind IN ('product','article')),old_slug text NOT NULL,target_id text NOT NULL,PRIMARY KEY(kind,old_slug));
CREATE INDEX ON shop_orders(created_at DESC);
CREATE INDEX ON shop_uploads(created_at) WHERE order_id IS NULL;
-- +goose Down
DROP TABLE shop_redirects;
