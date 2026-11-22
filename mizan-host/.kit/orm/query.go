package orm

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
)

// Query is a one-shot, not goroutine-safe builder; get one from Repository.Query. Every method that
// returns *Query[T] only records intent, nothing touches the database until All, One, Count, ...
type Query[T any] struct {
	r           *Repository[T]
	conds       []Cond
	order       []orderTerm
	limit       int
	offset      int
	cols        []string
	preloads    []string
	withDeleted bool
	forUpdate   bool
	distinct    bool
	joins       []joinSpec
	groupBy     []string
	having      []Cond
	schema      *query.Schema
}

type orderTerm struct {
	col  string
	desc bool
}

// Query starts a query on the model.
func (r *Repository[T]) Query() *Query[T] { return &Query[T]{r: r} }

// Where adds conditions, combined with AND.
func (q *Query[T]) Where(conds ...Cond) *Query[T] { q.conds = append(q.conds, conds...); return q }

// OrderBy sorts ascending by a column ("Relation.column" after a Join, or the alias of a Project
// expression); call repeatedly for more keys.
func (q *Query[T]) OrderBy(col string) *Query[T] {
	q.order = append(q.order, orderTerm{col, false})
	return q
}

// OrderByDesc sorts descending.
func (q *Query[T]) OrderByDesc(col string) *Query[T] {
	q.order = append(q.order, orderTerm{col, true})
	return q
}

func (q *Query[T]) Limit(n int) *Query[T]  { q.limit = n; return q }
func (q *Query[T]) Offset(n int) *Query[T] { q.offset = n; return q }

// Select loads only these columns (the rest of each struct stays zero): fewer bytes over the wire and
// index-only scans become possible. The primary key is not added automatically.
func (q *Query[T]) Select(cols ...string) *Query[T] { q.cols = cols; return q }

// Preload loads relations after the main query, one extra query per relation (never per row). Dotted
// names load nested relations: "Posts.Comments".
func (q *Query[T]) Preload(names ...string) *Query[T] {
	q.preloads = append(q.preloads, names...)
	return q
}

// WithDeleted includes soft-deleted rows.
func (q *Query[T]) WithDeleted() *Query[T] { q.withDeleted = true; return q }

// ForUpdate locks the selected rows until the transaction ends.
func (q *Query[T]) ForUpdate() *Query[T] { q.forUpdate = true; return q }

// UseSchema overrides the client-filtering schema for Paginate and Keyset (for example a Restrict-ed one).
func (q *Query[T]) UseSchema(s *query.Schema) *Query[T] { q.schema = s; return q }

// newBuilder starts a statement over the main table, with the joins registered.
func (q *Query[T]) newBuilder() *builder { return newBuilder(q.r.m) }

// where writes the WHERE conditions (without the keyword) and returns whether there were any.
func (q *Query[T]) where(b *builder) bool {
	n := 0
	sep := func() {
		if n > 0 {
			b.sb.WriteString(" AND ")
		}
		n++
	}
	if sd := q.r.m.SoftDelete; sd != nil && !q.withDeleted {
		sep()
		b.sb.WriteString(b.qual + "." + sd.quoted + " IS NULL")
	}
	for _, c := range q.conds {
		sep()
		c.write(b)
	}
	return n > 0
}

func (q *Query[T]) selectFields() ([]*Field, error) {
	if len(q.cols) == 0 {
		return q.r.m.Fields, nil
	}
	out := make([]*Field, len(q.cols))
	for i, name := range q.cols {
		f, ok := q.r.m.Field(name)
		if !ok {
			return nil, fmt.Errorf("orm: %s has no column %q", q.r.m.Type.Name(), name)
		}
		out[i] = f
	}
	return out, nil
}

// qualifiedList renders "table"."col", ... for the fields.
func qualifiedList(m *Model, fields []*Field) string {
	var sb strings.Builder
	for i, f := range fields {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(m.quotedTable + "." + f.quoted)
	}
	return sb.String()
}

// fromAndWhere writes " FROM table [joins] [WHERE ...]" onto b.
func (q *Query[T]) fromAndWhere(b *builder) {
	b.sb.WriteString(" FROM " + q.r.m.quotedTable)
	q.writeJoins(b)
	// WHERE is written into a scratch builder first so an empty condition list adds no keyword; the
	// scratch shares the root's argument list and registered joins.
	if q.hasConditions() {
		b.sb.WriteString(" WHERE ")
		q.where(b)
	}
}

