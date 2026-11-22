package orm

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
)

// Conn supplies the connection (or the transaction carried by ctx) for each statement; *postgres.DB
// implements it. Repositories therefore join a transaction opened with DB.WithTx automatically.
type Conn interface {
	Querier(ctx context.Context) postgres.Querier
}

// Repository gives typed CRUD, queries and relation loading for the row struct T. It is safe for
// concurrent use; create one per model at start-up.
type Repository[T any] struct {
	conn Conn
	m    *Model

	schemaOnce sync.Once
	schema     *query.Schema
	schemaErr  error
	schemaOpts []query.Option
}

// Option configures a Repository.
type Option func(*repoConfig)

type repoConfig struct{ schemaOpts []query.Option }

// WithSchemaOptions adds options for the query.Schema derived from the model (default sort, page sizes,
// allow / deny lists for client-driven filtering).
func WithSchemaOptions(opts ...query.Option) Option {
	return func(c *repoConfig) { c.schemaOpts = append(c.schemaOpts, opts...) }
}

// New builds a repository and validates the model.
func New[T any](conn Conn, opts ...Option) (*Repository[T], error) {
	m, err := ModelOf[T]()
	if err != nil {
		return nil, err
	}
	var cfg repoConfig
	for _, o := range opts {
		o(&cfg)
	}
	return &Repository[T]{conn: conn, m: m, schemaOpts: cfg.schemaOpts}, nil
}

// Model returns the metadata of T.
func (r *Repository[T]) Model() *Model { return r.m }

func (r *Repository[T]) notFound() error {
	return apperr.NotFound(strings.ReplaceAll(r.m.Name, "_", " "))
}

// Get loads one row by primary key (soft-deleted rows are not found).
func (r *Repository[T]) Get(ctx context.Context, id ...any) (T, error) {
	var zero T
	if len(id) != len(r.m.PK) {
		return zero, fmt.Errorf("orm: %s has a %d-column primary key, Get got %d values", r.m.Type.Name(), len(r.m.PK), len(id))
	}
	rows, err := r.conn.Querier(ctx).Query(ctx, r.m.cache.getByPK, id...)
	if err != nil {
		return zero, postgres.MapError(err)
	}
	items, err := scanAll[T](rows, r.m.Fields, 1)
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, r.notFound()
	}
	return items[0], nil
}

// Create inserts v and reads back database-generated values (identity, defaults, timestamps).
func (r *Repository[T]) Create(ctx context.Context, v *T) error {
	rv := reflect.ValueOf(v).Elem()
	plan := r.m.insertPlan(r.m.insertMask(rv))
	args := make([]any, len(plan.args))
	for i, f := range plan.args {
		args[i] = rv.FieldByIndex(f.Index).Interface()
	}
	q := r.conn.Querier(ctx)
	if len(plan.returning) == 0 {
		_, err := postgres.Exec(ctx, q, plan.sql, args...)
		return err
	}
	return postgres.MapError(scanReturning(q.QueryRow(ctx, plan.sql, args...), rv, plan.returning))
}

const bulkChunk = 5000

// CreateMany inserts all rows and writes the generated values back into items. Models whose columns are
// all scalar use one unnest-based statement per 5000 rows (one parse and one round trip however many
// rows). Models with array columns (which unnest would flatten) use a pgx batch instead: still one
// round trip, one statement per row.
func (r *Repository[T]) CreateMany(ctx context.Context, items []T) error {
	if len(items) == 0 {
		return nil
	}
	q := r.conn.Querier(ctx)
	if r.m.hasArrayColumn() {
		return r.createBatch(ctx, q, items)
	}
	sql, cols, returning := r.m.bulkInsertSQL()
	for start := 0; start < len(items); start += bulkChunk {
		chunk := items[start:min(start+bulkChunk, len(items))]
		args := make([]any, len(cols))
		for i, f := range cols {
			args[i] = columnArray(f, chunk)
		}
		rows, err := q.Query(ctx, sql, args...)
		if err != nil {
			return postgres.MapError(err)
		}
		i := 0
		dest := make([]any, len(returning))
		for rows.Next() {
			if i >= len(chunk) {
				rows.Close()
				return errors.New("orm: bulk insert returned more rows than were sent")
			}
			elem := reflect.ValueOf(&chunk[i]).Elem()
			for j, f := range returning {
				dest[j] = elem.FieldByIndex(f.Index).Addr().Interface()
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return postgres.MapError(err)
			}
			i++
		}
		if err := rows.Err(); err != nil {
			return postgres.MapError(err)
		}
		if i != len(chunk) {
			return fmt.Errorf("orm: bulk insert returned %d rows for %d sent", i, len(chunk))
		}
	}
	return nil
}

