-- +goose Up
ALTER TABLE shop_products ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
UPDATE shop_products SET created_at=updated_at;
CREATE INDEX shop_products_created_feed ON shop_products(created_at DESC,id);
CREATE INDEX shop_products_updated_feed ON shop_products(updated_at DESC,id);
CREATE TABLE shop_product_sync (
 product_id text PRIMARY KEY REFERENCES shop_products(id) ON DELETE CASCADE,
 generation bigint NOT NULL DEFAULT 1,
 attempts integer NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX shop_product_sync_due ON shop_product_sync(available_at);
CREATE TABLE shop_sync_status (id boolean PRIMARY KEY DEFAULT true CHECK(id),last_success timestamptz,next_dispatch_at timestamptz NOT NULL DEFAULT now());
INSERT INTO shop_sync_status(id) VALUES(true);
-- +goose StatementBegin
CREATE FUNCTION shop_enqueue_product_sync() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target text;
BEGIN
 IF TG_TABLE_NAME='shop_variants' THEN
  target=NEW.product_id;
  UPDATE shop_products SET updated_at=clock_timestamp() WHERE id=target;
 ELSE
  target=NEW.id;
  INSERT INTO shop_product_sync(product_id) VALUES(target)
  ON CONFLICT(product_id) DO UPDATE SET generation=shop_product_sync.generation+1,attempts=0,available_at=now();
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER shop_product_sync_change AFTER INSERT OR UPDATE ON shop_products FOR EACH ROW EXECUTE FUNCTION shop_enqueue_product_sync();
CREATE TRIGGER shop_variant_sync_change AFTER INSERT OR UPDATE OF stock,price ON shop_variants FOR EACH ROW EXECUTE FUNCTION shop_enqueue_product_sync();
INSERT INTO shop_product_sync(product_id) SELECT id FROM shop_products;
-- +goose Down
DROP TRIGGER shop_variant_sync_change ON shop_variants;
DROP TRIGGER shop_product_sync_change ON shop_products;
DROP FUNCTION shop_enqueue_product_sync();
DROP TABLE shop_product_sync,shop_sync_status;
DROP INDEX shop_products_created_feed;
DROP INDEX shop_products_updated_feed;
ALTER TABLE shop_products DROP COLUMN created_at;
