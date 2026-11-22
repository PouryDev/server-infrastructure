package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/PouryDev/storefront/internal/domain/shop"
)

func (s *Store) FeedProducts(ctx context.Context, q shop.FeedQuery) (shop.FeedPage, error) {
	out := shop.FeedPage{Products: []shop.Product{}}
	// Joining only variants still present in product JSON excludes removed models.
	from := ` FROM shop_products p JOIN shop_variants v ON v.product_id=p.id
 JOIN LATERAL jsonb_array_elements(p.data->'variants') j ON j->>'id'=v.id
 WHERE p.data->>'active'='true'
 AND ($1::text[] IS NULL OR v.id=ANY($1))
 AND ($2::text[] IS NULL OR EXISTS(SELECT 1 FROM unnest($2::text[],$3::text[]) AS lookup(slug,variant) WHERE p.slug=lookup.slug AND (lookup.variant='' OR v.id=lookup.variant)))`
	args := []any{q.IDs, q.Slugs, q.VariantIDs}
	if err := s.DB.Querier(ctx).QueryRow(ctx, "SELECT count(*)"+from, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	order := "p.created_at DESC,v.id DESC"
	if q.Sort == "date_updated_desc" {
		order = "p.updated_at DESC,v.id DESC"
	}
	if q.Sort == "product_id_desc" {
		order = "v.id DESC"
	}
	rows, err := s.DB.Querier(ctx).Query(ctx, `SELECT p.data,p.created_at,p.updated_at,j,v.price,v.stock`+from+` AND ($4='' OR v.id<$4) ORDER BY `+order+` LIMIT 100 OFFSET $5`, append(args, q.BeforeID, q.Offset)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p shop.Product
		var v shop.Variant
		var data, model []byte
		var created, updated time.Time
		var price int64
		var stock int
		if err = rows.Scan(&data, &created, &updated, &model, &price, &stock); err != nil {
			return out, err
		}
		if err = json.Unmarshal(data, &p); err != nil {
			return out, err
		}
		if err = json.Unmarshal(model, &v); err != nil {
			return out, err
		}
		v.Price = price
		v.Stock = stock
		p.Variants = []shop.Variant{v}
		p.CreatedAt = created
		p.UpdatedAt = updated
		out.Products = append(out.Products, p)
	}
	return out, rows.Err()
}
func (s *Store) LeaseSync(ctx context.Context) (shop.SyncJob, error) {
	var out shop.SyncJob
	err := s.DB.Querier(ctx).QueryRow(ctx, `WITH gate AS (UPDATE shop_sync_status SET next_dispatch_at=clock_timestamp()+interval '4 seconds' WHERE id=true AND next_dispatch_at<=clock_timestamp() RETURNING id)
 UPDATE shop_product_sync SET available_at=now()+interval '2 minutes'
 WHERE product_id=(SELECT product_id FROM shop_product_sync WHERE available_at<=now() AND EXISTS(SELECT 1 FROM gate) ORDER BY available_at,product_id FOR UPDATE SKIP LOCKED LIMIT 1)
 RETURNING product_id,generation,attempts`).Scan(&out.ProductID, &out.Generation, &out.Attempts)
	return out, missing(err)
}
func (s *Store) FinishSync(ctx context.Context, j shop.SyncJob, success bool) error {
	if !success {
		_, err := s.DB.Querier(ctx).Exec(ctx, `UPDATE shop_product_sync SET attempts=attempts+1,available_at=now()+make_interval(secs=>LEAST(3600,30*power(2,LEAST(attempts,7)))::int) WHERE product_id=$1 AND generation=$2`, j.ProductID, j.Generation)
		return err
	}
	return s.Atomic(ctx, func(ctx context.Context) error {
		if _, err := s.DB.Querier(ctx).Exec(ctx, `DELETE FROM shop_product_sync WHERE product_id=$1 AND generation=$2`, j.ProductID, j.Generation); err != nil {
			return err
		}
		_, err := s.DB.Querier(ctx).Exec(ctx, `UPDATE shop_sync_status SET last_success=now() WHERE id=true`)
		return err
	})
}
func (s *Store) SyncStatus(ctx context.Context) (shop.SyncStatus, error) {
	var out shop.SyncStatus
	err := s.DB.Querier(ctx).QueryRow(ctx, `SELECT (SELECT count(*) FROM shop_product_sync),last_success FROM shop_sync_status WHERE id=true`).Scan(&out.Pending, &out.LastSuccess)
	return out, err
}