func (q *Query[T]) hasConditions() bool {
	return (q.r.m.SoftDelete != nil && !q.withDeleted) || len(q.conds) > 0
}

func (q *Query[T]) writeOrderLimit(b *builder) {
	if len(q.groupBy) > 0 {
		b.sb.WriteString(" GROUP BY ")
		for i, g := range q.groupBy {
			if i > 0 {
				b.sb.WriteString(", ")
			}
			if ref, ok := b.ref(g); ok {
				b.sb.WriteString(ref)
			}
		}
	}
	if len(q.having) > 0 {
		b.sb.WriteString(" HAVING ")
		root := b.rootb()
		root.havingMode = true
		for i, h := range q.having {
			if i > 0 {
				b.sb.WriteString(" AND ")
			}
			h.write(b)
		}
		root.havingMode = false
	}
	for i, o := range q.order {
		ref, ok := b.ref(o.col)
		if !ok {
			break
		}
		if i == 0 {
			b.sb.WriteString(" ORDER BY ")
		} else {
			b.sb.WriteString(", ")
		}
		b.sb.WriteString(ref)
		if o.desc {
			b.sb.WriteString(" DESC")
		}
	}
	if q.limit > 0 {
		b.sb.WriteString(" LIMIT " + strconv.Itoa(q.limit))
	}
	if q.offset > 0 {
		b.sb.WriteString(" OFFSET " + strconv.Itoa(q.offset))
	}
	if q.forUpdate {
		b.sb.WriteString(" FOR UPDATE")
	}
}

func (q *Query[T]) selectSQL() (string, []any, []*Field, error) {
	fields, err := q.selectFields()
	if err != nil {
		return "", nil, nil, err
	}
	b := q.newBuilder()
	b.sb.WriteString("SELECT ")
	if q.distinct {
		b.sb.WriteString("DISTINCT ")
	}
	if len(q.cols) == 0 {
		b.sb.WriteString(q.r.m.cache.qualifiedSelect)
	} else {
		b.sb.WriteString(qualifiedList(q.r.m, fields))
	}
	q.fromAndWhere(b)
	q.writeOrderLimit(b)
	if b.err != nil {
		return "", nil, nil, b.err
	}
	return b.sb.String(), b.args, fields, nil
}

// SQL returns the statement All would run and its arguments (for logging and tests).
func (q *Query[T]) SQL() (string, []any, error) {
	sql, args, _, err := q.selectSQL()
	return sql, args, err
}

// All returns every matching row.
func (q *Query[T]) All(ctx context.Context) ([]T, error) {
	sql, args, fields, err := q.selectSQL()
	if err != nil {
		return nil, err
	}
	hint := 16
	if q.limit > 0 {
		hint = q.limit
	}
	rows, err := q.r.conn.Querier(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, postgres.MapError(err)
	}
	items, err := scanAll[T](rows, fields, hint)
	if err != nil {
		return nil, err
	}
	if err := q.r.preload(ctx, items, q.preloads); err != nil {
		return nil, err
	}
	return items, nil
}

// One returns the first matching row (apperr.KindNotFound when there is none).
func (q *Query[T]) One(ctx context.Context) (T, error) {
	var zero T
	items, err := q.Limit(1).All(ctx)
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, q.r.notFound()
	}
	return items[0], nil
}

// Count returns the number of matching main rows (distinct ones after Distinct()).
func (q *Query[T]) Count(ctx context.Context) (int64, error) {
	b := q.newBuilder()
	m := q.r.m
	if q.distinct && len(m.PK) == 1 {
		b.sb.WriteString("SELECT count(DISTINCT " + m.quotedTable + "." + m.PK[0].quoted + ")")
	} else {
		b.sb.WriteString("SELECT count(*)")
	}
	q.fromAndWhere(b)
	if b.err != nil {
		return 0, b.err
	}
	return postgres.Value[int64](ctx, q.r.conn.Querier(ctx), b.sb.String(), b.args...)
}

// Exists reports whether any row matches (SELECT EXISTS, which stops at the first hit).
func (q *Query[T]) Exists(ctx context.Context) (bool, error) {
	b := q.newBuilder()
	b.sb.WriteString("SELECT EXISTS (SELECT 1")
	q.fromAndWhere(b)
	b.sb.WriteString(")")
	if b.err != nil {
		return false, b.err
	}
	return postgres.Value[bool](ctx, q.r.conn.Querier(ctx), b.sb.String(), b.args...)
}

// Assignment is one SET column = value of UpdateWhere.
type Assignment struct {
	col string
	val any
}

