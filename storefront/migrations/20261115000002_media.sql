-- +goose Up
CREATE TABLE shop_media (id text PRIMARY KEY, data jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
-- +goose Down
DROP TABLE shop_media;
