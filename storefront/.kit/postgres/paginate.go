package postgres

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/PouryDev/server-infrastructure/gokit/query"
)

// ListQuery is the fixed, trusted part of a listing: written by the repository, never by a client.
// The client's filters and sorting are added by Paginate.
type ListQuery struct {
	Select string // "SELECT u.id, u.email, u.created_at"
	From   string // "FROM users u" (joins allowed)
	Where  string // optional fixed condition, e.g. "u.deleted_at IS NULL AND u.tenant_id = $1"
	Args   []any  // arguments for Where, numbered $1..$n
}

type paginateConfig struct{ withTotal bool }

// PaginateOption tunes Paginate.
type PaginateOption func(*paginateConfig)

// WithoutTotal skips the count query (cheaper on big tables); the page still reports HasNext.
func WithoutTotal() PaginateOption { return func(c *paginateConfig) { c.withTotal = false } }

// ScanFunc runs a query and returns its rows as T; Select[T] is the default, the ORM supplies a faster one.
type ScanFunc[T any] func(ctx context.Context, q Querier, sql string, args []any) ([]T, error)

// Paginate lists T rows matching the client's params, restricted by the schema's allow/deny rules.
// Row type T is scanned by `db` tags. It fetches one extra row to learn whether a next page exists,
// and runs a count query only when the answer is not already known from the page itself.
func Paginate[T any](ctx context.Context, q Querier, schema *query.Schema, base ListQuery,
	params query.Params, opts ...PaginateOption) (query.Page[T], error) {
	return PaginateWith(ctx, q, schema, base, params, func(ctx context.Context, q Querier, sql string, args []any) ([]T, error) {
		return Select[T](ctx, q, sql, args...)
	}, opts...)
}

// PaginateWith is Paginate with a custom row scanner.
func PaginateWith[T any](ctx context.Context, q Querier, schema *query.Schema, base ListQuery,
	params query.Params, scan ScanFunc[T], opts ...PaginateOption) (query.Page[T], error) {

	cfg := paginateConfig{withTotal: true}
	for _, o := range opts {
		o(&cfg)
	}

	st, err := schema.Compile(params, len(base.Args))
	if err != nil {
		return query.Page[T]{}, err
	}
	listSQL, countSQL := buildListSQL(base, st)
	args := append(slices.Clone(base.Args), st.Args...)

	rows, err := scan(ctx, q, listSQL, args)
	if err != nil {
		return query.Page[T]{}, err
	}
	hasNext := len(rows) > st.Limit
	if hasNext {
		rows = rows[:st.Limit]
	}

	total := int64(-1)
	if cfg.withTotal {
		switch {
		case st.Page == 1 && !hasNext: // the whole result fit in the first page
			total = int64(len(rows))
		default:
			if total, err = Value[int64](ctx, q, countSQL, args...); err != nil {
				return query.Page[T]{}, err
			}
		}
	}
	return query.NewPage(rows, st.Page, st.PageSize, hasNext, total), nil
}

// buildListSQL assembles the page query (limit+1 rows) and the matching count query.
func buildListSQL(base ListQuery, st query.Statement) (list, count string) {
	var conds []string
	if base.Where != "" {
		conds = append(conds, "("+base.Where+")")
	}
	if st.Where != "" {
		conds = append(conds, st.Where)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	order := ""
	if st.OrderBy != "" {
		order = " ORDER BY " + st.OrderBy
	}
	list = fmt.Sprintf("%s %s%s%s LIMIT %d OFFSET %d", base.Select, base.From, where, order, st.Limit+1, st.Offset)
	count = fmt.Sprintf("SELECT count(*) %s%s", base.From, where)
	return list, count
}
