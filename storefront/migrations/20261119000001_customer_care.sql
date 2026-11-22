-- +goose Up
CREATE TABLE shop_reviews (
 id text PRIMARY KEY,product_id text NOT NULL REFERENCES shop_products(id),user_id text NOT NULL REFERENCES shop_users(id),
 status text NOT NULL CHECK(status IN ('pending','published','hidden')),data jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(product_id,user_id)
);
CREATE INDEX shop_reviews_public ON shop_reviews(product_id,created_at DESC) WHERE status='published';
CREATE TABLE shop_returns (
 id text PRIMARY KEY,order_id text UNIQUE NOT NULL REFERENCES shop_orders(id),user_id text NOT NULL REFERENCES shop_users(id),data jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX shop_returns_customer ON shop_returns(user_id,created_at DESC);
-- +goose Down
DROP TABLE shop_returns,shop_reviews;