// Set builds an assignment for UpdateWhere.
func Set(col string, val any) Assignment { return Assignment{col, val} }

// UpdateWhere updates all matching rows with one statement and returns how many changed. updated_at is
// refreshed; a Where is required (use Raw("TRUE") to update every row deliberately). Conditions may use
// Has / NotHas but not joins.
func (q *Query[T]) UpdateWhere(ctx context.Context, sets ...Assignment) (int64, error) {
	if len(q.conds) == 0 {
		return 0, fmt.Errorf("orm: UpdateWhere without a condition; use Where(Raw(\"TRUE\")) to update every row")
	}
	if len(sets) == 0 {
		return 0, fmt.Errorf("orm: UpdateWhere without assignments")
	}
	b := q.newBuilder()
	b.sb.WriteString("UPDATE " + q.r.m.quotedTable + " SET ")
	for i, s := range sets {
		f, ok := q.r.m.Field(s.col)
		if !ok {
			b.fail("orm: %s has no column %q", q.r.m.Type.Name(), s.col)
			break
		}
		if f.PK || f.Auto || f.ReadOnly {
			return 0, fmt.Errorf("orm: cannot update column %q", s.col)
		}
		if i > 0 {
			b.sb.WriteString(", ")
		}
		b.sb.WriteString(f.quoted + " = " + b.bind(s.val))
	}
	if u := q.r.m.UpdatedAt; u != nil {
		b.sb.WriteString(", " + u.quoted + " = now()")
	}
	if v := q.r.m.Version; v != nil {
		b.sb.WriteString(", " + v.quoted + " = " + v.quoted + " + 1")
	}
	b.sb.WriteString(" WHERE ")
	q.where(b)
	if b.err != nil {
		return 0, b.err
	}
	return postgres.Exec(ctx, q.r.conn.Querier(ctx), b.sb.String(), b.args...)
}

// Delete removes (or soft-deletes) all matching rows and returns how many.
func (q *Query[T]) Delete(ctx context.Context) (int64, error) {
	if len(q.conds) == 0 {
		return 0, fmt.Errorf("orm: Delete without a condition; use Where(Raw(\"TRUE\")) to delete every row")
	}
	b := q.newBuilder()
	m := q.r.m
	if m.SoftDelete != nil {
		b.sb.WriteString("UPDATE " + m.quotedTable + " SET " + m.SoftDelete.quoted + " = now() WHERE ")
	} else {
		b.sb.WriteString("DELETE FROM " + m.quotedTable + " WHERE ")
	}
	q.where(b)
	if b.err != nil {
		return 0, b.err
	}
	return postgres.Exec(ctx, q.r.conn.Querier(ctx), b.sb.String(), b.args...)
}

// baseList is the fixed part of a paginated listing and the columns it selects (Select(...) is honoured,
// so sensitive or heavy columns need not be read at all). Joins to one-row relations are included.
func (q *Query[T]) baseList() (postgres.ListQuery, []*Field, error) {
	if q.toManyJoin() {
		return postgres.ListQuery{}, nil, fmt.Errorf("orm: Paginate and Keyset cannot join a to-many relation (rows would repeat and the count would be wrong); filter with Has(...) instead")
	}
	fields, err := q.selectFields()
	if err != nil {
		return postgres.ListQuery{}, nil, err
	}
	b := q.newBuilder()
	q.writeJoins(b)
	joins := b.sb.String()
	b.sb.Reset()
	q.where(b)
	if b.err != nil {
		return postgres.ListQuery{}, nil, b.err
	}
	return postgres.ListQuery{
		Select: "SELECT " + qualifiedList(q.r.m, fields), From: "FROM " + q.r.m.quotedTable + joins,
		Where: b.sb.String(), Args: b.args,
	}, fields, nil
}

func (q *Query[T]) pageSchema() (*query.Schema, error) {
	if q.schema != nil {
		return q.schema, nil
	}
	return q.r.Schema()
}

