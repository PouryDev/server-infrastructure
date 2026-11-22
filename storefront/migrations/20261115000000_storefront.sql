-- +goose Up
CREATE TABLE shop_settings (id boolean PRIMARY KEY DEFAULT true CHECK(id), revision integer NOT NULL DEFAULT 1, data jsonb NOT NULL);
CREATE TABLE shop_users (id text PRIMARY KEY, phone text UNIQUE NOT NULL CHECK(phone ~ '^\+989[0-9]{9}$'), name text NOT NULL DEFAULT '', role text NOT NULL DEFAULT 'customer' CHECK(role IN ('customer','seller','admin')), password_hash text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE shop_sessions (token_hash text PRIMARY KEY, user_id text NOT NULL REFERENCES shop_users(id), csrf text NOT NULL, expires_at timestamptz NOT NULL);
CREATE INDEX ON shop_sessions(expires_at);
CREATE TABLE shop_otp (phone text PRIMARY KEY, code_hash text NOT NULL, attempts integer NOT NULL DEFAULT 0, sent_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL);
CREATE TABLE shop_rate_limits (key text PRIMARY KEY, hits integer NOT NULL, resets_at timestamptz NOT NULL);
CREATE TABLE shop_products (id text PRIMARY KEY, slug text UNIQUE NOT NULL, data jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE shop_variants (id text PRIMARY KEY, product_id text NOT NULL REFERENCES shop_products(id), price bigint NOT NULL CHECK(price>=10000), stock integer NOT NULL CHECK(stock>=0));
CREATE TABLE shop_campaigns (id text PRIMARY KEY, code text NOT NULL, data jsonb NOT NULL);
CREATE UNIQUE INDEX ON shop_campaigns(lower(code)) WHERE code<>'';
CREATE TABLE shop_orders (id text PRIMARY KEY, user_id text NOT NULL REFERENCES shop_users(id), request_key text NOT NULL, status text NOT NULL, payment_status text NOT NULL, gateway text NOT NULL, authority text NOT NULL DEFAULT '', total bigint NOT NULL CHECK(total>=0), data jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL, UNIQUE(user_id,request_key));
CREATE INDEX ON shop_orders(user_id,created_at DESC);
CREATE INDEX ON shop_orders(expires_at) WHERE payment_status='pending';
CREATE UNIQUE INDEX ON shop_orders(gateway,authority) WHERE authority<>'';
CREATE TABLE shop_redemptions (order_id text NOT NULL REFERENCES shop_orders(id), campaign_id text NOT NULL REFERENCES shop_campaigns(id), user_id text NOT NULL REFERENCES shop_users(id), PRIMARY KEY(order_id,campaign_id));
CREATE INDEX ON shop_redemptions(campaign_id,user_id);
CREATE TABLE shop_uploads (id text PRIMARY KEY, user_id text NOT NULL REFERENCES shop_users(id), order_id text REFERENCES shop_orders(id), data jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE shop_articles (id text PRIMARY KEY, slug text UNIQUE NOT NULL, data jsonb NOT NULL);
CREATE TABLE shop_events (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, kind text NOT NULL, product_id text NOT NULL DEFAULT '', session text NOT NULL, source text NOT NULL DEFAULT '', campaign text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX ON shop_events(created_at,kind);
CREATE TABLE shop_audit (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, actor text NOT NULL, action text NOT NULL, subject text NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
-- +goose Down
DROP TABLE shop_audit,shop_events,shop_articles,shop_uploads,shop_redemptions,shop_orders,shop_campaigns,shop_variants,shop_products,shop_rate_limits,shop_otp,shop_sessions,shop_users,shop_settings;
