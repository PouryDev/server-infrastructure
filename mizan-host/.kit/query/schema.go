package query

import (
	"fmt"
	"regexp"
	"slices"
)

// A column is a plain or double-quoted identifier, optionally table-qualified. Quoted identifiers cannot
// contain quotes, so nothing here can break out of the identifier.
var columnRe = regexp.MustCompile(`^("[A-Za-z_][A-Za-z0-9_]*"|[A-Za-z_][A-Za-z0-9_]*)(\.("[A-Za-z_][A-Za-z0-9_]*"|[A-Za-z_][A-Za-z0-9_]*))?$`)

// Schema is an immutable description of a queryable resource. Build it once at start-up with
// NewSchema (or NewSchemaFor) and derive per-request variants with Restrict.
type Schema struct {
	fields map[string]Field
	order  []string

	filterPolicies []Policy
	sortPolicies   []Policy

	defaultSort     []Sort
	tieBreaker      string
	defaultPageSize int
	maxPageSize     int
	maxOffset       int
	maxFilters      int
	maxListValues   int
	maxSortKeys     int
}

// Option configures a Schema (NewSchema) or derives a stricter one (Restrict).
type Option func(*Schema)

// WithFilterPolicy restricts the fields clients may filter on.
func WithFilterPolicy(p Policy) Option {
	return func(s *Schema) { s.filterPolicies = append(s.filterPolicies, p) }
}

// WithSortPolicy restricts the fields clients may sort by.
func WithSortPolicy(p Policy) Option {
	return func(s *Schema) { s.sortPolicies = append(s.sortPolicies, p) }
}

// WithPolicy restricts both filtering and sorting.
func WithPolicy(p Policy) Option {
	return func(s *Schema) { WithFilterPolicy(p)(s); WithSortPolicy(p)(s) }
}

// WithDefaultSort sets the order used when the client sends none.
func WithDefaultSort(sorts ...Sort) Option { return func(s *Schema) { s.defaultSort = sorts } }

// WithTieBreaker appends a unique column (usually the primary key) to every ORDER BY so pages are
// stable. It is a column expression, not a client-visible field.
func WithTieBreaker(column string) Option { return func(s *Schema) { s.tieBreaker = column } }

// WithPageSize sets the default and the maximum page size (larger requests are clamped).
func WithPageSize(def, max int) Option {
	return func(s *Schema) { s.defaultPageSize, s.maxPageSize = def, max }
}

// WithLimits bounds the work one request can ask for.
func WithLimits(maxFilters, maxListValues, maxSortKeys, maxOffset int) Option {
	return func(s *Schema) {
		s.maxFilters, s.maxListValues, s.maxSortKeys, s.maxOffset = maxFilters, maxListValues, maxSortKeys, maxOffset
	}
}

// NewSchema validates the fields and applies the options.
func NewSchema(fields []Field, opts ...Option) (*Schema, error) {
	s := &Schema{
		fields:          make(map[string]Field, len(fields)),
		defaultPageSize: 20,
		maxPageSize:     100,
		maxOffset:       100_000,
		maxFilters:      20,
		maxListValues:   100,
		maxSortKeys:     3,
	}
	for _, f := range fields {
		if f.Name == "" {
			return nil, fmt.Errorf("query: field with empty name (column %q)", f.Column)
		}
		if !columnRe.MatchString(f.Column) {
			return nil, fmt.Errorf("query: field %q has unsafe column %q", f.Name, f.Column)
		}
		if _, dup := s.fields[f.Name]; dup {
			return nil, fmt.Errorf("query: duplicate field %q", f.Name)
		}
		for _, op := range f.Operators {
			if !slices.Contains(defaultOperators[f.Type], op) {
				return nil, fmt.Errorf("query: field %q (%s) cannot support operator %q", f.Name, f.Type, op)
			}
		}
		s.fields[f.Name] = f
		s.order = append(s.order, f.Name)
	}
	for _, o := range opts {
		o(s)
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Restrict returns a copy with extra restrictions, e.g. a narrower white list for non-admin callers.
// Restrictions only accumulate; a Schema can never become more permissive. Naming a field that does
// not exist is an error, so a typo in a deny list cannot silently expose a field.
func (s *Schema) Restrict(opts ...Option) (*Schema, error) {
	c := *s
	c.filterPolicies = slices.Clone(s.filterPolicies)
	c.sortPolicies = slices.Clone(s.sortPolicies)
	for _, o := range opts {
		o(&c)
	}
	// Restrict must not loosen sort or paging rules the base schema chose.
	c.defaultSort, c.tieBreaker = s.defaultSort, s.tieBreaker
	c.maxPageSize = min(c.maxPageSize, s.maxPageSize)
	c.defaultPageSize = min(c.defaultPageSize, c.maxPageSize)
	c.maxFilters = min(c.maxFilters, s.maxFilters)
	c.maxListValues = min(c.maxListValues, s.maxListValues)
	c.maxSortKeys = min(c.maxSortKeys, s.maxSortKeys)
	c.maxOffset = min(c.maxOffset, s.maxOffset)
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Schema) validate() error {
	for _, policies := range [][]Policy{s.filterPolicies, s.sortPolicies} {
		for _, p := range policies {
			for _, n := range p.names() {
				if _, ok := s.fields[n]; !ok {
					return fmt.Errorf("query: policy names unknown field %q", n)
				}
			}
		}
	}
	for _, d := range s.defaultSort {
		if _, ok := s.fields[d.Field]; !ok {
			return fmt.Errorf("query: default sort uses unknown field %q", d.Field)
		}
	}
	if s.tieBreaker != "" && !columnRe.MatchString(s.tieBreaker) {
		return fmt.Errorf("query: unsafe tie-breaker %q", s.tieBreaker)
	}
	if s.defaultPageSize < 1 || s.maxPageSize < s.defaultPageSize {
		return fmt.Errorf("query: invalid page sizes (default %d, max %d)", s.defaultPageSize, s.maxPageSize)
	}
	return nil
}

func permitted(policies []Policy, name string) bool {
	for _, p := range policies {
		if !p.Permits(name) {
			return false
		}
	}
	return true
}

// Filterable returns the names of the fields a client may filter on, in declaration order.
func (s *Schema) Filterable() []string {
	return s.names(func(f Field) bool { return !f.NoFilter }, s.filterPolicies)
}

// Sortable returns the names of the fields a client may sort by, in declaration order.
func (s *Schema) Sortable() []string {
	return s.names(func(f Field) bool { return !f.NoSort }, s.sortPolicies)
}

func (s *Schema) names(ok func(Field) bool, policies []Policy) []string {
	var out []string
	for _, n := range s.order {
		if ok(s.fields[n]) && permitted(policies, n) {
			out = append(out, n)
		}
	}
	return out
}