// Paginate applies a client's filters, sorting and page (validated against the model's schema) on top of
// the query's own conditions. Soft-deleted rows stay hidden, Where conditions always apply, the count
// query is skipped when not needed, and Preload still works.
func (q *Query[T]) Paginate(ctx context.Context, params query.Params, opts ...postgres.PaginateOption) (query.Page[T], error) {
	schema, err := q.pageSchema()
	if err != nil {
		return query.Page[T]{}, err
	}
	base, fields, err := q.baseList()
	if err != nil {
		return query.Page[T]{}, err
	}
	page, err := postgres.PaginateWith(ctx, q.r.conn.Querier(ctx), schema, base, params,
		func(ctx context.Context, qr postgres.Querier, sql string, args []any) ([]T, error) {
			rows, err := qr.Query(ctx, sql, args...)
			if err != nil {
				return nil, postgres.MapError(err)
			}
			return scanAll[T](rows, fields, params.PageSize+1)
		}, opts...)
	if err != nil {
		return query.Page[T]{}, err
	}
	if err := q.r.preload(ctx, page.Items, q.preloads); err != nil {
		return query.Page[T]{}, err
	}
	return page, nil
}

// KeysetPage is one page of cursor pagination.
type KeysetPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"` // pass it back as the cursor to get the next page
	HasNext    bool   `json:"has_next"`
}

// Keyset is cursor pagination: constant cost for any depth because the database seeks with the index
// instead of skipping rows. cursor is "" for the first page. The schema's tie-breaker (the primary key by
// default) makes the order total; sort columns must not be NULL. See query.CompileKeyset.
func (q *Query[T]) Keyset(ctx context.Context, params query.Params, cursor string) (KeysetPage[T], error) {
	schema, err := q.pageSchema()
	if err != nil {
		return KeysetPage[T]{}, err
	}
	base, fields, err := q.baseList()
	if err != nil {
		return KeysetPage[T]{}, err
	}
	st, err := schema.CompileKeyset(params, cursor, len(base.Args))
	if err != nil {
		return KeysetPage[T]{}, err
	}
	if len(q.cols) > 0 { // the cursor is built from the last row, so its sort columns must have been read
		for _, k := range st.Order {
			if !slices.ContainsFunc(fields, func(f *Field) bool { return q.r.m.quotedTable+"."+f.quoted == k.Column }) {
				return KeysetPage[T]{}, fmt.Errorf("orm: Keyset with Select must include the sort column %s", k.Column)
			}
		}
	}
	var sb strings.Builder
	sb.WriteString(base.Select + " " + base.From)
	where := base.Where
	if st.Where != "" {
		if where != "" {
			where = "(" + where + ") AND " + st.Where
		} else {
			where = st.Where
		}
	}
	if where != "" {
		sb.WriteString(" WHERE " + where)
	}
	sb.WriteString(" ORDER BY " + st.OrderBy + " LIMIT " + strconv.Itoa(st.Limit+1))
	args := append(slices.Clone(base.Args), st.Args...)

	rows, err := q.r.conn.Querier(ctx).Query(ctx, sb.String(), args...)
	if err != nil {
		return KeysetPage[T]{}, postgres.MapError(err)
	}
	items, err := scanAll[T](rows, fields, st.Limit+1)
	if err != nil {
		return KeysetPage[T]{}, err
	}
	page := KeysetPage[T]{}
	if len(items) > st.Limit {
		items, page.HasNext = items[:st.Limit], true
	}
	page.Items = items
	if page.HasNext {
		if page.NextCursor, err = q.r.cursorOf(items[len(items)-1], st); err != nil {
			return KeysetPage[T]{}, err
		}
	}
	if err := q.r.preload(ctx, items, q.preloads); err != nil {
		return KeysetPage[T]{}, err
	}
	return page, nil
}

// cursorOf encodes the ORDER BY values of a row.
func (r *Repository[T]) cursorOf(row T, st query.Statement) (string, error) {
	order := st.Order
	rv := reflect.ValueOf(&row).Elem()
	vals := make([]string, len(order))
	for i, k := range order {
		f := r.fieldByQuotedColumn(k.Column)
		if f == nil {
			return "", fmt.Errorf("orm: cursor column %s is not a field of %s", k.Column, r.m.Type.Name())
		}
		fv := rv.FieldByIndex(f.Index)
		for fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				return "", fmt.Errorf("orm: keyset sort column %s is NULL in a row; keyset pagination needs NOT NULL sort columns", f.Column)
			}
			fv = fv.Elem()
		}
		s, err := query.FormatValue(fv.Interface())
		if err != nil {
			return "", err
		}
		vals[i] = s
	}
	return query.EncodeCursor(st.OrderBy, vals), nil
}

// fieldByQuotedColumn resolves `"table"."col"` (or `"col"`) back to the field.
func (r *Repository[T]) fieldByQuotedColumn(col string) *Field {
	if i := strings.LastIndexByte(col, '.'); i >= 0 {
		col = col[i+1:]
	}
	f, _ := r.m.Field(strings.Trim(col, `"`))
	return f
}