// columnArray collects one column of the chunk as a typed slice. Columns with a database default become
// pointer slices so a zero value can be sent as NULL and replaced by the default (COALESCE).
func columnArray[T any](f *Field, items []T) any {
	elemType := f.Type
	asPtr := f.hasDefault() && elemType.Kind() != reflect.Pointer
	if asPtr {
		elemType = reflect.PointerTo(elemType)
	}
	out := reflect.MakeSlice(reflect.SliceOf(elemType), len(items), len(items))
	for i := range items {
		fv := reflect.ValueOf(&items[i]).Elem().FieldByIndex(f.Index)
		switch {
		case asPtr && fv.IsZero():
			// leave nil: NULL -> default
		case asPtr:
			p := reflect.New(f.Type)
			p.Elem().Set(fv)
			out.Index(i).Set(p)
		case f.hasDefault() && fv.IsZero():
			// nullable pointer column with a default: nil means "use the default"
		default:
			out.Index(i).Set(fv)
		}
	}
	return out.Interface()
}

// Update writes v back by primary key. Without fields it writes every updatable column; with fields only
// those (Go field or column names). updated_at is set by the database and a version column is checked
// and incremented (a stale version is an apperr.KindConflict error).
func (r *Repository[T]) Update(ctx context.Context, v *T, fields ...string) error {
	set := r.m.cache.updatable
	if len(fields) > 0 {
		set = make([]*Field, 0, len(fields))
		for _, name := range fields {
			f, ok := r.m.Field(name)
			if !ok || f.PK || f.Auto || f.ReadOnly {
				return fmt.Errorf("orm: %s: cannot update column %q", r.m.Type.Name(), name)
			}
			set = append(set, f)
		}
	}
	plan := r.m.updatePlan(set)
	rv := reflect.ValueOf(v).Elem()

	args := make([]any, 0, len(plan.set)+len(r.m.PK)+1)
	for _, f := range plan.set {
		args = append(args, rv.FieldByIndex(f.Index).Interface())
	}
	for _, f := range r.m.PK {
		args = append(args, rv.FieldByIndex(f.Index).Interface())
	}
	if r.m.Version != nil {
		args = append(args, rv.FieldByIndex(r.m.Version.Index).Interface())
	}

	q := r.conn.Querier(ctx)
	if len(plan.returning) == 0 {
		n, err := postgres.Exec(ctx, q, plan.sql, args...)
		if err != nil {
			return err
		}
		if n == 0 {
			return r.notFound()
		}
		return nil
	}
	err := scanReturning(q.QueryRow(ctx, plan.sql, args...), rv, plan.returning)
	if err != nil {
		if apperr.Is(postgres.MapError(err), apperr.KindNotFound) {
			if r.m.Version != nil {
				return apperr.New(apperr.KindConflict, "stale_version", "the "+strings.ReplaceAll(r.m.Name, "_", " ")+" was modified by someone else")
			}
			return r.notFound()
		}
		return postgres.MapError(err)
	}
	return nil
}

// Delete removes a row by primary key; it only marks the row when the model has a soft_delete field.
func (r *Repository[T]) Delete(ctx context.Context, id ...any) error {
	sql := r.m.cache.deleteByPK
	if r.m.SoftDelete != nil {
		sql = r.m.cache.softDeleteByPK
	}
	return r.deleteBy(ctx, sql, id)
}

