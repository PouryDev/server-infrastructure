-- +goose Up
CREATE TABLE shop_address_book (
 id text PRIMARY KEY,
 user_id text NOT NULL REFERENCES shop_users(id) ON DELETE CASCADE,
 label text NOT NULL,
 address jsonb NOT NULL,
 is_default boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON shop_address_book(user_id,updated_at DESC);
CREATE UNIQUE INDEX shop_address_default ON shop_address_book(user_id) WHERE is_default;
-- Existing shops gain a real automatic selection and category product shelves.
-- Preserve all existing section settings, content and encrypted credentials.
UPDATE shop_settings SET revision=revision+1,
 data=jsonb_set(jsonb_set(jsonb_set(data,'{revision}',to_jsonb(revision+1)),
 '{collections}',COALESCE(NULLIF(data->'collections','null'::jsonb),'[]'::jsonb) ||
 CASE WHEN EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(data->'collections','null'::jsonb),'[]'::jsonb)) c WHERE c->>'id'='store-picks') THEN '[]'::jsonb ELSE
 '[{"id":"store-picks","name":"پیشنهادهای فروشگاه","description":"منتخب کالاهای نشان‌دار و موجود؛ آمادهٔ انتخاب و سفارش","mode":"automatic","sort":"catalog","limit":8,"inStock":true,"withBadge":true,"enabled":true,"lazy":false}]'::jsonb END),
 '{sections}',
 CASE WHEN EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(data->'sections','null'::jsonb),'[]'::jsonb)) s WHERE s->>'collectionId'='store-picks') THEN '[]'::jsonb ELSE
 '[{"kind":"collection","collectionId":"store-picks","title":"پیشنهادهای فروشگاه","enabled":true}]'::jsonb END ||
 COALESCE(NULLIF(data->'sections','null'::jsonb),'[]'::jsonb) ||
 '[{"kind":"category-products","title":"خرید بر اساس دسته","enabled":true}]'::jsonb)
 WHERE NOT EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(data->'sections','null'::jsonb),'[]'::jsonb)) s WHERE s->>'kind'='category-products');
CREATE INDEX shop_customer_order_history ON shop_orders(user_id,created_at DESC);
-- +goose Down
DROP TABLE shop_address_book;
