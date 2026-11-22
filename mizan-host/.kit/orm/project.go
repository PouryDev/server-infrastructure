package orm

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
)

// Expr is one selected expression of Project.
type Expr struct {
	alias string
	build func(b *builder) (string, bool)
}

func (e Expr) sql(b *builder) (string, bool) {
	s, ok := e.build(b)
	if !ok {
		return "", false
	}
	if !identRe.MatchString(e.alias) {
		b.fail("orm: alias %q must match %s", e.alias, identRe)
		return "", false
	}
	return s + " AS " + quote(e.alias), true
}

// OrZero replaces a NULL result by 0: SUM, AVG, MIN and MAX of no rows (a left join without matches) are NULL,
// which cannot be scanned into a plain int64 / float64 field.
func (e Expr) OrZero() Expr {
	inner := e.build
	return Expr{alias: e.alias, build: func(b *builder) (string, bool) {
		s, ok := inner(b)
		if !ok {
			return "", false
		}
		return "COALESCE(" + s + ", 0)", true
	}}
}

// Col selects a column ("Relation.column" after a Join) under the given alias.
func Col(path, alias string) Expr {
	return Expr{alias: alias, build: func(b *builder) (string, bool) { return b.ref(path) }}
}

func agg(fn, path, alias string) Expr {
	return Expr{alias: alias, build: func(b *builder) (string, bool) {
		if path == "*" {
			if fn != "count" {
				b.fail("orm: %s(*) is not valid", fn)
				return "", false
			}
			return "count(*)", true
		}
		ref, ok := b.ref(path)
		if !ok {
			return "", false
		}
		return fn + "(" + ref + ")", true
	}}
}

// Count is count(path) or count(*) (path "*").
func Count(path, alias string) Expr { return agg("count", path, alias) }
func Sum(path, alias string) Expr   { return agg("sum", path, alias) }
func Avg(path, alias string) Expr   { return agg("avg", path, alias) }
func Min(path, alias string) Expr   { return agg("min", path, alias) }
func Max(path, alias string) Expr   { return agg("max", path, alias) }

// CountDistinct is count(DISTINCT path).
func CountDistinct(path, alias string) Expr {
	return Expr{alias: alias, build: func(b *builder) (string, bool) {
		ref, ok := b.ref(path)
		if !ok {
			return "", false
		}
		return "count(DISTINCT " + ref + ")", true
	}}
}

// GroupBy groups by columns ("Relation.column" after a Join). Use it with Project.
func (q *Query[T]) GroupBy(paths ...string) *Query[T] {
	q.groupBy = append(q.groupBy, paths...)
	return q
}

// Having filters groups; conditions may refer to the aliases of the Project expressions:
// Having(orm.Gt("posts", 5)) after Count("*", "posts").
func (q *Query[T]) Having(conds ...Cond) *Query[T] { q.having = append(q.having, conds...); return q }

// Project runs the query (joins, conditions, grouping, ordering, limits) selecting the given expressions
// instead of the model's columns, and scans the rows into R by column alias (R's `db` tags). It is the way
// to get aggregates and the columns of joined tables in one result:
//
//	type stat struct {
//	    Author string `db:"author"`
//	    Posts  int64  `db:"posts"`
//	}
//	rows, err := orm.Project[stat](ctx,
//	    authors.Query().LeftJoin("Posts").GroupBy("name").Having(orm.Gt("posts", 1)).OrderByDesc("posts").Limit(10),
//	    orm.Col("name", "author"), orm.Count("Posts.id", "posts"))
func Project[R, T any](ctx context.Context, q *Query[T], exprs ...Expr) ([]R, error) {
	if len(exprs) == 0 {
		return nil, fmt.Errorf("orm: Project needs at least one expression")
	}
	b := q.newBuilder()
	b.exprs = map[string]string{}
	b.sb.WriteString("SELECT ")
	if q.distinct {
		b.sb.WriteString("DISTINCT ")
	}
	// The FROM clause (and its joins) must be known before expressions are resolved, so render it first
	// into the same builder and splice the select list in front afterwards.
	b2 := q.newBuilder()
	b2.exprs = b.exprs
	q.fromAndWhere(b2)
	b.joins = b2.joins
	for i, e := range exprs {
		s, ok := e.sql(b)
		if !ok {
			break
		}
		if i > 0 {
			b.sb.WriteString(", ")
		}
		b.sb.WriteString(s)
		b.exprs[e.alias] = strings.TrimSuffix(s, " AS "+quote(e.alias))
	}
	b.sb.WriteString(b2.sb.String())
	b.args = append(b.args, b2.args...)
	if b2.err != nil && b.err == nil {
		b.err = b2.err
	}
	// Re-number: the WHERE placeholders were numbered in b2, the select list in b (usually none).
	if len(b.args) != len(b2.args) {
		return nil, fmt.Errorf("orm: Project expressions cannot take bind arguments")
	}
	q.writeOrderLimit(b)
	if b.err != nil {
		return nil, b.err
	}
	rows, err := q.r.conn.Querier(ctx).Query(ctx, b.sb.String(), b.args...)
	if err != nil {
		return nil, postgres.MapError(err)
	}
	return scanNamed[R](rows, 16)
}

