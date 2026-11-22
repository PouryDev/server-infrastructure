// Package store implements persistence with PostgreSQL through the shared PgBouncer pool.
package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	kitpg "github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/jackc/pgx/v5"
	"time"
)

type Store struct {
	DB     *kitpg.DB
	Secret []byte
}

func (s *Store) crypt(value string, encrypt bool) (string, error) {
	if value == "" {
		return "", nil
	}
	key := sha256.Sum256(s.Secret)
	block, e := aes.NewCipher(key[:])
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	if encrypt {
		nonce := make([]byte, g.NonceSize())
		if _, e = rand.Read(nonce); e != nil {
			return "", e
		}
		return base64.StdEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(value), []byte("storefront.gateway.v1"))), nil
	}
	raw, e := base64.StdEncoding.DecodeString(value)
	if e != nil || len(raw) < g.NonceSize() {
		return "", shop.ErrInvalid
	}
	out, e := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte("storefront.gateway.v1"))
	return string(out), e
}
func (s *Store) Atomic(ctx context.Context, fn func(context.Context) error) error {
	return s.DB.WithTx(ctx, fn)
}
func missing(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return shop.ErrNotFound
	}
	return err
}
func read[T any](ctx context.Context, s *Store, sql string, args ...any) (T, error) {
	var out T
	var raw []byte
	e := s.DB.Querier(ctx).QueryRow(ctx, sql, args...).Scan(&raw)
	if e != nil {
		return out, missing(e)
	}
	e = json.Unmarshal(raw, &out)
	return out, e
}
func list[T any](ctx context.Context, s *Store, sql string, args ...any) ([]T, error) {
	out := []T{}
	rows, e := s.DB.Querier(ctx).Query(ctx, sql, args...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var item T
		if e = rows.Scan(&raw); e != nil {
			return out, e
		}
		if e = json.Unmarshal(raw, &item); e != nil {
			return out, e
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) Settings(ctx context.Context, lock bool) (shop.Settings, error) {
	sql := "SELECT data FROM shop_settings WHERE id=true"
	if lock {
		sql += " FOR UPDATE"
	}
	v, e := read[shop.Settings](ctx, s, sql)
	if e != nil {
		return v, e
	}
	for i := range v.Gateways {
		v.Gateways[i].Credential, e = s.crypt(v.Gateways[i].Credential, false)
		if e != nil {
			return v, e
		}
	}
	v.Torob.WebhookToken, e = s.crypt(v.Torob.WebhookToken, false)
	return v, e
}
func (s *Store) SaveSettings(ctx context.Context, v shop.Settings) error {
	v.Gateways = append([]shop.Gateway(nil), v.Gateways...)
	for i := range v.Gateways {
		var e error
		v.Gateways[i].Credential, e = s.crypt(v.Gateways[i].Credential, true)
		if e != nil {
			return e
		}
	}
	token, err := s.crypt(v.Torob.WebhookToken, true)
	if err != nil {
		return err
	}
	v.Torob.WebhookToken = token
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_settings(id,revision,data) VALUES(true,$1,$2) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,data=excluded.data", v.Revision, b)
	return e
}
func (s *Store) Products(ctx context.Context) ([]shop.Product, error) {
	ps, e := list[shop.Product](ctx, s, "SELECT data FROM shop_products ORDER BY updated_at DESC,id LIMIT 500")
	if e != nil {
		return nil, e
	}
	rows, e := s.DB.Querier(ctx).Query(ctx, "SELECT id,price,stock FROM shop_variants")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	type state struct {
		price int64
		stock int
	}
	vs := map[string]state{}
	for rows.Next() {
		var id string
		var st state
		if e = rows.Scan(&id, &st.price, &st.stock); e != nil {
			return nil, e
		}
		vs[id] = st
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	for i := range ps {
		for j, v := range ps[i].Variants {
			if st, ok := vs[v.ID]; ok {
				ps[i].Variants[j].Stock = st.stock
				ps[i].Variants[j].Price = st.price
			}
		}
	}
	return ps, nil
}
func (s *Store) Product(ctx context.Context, id string) (shop.Product, error) {
	p, e := read[shop.Product](ctx, s, "SELECT data FROM shop_products WHERE id=$1", id)
	if e != nil {
		return p, e
	}
	rows, e := s.DB.Querier(ctx).Query(ctx, "SELECT id,price,stock FROM shop_variants WHERE product_id=$1", p.ID)
	if e != nil {
		return p, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var price int64
		var stock int
		if e = rows.Scan(&id, &price, &stock); e != nil {
			return p, e
		}
		for i := range p.Variants {
			if p.Variants[i].ID == id {
				p.Variants[i].Stock = stock
				p.Variants[i].Price = price
			}
		}
	}
	return p, rows.Err()
}
func (s *Store) SaveProduct(ctx context.Context, p shop.Product) error {
	var old string
	err := s.DB.Querier(ctx).QueryRow(ctx, "SELECT slug FROM shop_products WHERE id=$1", p.ID).Scan(&old)
	if err != nil && missing(err) != shop.ErrNotFound {
		return err
	}
	if old != "" && old != p.Slug {
		if _, err = s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_redirects(kind,old_slug,target_id) VALUES('product',$1,$2) ON CONFLICT(kind,old_slug) DO UPDATE SET target_id=excluded.target_id", old, p.ID); err != nil {
			return err
		}
	}

	b, e := json.Marshal(p)
	if e != nil {
		return e
	}
	_, e = s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_products(id,slug,data) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET slug=excluded.slug,data=excluded.data,updated_at=now()", p.ID, p.Slug, b)
	if e != nil {
		return e
	}
	for _, v := range p.Variants {
		delta := 0
		if v.StockChange != nil {
			delta = *v.StockChange
		}
		tag, err := s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_variants(id,product_id,price,stock) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET price=excluded.price,stock=shop_variants.stock+$5 WHERE shop_variants.product_id=excluded.product_id AND shop_variants.stock+$5>=0", v.ID, p.ID, v.Price, v.Stock, delta)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return shop.ErrConflict
		}
	}
	return nil
}
func (s *Store) Reserve(ctx context.Context, id string, n int) error {
	tag, e := s.DB.Querier(ctx).Exec(ctx, "UPDATE shop_variants SET stock=stock-$2 WHERE id=$1 AND stock >= $2", id, n)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return shop.ErrConflict
	}
	return nil
}
func (s *Store) Release(ctx context.Context, id string, n int) error {
	_, e := s.DB.Querier(ctx).Exec(ctx, "UPDATE shop_variants SET stock=stock+$2 WHERE id=$1", id, n)
	return e
}
func (s *Store) Campaigns(ctx context.Context, lock bool) ([]shop.Discount, error) {
	sql := "SELECT data FROM shop_campaigns ORDER BY id"
	if lock {
		sql += " FOR UPDATE"
	}
	return list[shop.Discount](ctx, s, sql)
}
func (s *Store) SaveCampaign(ctx context.Context, d shop.Discount) error {
	b, e := json.Marshal(d)
	if e != nil {
		return e
	}
	_, e = s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_campaigns(id,code,data) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET code=excluded.code,data=excluded.data", d.ID, d.Code, b)
	return e
}
func (s *Store) Uses(ctx context.Context, id, user string) (int, int, int, error) {
	var all, own, prior int
	e := s.DB.Querier(ctx).QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE r.user_id=$2),(SELECT count(*) FROM shop_orders WHERE user_id=$2 AND payment_status='paid') FROM shop_redemptions r JOIN shop_orders o ON o.id=r.order_id WHERE campaign_id=$1 AND (o.payment_status='paid' OR o.status='awaiting_payment')`, id, user).Scan(&all, &own, &prior)
	return all, own, prior, e
}
func (s *Store) Upload(ctx context.Context, id string) (shop.Upload, error) {
	return read[shop.Upload](ctx, s, "SELECT data FROM shop_uploads WHERE id=$1 AND order_id IS NULL", id)
}
func (s *Store) SaveUpload(ctx context.Context, u shop.Upload) error {
	b, e := json.Marshal(u)
	if e != nil {
		return e
	}
	_, e = s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_uploads(id,user_id,data) VALUES($1,$2,$3)", u.ID, u.UserID, b)
	return e
}
func (s *Store) BindUpload(ctx context.Context, id, user, order string) error {
	tag, e := s.DB.Querier(ctx).Exec(ctx, "UPDATE shop_uploads SET order_id=$3 WHERE id=$1 AND user_id=$2 AND order_id IS NULL", id, user, order)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return shop.ErrConflict
	}
	return nil
}
func (s *Store) Order(ctx context.Context, id string, lock bool) (shop.Order, error) {
	query := "SELECT data,request_key,gateway_config FROM shop_orders WHERE id=$1"
	if lock {
		query += " FOR UPDATE"
	}
	var raw []byte
	var secret string
	var o shop.Order
	e := s.DB.Querier(ctx).QueryRow(ctx, query, id).Scan(&raw, &o.Key, &secret)
	if e != nil {
		return o, missing(e)
	}
	key := o.Key
	if e = json.Unmarshal(raw, &o); e != nil {
		return o, e
	}
	o.Key = key
	if secret != "" {
		plain, err := s.crypt(secret, false)
		if err != nil {
			return o, err
		}
		if err = json.Unmarshal([]byte(plain), &o.GatewayConfig); err != nil {
			return o, err
		}
	}
	return o, nil
}
func (s *Store) OrderByKey(ctx context.Context, user, key string) (shop.Order, error) {
	return read[shop.Order](ctx, s, "SELECT data FROM shop_orders WHERE user_id=$1 AND request_key=$2", user, key)
}
func (s *Store) Orders(ctx context.Context, user string) ([]shop.Order, error) {
	return list[shop.Order](ctx, s, "SELECT data FROM shop_orders WHERE ($1='' OR user_id=$1) ORDER BY created_at DESC LIMIT 250", user)
}
func (s *Store) SaveOrder(ctx context.Context, o shop.Order) error {
	b, e := json.Marshal(o)
	if e != nil {
		return e
	}
	configRaw, e := json.Marshal(o.GatewayConfig)
	if e != nil {
		return e
	}
	secret, e := s.crypt(string(configRaw), true)
	if e != nil {
		return e
	}
	_, e = s.DB.Querier(ctx).Exec(ctx, `INSERT INTO shop_orders(id,user_id,request_key,status,payment_status,gateway,authority,total,data,created_at,expires_at,gateway_config) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(id) DO UPDATE SET status=excluded.status,payment_status=excluded.payment_status,authority=excluded.authority,data=excluded.data,gateway_config=excluded.gateway_config`, o.ID, o.UserID, o.Key, o.Status, o.PaymentStatus, o.Gateway, o.Authority, o.Quote.Total, b, o.CreatedAt, o.ExpiresAt, secret)
	return e
}
func (s *Store) Redeem(ctx context.Context, order, campaign, user string) error {
	_, e := s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_redemptions(order_id,campaign_id,user_id) VALUES($1,$2,$3)", order, campaign, user)
	return e
}
func (s *Store) Expired(ctx context.Context, at time.Time) ([]string, error) {
	rows, e := s.DB.Querier(ctx).Query(ctx, "SELECT id FROM shop_orders WHERE status='awaiting_payment' AND payment_status='pending' AND expires_at<$1 LIMIT 100", at)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s *Store) Articles(ctx context.Context) ([]shop.Article, error) {
	return list[shop.Article](ctx, s, "SELECT data FROM shop_articles ORDER BY id LIMIT 200")
}
func (s *Store) SaveArticle(ctx context.Context, a shop.Article) error {
	var old string
	err := s.DB.Querier(ctx).QueryRow(ctx, "SELECT slug FROM shop_articles WHERE id=$1", a.ID).Scan(&old)
	if err != nil && missing(err) != shop.ErrNotFound {
		return err
	}
	if old != "" && old != a.Slug {
		if _, err = s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_redirects(kind,old_slug,target_id) VALUES('article',$1,$2) ON CONFLICT(kind,old_slug) DO UPDATE SET target_id=excluded.target_id", old, a.ID); err != nil {
			return err
		}
	}

	b, e := json.Marshal(a)
	if e != nil {
		return e
	}
	_, e = s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_articles(id,slug,data) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET slug=excluded.slug,data=excluded.data", a.ID, a.Slug, b)
	return e
}
func (s *Store) Event(ctx context.Context, v shop.Event) error {
	_, e := s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_events(kind,product_id,session,source,campaign) VALUES($1,$2,$3,$4,$5)", v.Kind, v.ProductID, v.Session, v.Source, v.Campaign)
	return e
}
func (s *Store) Audit(ctx context.Context, actor, action, subject string) error {
	_, e := s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_audit(actor,action,subject) VALUES($1,$2,$3)", actor, action, subject)
	return e
}
func (s *Store) Analytics(ctx context.Context, since time.Time) (shop.Analytics, error) {
	a := shop.Analytics{Daily: []shop.Metric{}, Products: []shop.Metric{}, Campaigns: []shop.Metric{}, Gateways: []shop.Metric{}}
	e := s.DB.Querier(ctx).QueryRow(ctx, "SELECT count(DISTINCT session),count(*) FILTER(WHERE kind='page_view'),count(*) FILTER(WHERE kind='add_to_cart'),count(*) FILTER(WHERE kind='begin_checkout') FROM shop_events WHERE created_at>=$1", since).Scan(&a.Visitors, &a.Views, &a.Adds, &a.Checkouts)
	if e != nil {
		return a, e
	}
	e = s.DB.Querier(ctx).QueryRow(ctx, "SELECT count(*),count(*) FILTER(WHERE payment_status='paid'),coalesce(sum(total) FILTER(WHERE payment_status='paid'),0),coalesce(sum((data->'quote'->>'discount')::bigint) FILTER(WHERE payment_status='paid'),0) FROM shop_orders WHERE created_at>=$1", since).Scan(&a.Orders, &a.Paid, &a.Revenue, &a.Discounts)
	if e != nil {
		return a, e
	}
	queries := []struct {
		sql string
		out *[]shop.Metric
	}{{"SELECT to_char(created_at AT TIME ZONE 'Asia/Tehran','YYYY-MM-DD'),sum(total) FROM shop_orders WHERE created_at>=$1 AND payment_status='paid' GROUP BY 1 ORDER BY 1", &a.Daily}, {"SELECT coalesce(p.data->>'name',e.product_id),count(*) FROM shop_events e LEFT JOIN shop_products p ON p.id=e.product_id WHERE e.created_at>=$1 AND kind='product_view' GROUP BY 1 ORDER BY 2 DESC LIMIT 10", &a.Products}, {"SELECT gateway,count(*) FROM shop_orders WHERE created_at>=$1 AND payment_status='paid' GROUP BY 1 ORDER BY 2 DESC", &a.Gateways}, {"SELECT coalesce(nullif(campaign,''),'مستقیم'),count(*) FROM shop_events WHERE created_at>=$1 AND kind='page_view' GROUP BY 1 ORDER BY 2 DESC LIMIT 10", &a.Campaigns}}
	for _, q := range queries {
		rows, e := s.DB.Querier(ctx).Query(ctx, q.sql, since)
		if e != nil {
			return a, e
		}
		for rows.Next() {
			var m shop.Metric
			if e = rows.Scan(&m.Label, &m.Value); e != nil {
				rows.Close()
				return a, e
			}
			*q.out = append(*q.out, m)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return a, e
		}
	}
	return a, nil
}

func (s *Store) File(ctx context.Context, id string) (shop.Upload, error) {
	return read[shop.Upload](ctx, s, "SELECT data FROM shop_uploads WHERE id=$1", id)
}
func (s *Store) Media(ctx context.Context, id string) (shop.Upload, error) {
	return read[shop.Upload](ctx, s, "SELECT data FROM shop_media WHERE id=$1", id)
}
func (s *Store) SaveMedia(ctx context.Context, a shop.Upload) error {
	b, e := json.Marshal(a)
	if e != nil {
		return e
	}
	_, e = s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_media(id,data) VALUES($1,$2)", a.ID, b)
	return e
}

func (s *Store) ProductBySlug(ctx context.Context, slug string) (shop.Product, error) {
	var id string
	e := s.DB.Querier(ctx).QueryRow(ctx, "SELECT id FROM shop_products WHERE slug=$1", slug).Scan(&id)
	if e != nil {
		return shop.Product{}, missing(e)
	}
	return s.Product(ctx, id)
}
func (s *Store) Redirect(ctx context.Context, kind, slug string) (string, error) {
	query := "SELECT p.slug FROM shop_redirects r JOIN shop_products p ON p.id=r.target_id WHERE r.kind='product' AND r.old_slug=$1 AND (p.data->>'active')::boolean"
	if kind == "article" {
		query = "SELECT a.slug FROM shop_redirects r JOIN shop_articles a ON a.id=r.target_id WHERE r.kind='article' AND r.old_slug=$1 AND (a.data->>'published')::boolean"
	}
	var target string
	e := s.DB.Querier(ctx).QueryRow(ctx, query, slug).Scan(&target)
	return target, missing(e)
}
