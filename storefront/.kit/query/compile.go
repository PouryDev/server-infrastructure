package query

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

const maxValueLen = 512

// Statement is the SQL produced for one request. Where and OrderBy have no keyword so the caller can
// splice them into any query; Limit and Offset are validated integers.
type Statement struct {
	Where    string     // "" or conditions joined with AND, e.g. "status = $1 AND age >= $2"
	Args     []any      // bind arguments for Where, numbered after the caller's own arguments
	OrderBy  string     // "" never happens when the schema has a default sort or tie-breaker
	Order    []OrderKey // the same order, structured (used by keyset pagination)
	Limit    int        // page size
	Offset   int
	Page     int
	PageSize int
}

// Compile validates p against the schema. argOffset is the number of bind arguments the caller's own
// query already uses, so placeholders continue after them ($argOffset+1, ...). Every problem found is
// reported at once as an apperr.KindInvalid error with one FieldError per offender.
func (s *Schema) Compile(p Params, argOffset int) (Statement, error) {
	var (
		bad   []apperr.FieldError
		conds = make([]string, 0, len(p.Filters))
		args  = make([]any, 0, len(p.Filters)+2)
	)
	addErr := func(field, format string, a ...any) {
		bad = append(bad, apperr.FieldError{Field: field, Message: fmt.Sprintf(format, a...)})
	}
	bind := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(argOffset+len(args))
	}

	if len(p.Filters) > s.maxFilters {
		addErr("filter", "at most %d filters per request", s.maxFilters)
	} else {
		for _, f := range p.Filters {
			cond, err := s.compileFilter(f, bind)
			if err != nil {
				addErr("filter["+f.Field+"]", "%s", err.Error())
				continue
			}
			conds = append(conds, cond)
		}
	}

	orderBy, order := s.compileSort(p.Sort, addErr)

	page, size := p.Page, p.PageSize
	if page == 0 {
		page = 1
	}
	if page < 1 {
		addErr("page", "must be 1 or greater")
	}
	if size <= 0 {
		size = s.defaultPageSize
	}
	size = min(size, s.maxPageSize)
	offset := 0
	if page >= 1 {
		offset = (page - 1) * size
		if offset > s.maxOffset {
			addErr("page", "too deep: results are limited to the first %d rows, narrow the filters instead", s.maxOffset)
		}
	}

	if len(bad) > 0 {
		return Statement{}, apperr.Invalid("invalid list parameters", bad...)
	}
	if len(args) == 0 {
		args = nil
	}
	return Statement{
		Where: joinAnd(conds), Args: args, OrderBy: orderBy, Order: order,
		Limit: size, Offset: offset, Page: page, PageSize: size,
	}, nil
}

func (s *Schema) field(name string, sorting bool) (Field, error) {
	f, ok := s.fields[name]
	policies, disabled := s.filterPolicies, f.NoFilter
	if sorting {
		policies, disabled = s.sortPolicies, f.NoSort
	}
	// An unknown and a forbidden field look the same to the client, so field names cannot be probed.
	if !ok || disabled || !permitted(policies, name) {
		allowed := s.Filterable()
		if sorting {
			allowed = s.Sortable()
		}
		return Field{}, fmt.Errorf("unknown field, allowed: %s", strings.Join(allowed, ", "))
	}
	return f, nil
}

// OrderKey is one resolved ORDER BY column.
type OrderKey struct {
	Column string
	Desc   bool
	Type   Type // value type, needed to parse keyset cursors
	typed  bool // false when the column is not a schema field (a tie-breaker without a field)
}

func (s *Schema) compileSort(sorts []Sort, addErr func(field, format string, a ...any)) (string, []OrderKey) {
	if len(sorts) == 0 {
		sorts = s.defaultSort // trusted: defaults bypass the client-facing policy
	} else if len(sorts) > s.maxSortKeys {
		addErr("sort", "at most %d sort keys", s.maxSortKeys)
		return "", nil
	}
	keys := make([]OrderKey, 0, len(sorts)+1)
	add := func(col string, desc bool, t Type, typed bool) {
		for _, k := range keys { // a handful of keys: a scan beats a map
			if k.Column == col {
				return
			}
		}
		keys = append(keys, OrderKey{Column: col, Desc: desc, Type: t, typed: typed})
	}
	for _, so := range sorts {
		var f Field
		if isDefault(s.defaultSort, so) {
			f = s.fields[so.Field]
		} else {
			var err error
			if f, err = s.field(so.Field, true); err != nil {
				addErr("sort", "%q: %s", so.Field, err)
				continue
			}
		}
		add(f.Column, so.Desc, f.Type, true)
	}
	if s.tieBreaker != "" {
		t, typed := s.typeOfColumn(s.tieBreaker)
		add(s.tieBreaker, false, t, typed)
	}
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k.Column)
		if k.Desc {
			b.WriteString(" DESC")
		} else {
			b.WriteString(" ASC")
		}
	}
	return b.String(), keys
}

func (s *Schema) typeOfColumn(col string) (Type, bool) {
	for _, f := range s.fields {
		if f.Column == col {
			return f.Type, true
		}
	}
	return 0, false
}