// Fetch runs SQL and scans every row into R by column name (R's `db` tags, or snake_case field names).
// It is the escape hatch for what the builder does not express: arbitrary joins, CTEs, window functions,
// lateral joins. R need not be a model. The result may only contain columns R has fields for, and column
// names must be unique (alias duplicates such as a.id, b.id).
func Fetch[R any](ctx context.Context, conn Conn, sql string, args ...any) ([]R, error) {
	rows, err := conn.Querier(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, postgres.MapError(err)
	}
	return scanNamed[R](rows, 16)
}

// FetchOne is Fetch for exactly one row; no row is apperr.KindNotFound.
func FetchOne[R any](ctx context.Context, conn Conn, sql string, args ...any) (R, error) {
	var zero R
	rows, err := Fetch[R](ctx, conn, sql, args...)
	if err != nil {
		return zero, err
	}
	if len(rows) == 0 {
		return zero, apperr.NotFound("row")
	}
	return rows[0], nil
}

// resultMeta maps result column names to field index paths of a result struct.
type resultMeta struct {
	byCol map[string][]int
}

var resultCache sync.Map // reflect.Type -> *resultMeta

func resultOf(t reflect.Type) (*resultMeta, error) {
	if v, ok := resultCache.Load(t); ok {
		return v.(*resultMeta), nil
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("orm: result type %s is not a struct", t)
	}
	meta := &resultMeta{byCol: map[string][]int{}}
	var walk func(t reflect.Type, prefix []int)
	walk = func(t reflect.Type, prefix []int) {
		for i := range t.NumField() {
			sf := t.Field(i)
			idx := append(append([]int(nil), prefix...), i)
			tag := sf.Tag.Get("db")
			if !sf.IsExported() || tag == "-" || parseTag(sf.Tag.Get("orm")).has("-") {
				continue
			}
			if sf.Anonymous && sf.Type.Kind() == reflect.Struct && tag == "" {
				walk(sf.Type, idx)
				continue
			}
			col := strings.Split(tag, ",")[0]
			if col == "" {
				col = snake(sf.Name)
			}
			if _, dup := meta.byCol[col]; !dup {
				meta.byCol[col] = idx
			}
		}
	}
	walk(t, nil)
	actual, _ := resultCache.LoadOrStore(t, meta)
	return actual.(*resultMeta), nil
}

// scanNamed reads rows into []R matching columns to fields by name.
func scanNamed[R any](rows pgx.Rows, hint int) ([]R, error) {
	defer rows.Close()
	meta, err := resultOf(reflect.TypeFor[R]())
	if err != nil {
		return nil, err
	}
	descs := rows.FieldDescriptions()
	idx := make([][]int, len(descs))
	seen := make(map[string]bool, len(descs))
	for i, d := range descs {
		if seen[d.Name] {
			return nil, fmt.Errorf("orm: the result has two columns named %q; alias one of them", d.Name)
		}
		seen[d.Name] = true
		p, ok := meta.byCol[d.Name]
		if !ok {
			return nil, fmt.Errorf("orm: the result has column %q but %s has no field for it (add `db:%q`)", d.Name, reflect.TypeFor[R](), d.Name)
		}
		idx[i] = p
	}
	out := make([]R, 0, hint)
	sv := reflect.ValueOf(&out).Elem()
	dest := make([]any, len(descs))
	for rows.Next() {
		n := sv.Len()
		sv.Grow(1)
		sv.SetLen(n + 1)
		elem := sv.Index(n)
		for i, p := range idx {
			dest[i] = elem.FieldByIndex(p).Addr().Interface()
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, postgres.MapError(err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, postgres.MapError(err)
	}
	return out, nil
}
