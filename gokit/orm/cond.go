package orm

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// builder assembles one statement: SQL text plus bind arguments. Unknown column names are recorded and
// reported when the statement runs, so a typo is an error, never silently dropped SQL. Sub-builders
// (EXISTS subqueries) share the root's argument list, so placeholders stay numbered consistently.
type builder struct {
	m    *Model
	qual string // quoted table or alias that qualifies this model's columns
	sb   strings.Builder
	args []any
	err  error

	root *builder // nil on the root

	// root only
	joins      map[string]joined // relation name -> joined alias and model
	exprs      map[string]string // output alias -> SQL (Project)
	havingMode bool              // alias references expand to their expression
	nextAlias  int
}

type joined struct {
	alias  string
	target *Model
}

func newBuilder(m *Model) *builder { return &builder{m: m, qual: m.quotedTable} }

func (b *builder) rootb() *builder {
	if b.root != nil {
		return b.root
	}
	return b
}

func (b *builder) bind(v any) string {
	r := b.rootb()
	r.args = append(r.args, v)
	return "$" + strconv.Itoa(len(r.args))
}

func (b *builder) fail(format string, a ...any) {
	if r := b.rootb(); r.err == nil {
		r.err = fmt.Errorf(format, a...)
	}
}

// child returns a builder for a subquery over target, qualified by alias.
func (b *builder) child(target *Model, alias string) *builder {
	return &builder{m: target, qual: alias, root: b.rootb()}
}

func (b *builder) newAlias() string {
	r := b.rootb()
	r.nextAlias++
	return `"x` + strconv.Itoa(r.nextAlias) + `"`
}

// ref resolves a column reference to qualified SQL. Accepted forms: a column or Go field name of the
// current model, "Relation.column" for a relation brought in with Join, and (in ORDER BY / Project
// contexts) the alias of a selected expression.
func (b *builder) ref(name string) (string, bool) {
	if rel, col, ok := strings.Cut(name, "."); ok {
		j, found := b.rootb().joins[rel]
		if !found {
			b.fail("orm: %q refers to relation %s, which is not joined (call Join(%q) first)", name, rel, rel)
			return "", false
		}
		f, ok := j.target.Field(col)
		if !ok {
			b.fail("orm: %s has no column %q", j.target.Type.Name(), col)
			return "", false
		}
		return j.alias + "." + f.quoted, true
	}
	if f, ok := b.m.Field(name); ok {
		return b.qual + "." + f.quoted, true
	}
	if r := b.rootb(); r.exprs != nil {
		if sql, ok := r.exprs[name]; ok {
			if r.havingMode {
				return sql, true
			}
			return quote(name), true
		}
	}
	b.fail("orm: %s has no column %q", b.m.Type.Name(), name)
	return "", false
}

// Cond is a WHERE condition. Build them with Eq, In, And, Or, WhereHas ...; columns are Go field names or
// column names (and "Relation.column" after a Join).
type Cond interface{ write(b *builder) }

type cond func(b *builder)

func (c cond) write(b *builder) { c(b) }

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

func compare(op, col string, v any) Cond {
	return cond(func(b *builder) {
		ref, ok := b.ref(col)
		if !ok {
			return
		}
		if isNil(v) && (op == "=" || op == "<>") {
			b.sb.WriteString(ref + map[string]string{"=": " IS NULL", "<>": " IS NOT NULL"}[op])
			return
		}
		b.sb.WriteString(ref + " " + op + " " + b.bind(v))
	})
}

func Eq(col string, v any) Cond  { return compare("=", col, v) }  // v == nil means IS NULL
func Ne(col string, v any) Cond  { return compare("<>", col, v) } // v == nil means IS NOT NULL
func Gt(col string, v any) Cond  { return compare(">", col, v) }
func Gte(col string, v any) Cond { return compare(">=", col, v) }
func Lt(col string, v any) Cond  { return compare("<", col, v) }
func Lte(col string, v any) Cond { return compare("<=", col, v) }