func isDefault(defaults []Sort, s Sort) bool { return slices.Contains(defaults, s) }

func (s *Schema) compileFilter(f Filter, bind func(any) string) (string, error) {
	fld, err := s.field(f.Field, false)
	if err != nil {
		return "", err
	}
	op := f.Op
	if op == "" {
		op = OpEq
	}
	if !fld.allows(op) {
		return "", fmt.Errorf("operator %q is not supported for this field", op)
	}
	col := fld.Column

	switch op {
	case OpNull:
		v := "true"
		if len(f.Values) == 1 && f.Values[0] != "" {
			v = f.Values[0]
		} else if len(f.Values) > 1 {
			return "", fmt.Errorf("expects a single true/false value")
		}
		isNull, err := strconv.ParseBool(v)
		if err != nil {
			return "", fmt.Errorf("expects true or false")
		}
		if isNull {
			return col + " IS NULL", nil
		}
		return col + " IS NOT NULL", nil

	case OpIn, OpNin, OpBetween:
		raw := splitList(f.Values)
		if len(raw) == 0 {
			return "", fmt.Errorf("needs at least one value")
		}
		if op == OpBetween && len(raw) != 2 {
			return "", fmt.Errorf("needs exactly two comma-separated values (from,to)")
		}
		if len(raw) > s.maxListValues {
			return "", fmt.Errorf("at most %d values", s.maxListValues)
		}
		if op == OpBetween {
			lo, err := parseValue(fld.Type, raw[0])
			if err != nil {
				return "", err
			}
			hi, err := parseValue(fld.Type, raw[1])
			if err != nil {
				return "", err
			}
			a, b := bind(lo), bind(hi)
			return col + " BETWEEN " + a + " AND " + b, nil
		}
		list, err := parseList(fld.Type, raw)
		if err != nil {
			return "", err
		}
		if op == OpIn {
			return col + " = ANY(" + bind(list) + ")", nil
		}
		return col + " <> ALL(" + bind(list) + ")", nil
	}

	if len(f.Values) != 1 {
		return "", fmt.Errorf("expects exactly one value")
	}
	raw := f.Values[0]
	switch op {
	case OpLike:
		return col + " ILIKE " + bind("%"+escapeLike(raw)+"%"), nil
	case OpPrefix:
		return col + " ILIKE " + bind(escapeLike(raw)+"%"), nil
	}
	v, err := parseValue(fld.Type, raw)
	if err != nil {
		return "", err
	}
	sym := map[Operator]string{OpEq: "=", OpNe: "<>", OpGt: ">", OpGte: ">=", OpLt: "<", OpLte: "<="}[op]
	if sym == "" {
		return "", fmt.Errorf("unsupported operator %q", op)
	}
	return col + " " + sym + " " + bind(v), nil
}

func splitList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// escapeLike neutralises LIKE wildcards in user input (Postgres' default escape character is \).
func escapeLike(s string) string { return likeEscaper.Replace(s) }

// Built once: constructing a Replacer per call dominated the cost of a LIKE filter.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func parseValue(t Type, raw string) (any, error) {
	if len(raw) > maxValueLen {
		return nil, fmt.Errorf("value too long")
	}
	switch t {
	case TypeString:
		return raw, nil
	case TypeInt:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", raw)
		}
		return n, nil
	case TypeFloat:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f != f || f > 1.7e308 || f < -1.7e308 {
			return nil, fmt.Errorf("%q is not a number", raw)
		}
		return f, nil
	case TypeBool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not true/false", raw)
		}
		return b, nil
	case TypeTime:
		for _, layout := range []string{time.RFC3339Nano, time.DateOnly} {
			if tm, err := time.Parse(layout, raw); err == nil {
				return tm.UTC(), nil
			}
		}
		return nil, fmt.Errorf("%q is not a date (RFC 3339 or YYYY-MM-DD)", raw)
	case TypeUUID:
		u, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not a UUID", raw)
		}
		return u, nil
	}
	return nil, fmt.Errorf("unsupported type %s", t)
}

// parseList returns a typed slice so the driver can bind it to a Postgres array.
func parseList(t Type, raw []string) (any, error) {
	switch t {
	case TypeString:
		return slices.Clone(raw), nil
	case TypeInt:
		return convert[int64](t, raw)
	case TypeFloat:
		return convert[float64](t, raw)
	case TypeBool:
		return convert[bool](t, raw)
	case TypeTime:
		return convert[time.Time](t, raw)
	case TypeUUID:
		return convert[uuid.UUID](t, raw)
	}
	return nil, fmt.Errorf("unsupported type %s", t)
}

func convert[T any](t Type, raw []string) ([]T, error) {
	out := make([]T, 0, len(raw))
	for _, r := range raw {
		v, err := parseValue(t, r)
		if err != nil {
			return nil, err
		}
		out = append(out, v.(T))
	}
	return out, nil
}

func joinAnd(conds []string) string {
	switch len(conds) {
	case 0:
		return ""
	case 1:
		return conds[0]
	}
	return strings.Join(conds, " AND ")
}
