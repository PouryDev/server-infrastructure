package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/migrate"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
)

// Runs against a real database when GOKIT_TEST_DATABASE_URL is set (make test-integration).
type widget struct {
	ID        uuid.UUID  `db:"id" json:"id"`
	Name      string     `db:"name" json:"name"`
	Qty       int        `db:"qty" json:"qty"`
	Price     float64    `db:"price" json:"price"`
	Active    bool       `db:"active" json:"active"`
	Secret    string     `db:"secret" json:"-" query:"-"`
	CreatedAt time.Time  `db:"created_at" json:"createdAt"`
	DeletedAt *time.Time `db:"deleted_at" json:"deletedAt"`
}

const widgetsMigration = `-- +goose Up
CREATE TABLE widgets (
  id uuid PRIMARY KEY, name text NOT NULL UNIQUE, qty int NOT NULL, price double precision NOT NULL,
  active boolean NOT NULL, secret text NOT NULL DEFAULT 'x', created_at timestamptz NOT NULL, deleted_at timestamptz);
-- +goose Down
DROP TABLE widgets;
`

func TestAgainstPostgres(t *testing.T) {
	url := os.Getenv("GOKIT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("GOKIT_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	// migrate: up, status, down, up again.
	r := migrate.Runner{URL: url, FS: fstest.MapFS{"m/20260101000000_widgets.sql": {Data: []byte(widgetsMigration)}}, Dir: "m"}
	_, _ = r.Down(ctx) // clean slate from a previous run
	if applied, err := r.Up(ctx); err != nil || len(applied) != 1 {
		t.Fatalf("up: %v %v", applied, err)
	}
	st, err := r.Status(ctx)
	if err != nil || len(st) != 1 || !st[0].Applied {
		t.Fatalf("status: %+v %v", st, err)
	}
	t.Cleanup(func() { _, _ = r.Down(ctx) })

	db, err := postgres.Connect(ctx, postgres.Config{URL: url, StartupTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := db.Querier(ctx)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]uuid.UUID, 25)
	for i := range ids {
		ids[i] = uuid.New()
		var deleted *time.Time
		if i%5 == 0 {
			d := base
			deleted = &d
		}
		_, err := postgres.Exec(ctx, q, `INSERT INTO widgets (id,name,qty,price,active,created_at,deleted_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			ids[i], fmt.Sprintf("Widget_%02d", i), i, float64(i)*1.5, i%2 == 0, base.AddDate(0, 0, i), deleted)
		if err != nil {
			t.Fatal(err)
		}
	}

	schema, err := query.NewSchemaFor[widget]("w",
		query.WithDefaultSort(query.Sort{Field: "createdAt", Desc: true}), query.WithTieBreaker("w.id"),
		query.WithPageSize(10, 20), query.WithFilterPolicy(query.DenyFields("price")))
	if err != nil {
		t.Fatal(err)
	}
	lq := postgres.ListQuery{
		Select: "SELECT w.id, w.name, w.qty, w.price, w.active, w.created_at, w.deleted_at",
		From:   "FROM widgets w", Where: "w.qty >= $1", Args: []any{0},
	}
	list := func(p query.Params, opts ...postgres.PaginateOption) query.Page[widget] {
		t.Helper()
		page, err := postgres.Paginate[widget](ctx, q, schema, lq, p, opts...)
		if err != nil {
			t.Fatalf("%+v: %v", p, err)
		}
		return page
	}

	// default: newest first, 10 per page, total counted.
	p1 := list(query.Params{})
	if len(p1.Items) != 10 || *p1.Meta.Total != 25 || *p1.Meta.TotalPages != 3 || !p1.Meta.HasNext || p1.Items[0].Qty != 24 {
		t.Fatalf("page 1: %+v", p1.Meta)
	}
	p3 := list(query.Params{Page: 3})
	if len(p3.Items) != 5 || p3.Meta.HasNext {
		t.Fatalf("page 3: %d items, next=%v", len(p3.Items), p3.Meta.HasNext)
	}
	if got := list(query.Params{}, postgres.WithoutTotal()); got.Meta.Total != nil || !got.Meta.HasNext {
		t.Fatalf("without total: %+v", got.Meta)
	}

	// operators on real column types.
	cases := map[string]struct {
		p    query.Params
		want int64
	}{
		"in uuid":      {query.Params{Filters: []query.Filter{{Field: "id", Op: query.OpIn, Values: []string{ids[1].String() + "," + ids[2].String()}}}}, 2},
		"nin uuid":     {query.Params{Filters: []query.Filter{{Field: "id", Op: query.OpNin, Values: []string{ids[1].String()}}}}, 24},
		"like":         {query.Params{Filters: []query.Filter{{Field: "name", Op: query.OpLike, Values: []string{"widget_0"}}}}, 10},
		"like escaped": {query.Params{Filters: []query.Filter{{Field: "name", Op: query.OpLike, Values: []string{"%"}}}}, 0},
		"prefix":       {query.Params{Filters: []query.Filter{{Field: "name", Op: query.OpPrefix, Values: []string{"widget_1"}}}}, 10},
		"between int":  {query.Params{Filters: []query.Filter{{Field: "qty", Op: query.OpBetween, Values: []string{"5,9"}}}}, 5},
		"gte+bool":     {query.Params{Filters: []query.Filter{{Field: "qty", Op: query.OpGte, Values: []string{"20"}}, {Field: "active", Values: []string{"true"}}}}, 3},
		"time between": {query.Params{Filters: []query.Filter{{Field: "createdAt", Op: query.OpBetween, Values: []string{"2026-01-03,2026-01-05"}}}}, 3},
		"is null":      {query.Params{Filters: []query.Filter{{Field: "deletedAt", Op: query.OpNull, Values: []string{"true"}}}}, 20},
		"not null":     {query.Params{Filters: []query.Filter{{Field: "deletedAt", Op: query.OpNull, Values: []string{"false"}}}}, 5},
		"nin ints":     {query.Params{Filters: []query.Filter{{Field: "qty", Op: query.OpNin, Values: []string{"1,2,3"}}}}, 22},
	}
	for name, c := range cases {
		got := list(c.p)
		if *got.Meta.Total != c.want {
			t.Errorf("%s: total = %d, want %d", name, *got.Meta.Total, c.want)
		}
	}

	// sort ascending by a client-chosen field; the denied filter field is rejected, the secret is invisible.
	if asc := list(query.Params{Sort: []query.Sort{{Field: "qty"}}}); asc.Items[0].Qty != 0 {
		t.Fatalf("asc first = %d", asc.Items[0].Qty)
	}
	if _, err := postgres.Paginate[widget](ctx, q, schema, lq, query.Params{Filters: []query.Filter{{Field: "price", Values: []string{"1"}}}}); apperr.KindOf(err) != apperr.KindInvalid {
		t.Fatalf("denied field: %v", err)
	}
	if _, err := postgres.Paginate[widget](ctx, q, schema, lq, query.Params{Filters: []query.Filter{{Field: "secret", Values: []string{"x"}}}}); apperr.KindOf(err) != apperr.KindInvalid {
		t.Fatalf("secret field: %v", err)
	}

	// errors.
	_, err = postgres.Exec(ctx, q, `INSERT INTO widgets (id,name,qty,price,active,created_at) VALUES ($1,'Widget_00',1,1,true,now())`, uuid.New())
	if apperr.KindOf(err) != apperr.KindConflict || !postgres.IsUniqueViolation(err, "widgets_name_key") {
		t.Fatalf("unique: %v", err)
	}
	if _, err = postgres.SelectOne[widget](ctx, q, `SELECT * FROM widgets WHERE id = $1`, uuid.New()); apperr.KindOf(err) != apperr.KindNotFound {
		t.Fatalf("not found: %v", err)
	}

	// transactions: rollback on error, join from nested calls, commit on success.
	boom := errors.New("boom")
	err = db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := postgres.Exec(ctx, db.Querier(ctx), `DELETE FROM widgets`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("tx err: %v", err)
	}
	if n, _ := postgres.Value[int64](ctx, q, `SELECT count(*) FROM widgets`); n != 25 {
		t.Fatalf("rollback failed, count = %d", n)
	}
	err = db.WithTx(ctx, func(ctx context.Context) error {
		return db.WithTx(ctx, func(ctx context.Context) error { // nested joins the outer tx
			_, err := postgres.Exec(ctx, db.Querier(ctx), `DELETE FROM widgets WHERE qty < 5`)
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := postgres.Value[int64](ctx, q, `SELECT count(*) FROM widgets`); n != 20 {
		t.Fatalf("commit failed, count = %d", n)
	}
}

// pgbouncerURL is a PgBouncer in transaction-pooling mode with prepared statements enabled, configured
// like the shared infrastructure's (make gokit-integration starts one).
func pgbouncerURL(tb testing.TB) string {
	tb.Helper()
	u := os.Getenv("GOKIT_TEST_PGBOUNCER_URL")
	if u == "" {
		tb.Skip("GOKIT_TEST_PGBOUNCER_URL not set")
	}
	return u
}

// TestThroughPgBouncer hammers a pool with both execution modes behind transaction pooling: concurrent
// goroutines, more distinct statements than the statement cache holds (forcing evictions and
// deallocations), and transactions. Any "prepared statement does not exist / already exists" error fails it.
func TestThroughPgBouncer(t *testing.T) {
	url := pgbouncerURL(t)
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			ctx := context.Background()
			db, err := postgres.Connect(ctx, postgres.Config{URL: url, MaxConns: 20, StartupTimeout: 10 * time.Second,
				PreparedStatements: prepared, StatementCacheCapacity: 32})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			errc := make(chan error, 16)
			for g := range 16 {
				go func() {
					for i := range 400 {
						n := (g*7 + i) % 90 // 90 distinct statements, cache holds 32
						sql := fmt.Sprintf("SELECT $1::int + %d", n)
						got, err := postgres.Value[int32](ctx, db.Querier(ctx), sql, int32(i))
						if err != nil {
							errc <- fmt.Errorf("query: %w", err)
							return
						}
						if got != int32(i+n) {
							errc <- fmt.Errorf("got %d, want %d", got, i+n)
							return
						}
						if i%25 == 0 {
							err := db.WithTx(ctx, func(ctx context.Context) error {
								_, err := postgres.Value[int32](ctx, db.Querier(ctx), "SELECT 1")
								return err
							})
							if err != nil {
								errc <- fmt.Errorf("tx: %w", err)
								return
							}
						}
					}
					errc <- nil
				}()
			}
			for range 16 {
				if err := <-errc; err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPgBouncerModes compares describe-per-execution with named prepared statements for the two
// shapes that matter: a point lookup and a small join-free list.
func BenchmarkPgBouncerModes(b *testing.B) {
	url := pgbouncerURL(b)
	ctx := context.Background()
	for _, prepared := range []bool{false, true} {
		db, err := postgres.Connect(ctx, postgres.Config{URL: url, MaxConns: 10, PreparedStatements: prepared})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := postgres.Exec(ctx, db.Pool(), `CREATE TABLE IF NOT EXISTS bench_items (id int PRIMARY KEY, name text NOT NULL, qty int NOT NULL)`); err != nil {
			b.Fatal(err)
		}
		_, _ = postgres.Exec(ctx, db.Pool(), `INSERT INTO bench_items SELECT g, 'item ' || g, g % 50 FROM generate_series(1, 5000) g ON CONFLICT DO NOTHING`)
		name := map[bool]string{false: "describe", true: "prepared"}[prepared]
		type item struct {
			ID   int32  `db:"id"`
			Name string `db:"name"`
			Qty  int32  `db:"qty"`
		}
		b.Run(name+"/point-lookup", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				if _, err := postgres.SelectOne[item](ctx, db.Pool(), `SELECT id, name, qty FROM bench_items WHERE id = $1`, int32(i%5000+1)); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(name+"/list-50", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				if _, err := postgres.Select[item](ctx, db.Pool(), `SELECT id, name, qty FROM bench_items WHERE qty = $1 ORDER BY id LIMIT 50`, int32(i%50)); err != nil {
					b.Fatal(err)
				}
			}
		})
		db.Close()
	}
}