// Like is a case-insensitive pattern match; the caller supplies the % wildcards.
func Like(col, pattern string) Cond { return compare("ILIKE", col, pattern) }

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Contains matches rows whose column contains s (wildcards in s are escaped).
func Contains(col, s string) Cond { return Like(col, "%"+likeEscaper.Replace(s)+"%") }

// HasPrefix matches rows whose column starts with s (wildcards in s are escaped).
func HasPrefix(col, s string) Cond { return Like(col, likeEscaper.Replace(s)+"%") }

func IsNull(col string) Cond  { return Eq(col, nil) }
func NotNull(col string) Cond { return Ne(col, nil) }

func Between(col string, lo, hi any) Cond {
	return cond(func(b *builder) {
		if ref, ok := b.ref(col); ok {
			b.sb.WriteString(ref + " BETWEEN " + b.bind(lo) + " AND " + b.bind(hi))
		}
	})
}

// In matches rows whose column equals any value of the slice (one bind argument however long the slice
// is, compiled as = ANY($n)). An empty slice matches nothing.
func In(col string, values any) Cond { return anyOf(col, values, false) }

// NotIn matches rows whose column differs from every value; an empty slice matches everything.
func NotIn(col string, values any) Cond { return anyOf(col, values, true) }

func anyOf(col string, values any, negate bool) Cond {
	return cond(func(b *builder) {
		ref, ok := b.ref(col)
		if !ok {
			return
		}
		rv := reflect.ValueOf(values)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			b.fail("orm: In/NotIn on %q needs a slice, got %T", col, values)
			return
		}
		if rv.Len() == 0 {
			b.sb.WriteString(map[bool]string{false: "FALSE", true: "TRUE"}[negate])
			return
		}
		if negate {
			b.sb.WriteString(ref + " <> ALL(" + b.bind(values) + ")")
		} else {
			b.sb.WriteString(ref + " = ANY(" + b.bind(values) + ")")
		}
	})
}

func group(sep, empty string, conds []Cond) Cond {
	return cond(func(b *builder) {
		if len(conds) == 0 {
			b.sb.WriteString(empty)
			return
		}
		b.sb.WriteByte('(')
		for i, c := range conds {
			if i > 0 {
				b.sb.WriteString(sep)
			}
			c.write(b)
		}
		b.sb.WriteByte(')')
	})
}

// And is true when all conditions are (true for none).
func And(conds ...Cond) Cond { return group(" AND ", "TRUE", conds) }

// Or is true when any condition is (false for none).
func Or(conds ...Cond) Cond { return group(" OR ", "FALSE", conds) }

// Not negates a condition.
func Not(c Cond) Cond {
	return cond(func(b *builder) { b.sb.WriteString("NOT ("); c.write(b); b.sb.WriteByte(')') })
}

// Raw embeds trusted SQL with ? placeholders (?? is a literal question mark). Never build the SQL text
// from user input; pass user input as args. The main table is referenced by its own name, joined
// relations by their aliases ("j_<relation>", see Join).
func Raw(sql string, args ...any) Cond {
	return cond(func(b *builder) {
		b.sb.WriteByte('(')
		i := 0
		for j := 0; j < len(sql); j++ {
			if sql[j] != '?' {
				b.sb.WriteByte(sql[j])
				continue
			}
			if j+1 < len(sql) && sql[j+1] == '?' {
				b.sb.WriteByte('?')
				j++
				continue
			}
			if i >= len(args) {
				b.fail("orm: Raw has more placeholders than arguments: %s", sql)
				return
			}
			b.sb.WriteString(b.bind(args[i]))
			i++
		}
		if i != len(args) {
			b.fail("orm: Raw has %d arguments for %d placeholders: %s", len(args), i, sql)
		}
		b.sb.WriteByte(')')
	})
}