// HardDelete removes the row for good, soft-deleted or not.
func (r *Repository[T]) HardDelete(ctx context.Context, id ...any) error {
	return r.deleteBy(ctx, r.m.cache.deleteByPK, id)
}

func (r *Repository[T]) deleteBy(ctx context.Context, sql string, id []any) error {
	if len(id) != len(r.m.PK) {
		return fmt.Errorf("orm: %s has a %d-column primary key, got %d values", r.m.Type.Name(), len(r.m.PK), len(id))
	}
	n, err := postgres.Exec(ctx, r.conn.Querier(ctx), sql, id...)
	if err != nil {
		return err
	}
	if n == 0 {
		return r.notFound()
	}
	return nil
}

// Schema returns the query.Schema derived from the model: what clients may filter and sort on. Derive
// stricter per-request variants with schema.Restrict.
func (r *Repository[T]) Schema() (*query.Schema, error) {
	r.schemaOnce.Do(func() { r.schema, r.schemaErr = buildSchema(r.m, r.schemaOpts) })
	return r.schema, r.schemaErr
}

func buildSchema(m *Model, extra []query.Option) (*query.Schema, error) {
	var fields []query.Field
	for _, f := range m.Fields {
		if f.NoQuery {
			continue
		}
		t, ok := queryType(f)
		if !ok {
			continue
		}
		fields = append(fields, query.Field{Name: f.APIName, Column: m.quotedTable + "." + f.quoted, Type: t, NoFilter: f.NoFilter, NoSort: f.NoSort})
	}
	// Defaults first so the caller's options win: newest first when there is a created_at, else by key.
	opts := []query.Option{query.WithTieBreaker(m.quotedTable + "." + m.PK[0].quoted), query.WithPageSize(20, 100)}
	if m.CreatedAt != nil {
		opts = append(opts, query.WithDefaultSort(query.Sort{Field: m.CreatedAt.APIName, Desc: true}))
	} else {
		opts = append(opts, query.WithDefaultSort(query.Sort{Field: m.PK[0].APIName}))
	}
	return query.NewSchema(fields, append(opts, extra...)...)
}

func queryType(f *Field) (query.Type, bool) {
	t := f.Type
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case timeType:
		return query.TypeTime, true
	case uuidType:
		return query.TypeUUID, true
	}
	switch t.Kind() {
	case reflect.String:
		return query.TypeString, true
	case reflect.Bool:
		return query.TypeBool, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return query.TypeInt, true
	case reflect.Float32, reflect.Float64:
		return query.TypeFloat, true
	}
	return 0, false
}

// batcher is implemented by pgxpool.Pool and pgx.Tx.
type batcher interface {
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

func (r *Repository[T]) createBatch(ctx context.Context, q postgres.Querier, items []T) error {
	bq, ok := q.(batcher)
	if !ok {
		return errors.New("orm: the connection does not support batches")
	}
	for start := 0; start < len(items); start += bulkChunk {
		chunk := items[start:min(start+bulkChunk, len(items))]
		batch := &pgx.Batch{}
		plans := make([]*insertPlan, len(chunk))
		for i := range chunk {
			rv := reflect.ValueOf(&chunk[i]).Elem()
			plan := r.m.insertPlan(r.m.insertMask(rv))
			plans[i] = plan
			args := make([]any, len(plan.args))
			for j, f := range plan.args {
				args[j] = rv.FieldByIndex(f.Index).Interface()
			}
			batch.Queue(plan.sql, args...)
		}
		br := bq.SendBatch(ctx, batch)
		for i := range chunk {
			rv := reflect.ValueOf(&chunk[i]).Elem()
			var err error
			if len(plans[i].returning) == 0 {
				_, err = br.Exec()
			} else {
				err = scanReturning(br.QueryRow(), rv, plans[i].returning)
			}
			if err != nil {
				_ = br.Close()
				return postgres.MapError(err)
			}
		}
		if err := br.Close(); err != nil {
			return postgres.MapError(err)
		}
	}
	return nil
}
