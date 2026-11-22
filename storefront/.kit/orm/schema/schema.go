// Package schema derives database DDL from ORM models, reads the schema of a live database, and
// computes the migration that turns one into the other (both directions: Up and Down).
//
// It manages only what the models describe: tables, columns (type, nullability, default, identity),
// primary keys, unique constraints, plain indexes and foreign keys (including the join tables of
// many_to_many relations). Objects that exist in the database but not in the models (CHECK constraints,
// partial or expression indexes, extra tables) are left alone; dropping tables, columns and indexes the
// models no longer mention happens only when Options.AllowDrop is set.
package schema

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Schema is a set of tables.
type Schema struct {
	Tables map[string]*Table
}

// Table is one table.
type Table struct {
	Name    string
	Columns []*Column
	PK      []string
	Uniques []*Unique
	Indexes []*Index
	FKs     []*ForeignKey
	Checks  []*Check
	Was     string // previous table name (rename)
}

// Check is a named CHECK constraint. Expr is the model's expression; Def is what the database reports
// (pg_get_constraintdef), used only to recreate a dropped constraint. Expressions are not compared: the
// name identifies the constraint, so to change an expression give the check a new name.
type Check struct {
	Name string
	Expr string
	Def  string
}

// Column is one column. Type is in PostgreSQL's canonical spelling (see Normalize).
type Column struct {
	Name     string
	Type     string
	NotNull  bool
	Default  string
	Identity bool
	Was      string // previous column name (rename)
}

// Unique is a unique constraint (or a unique index, when read from a database without the constraint).
type Unique struct {
	Name       string
	Columns    []string
	Constraint bool // true when it is a named constraint rather than a bare unique index
}

// Index is a plain, non-unique btree index.
type Index struct {
	Name    string
	Columns []string
}

// ForeignKey references another table.
type ForeignKey struct {
	Name       string
	Columns    []string
	RefTable   string
	RefColumns []string
	OnDelete   string // "", "cascade", "restrict", "set null"; "" means no action
}

func newSchema() *Schema { return &Schema{Tables: map[string]*Table{}} }

func (t *Table) column(name string) *Column {
	for _, c := range t.Columns {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (s *Schema) sortedTables() []*Table {
	names := make([]string, 0, len(s.Tables))
	for n := range s.Tables {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*Table, len(names))
	for i, n := range names {
		out[i] = s.Tables[n]
	}
	return out
}

var (
	castRe  = regexp.MustCompile(`::[a-z_ ]+(\([0-9, ]+\))?(\[\])?`)
	spaceRe = regexp.MustCompile(`\s+`)
)

var typeSynonyms = map[string]string{
	"int": "integer", "int4": "integer", "int8": "bigint", "int2": "smallint", "bool": "boolean",
	"varchar": "character varying", "float8": "double precision", "float4": "real", "float": "double precision",
	"timestamptz": "timestamp with time zone", "timestamp": "timestamp without time zone",
	"decimal": "numeric", "serial": "integer", "bigserial": "bigint",
}

// NormalizeType maps the many spellings of a type to what format_type() reports, so a model that says
// varchar(100) and a database that says character varying(100) compare equal.
func NormalizeType(t string) string {
	t = strings.ToLower(strings.TrimSpace(spaceRe.ReplaceAllString(t, " ")))
	suffix := ""
	for strings.HasSuffix(t, "[]") {
		suffix += "[]"
		t = strings.TrimSuffix(t, "[]")
	}
	base, args := t, ""
	if i := strings.IndexByte(t, '('); i >= 0 {
		base, args = strings.TrimSpace(t[:i]), strings.ReplaceAll(t[i:], " ", "")
	}
	if syn, ok := typeSynonyms[base]; ok {
		base = syn
	}
	return base + args + suffix
}

// NormalizeDefault makes a default expression comparable: lower case, no casts, no outer parentheses,
// and the spellings of "now" folded together.
func NormalizeDefault(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = castRe.ReplaceAllString(d, "")
	for strings.HasPrefix(d, "(") && strings.HasSuffix(d, ")") && balanced(d[1:len(d)-1]) {
		d = strings.TrimSpace(d[1 : len(d)-1])
	}
	d = spaceRe.ReplaceAllString(d, " ")
	switch d {
	case "current_timestamp", "transaction_timestamp()", "now()", "statement_timestamp()":
		return "now()"
	}
	return d
}

func balanced(s string) bool {
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

func quoteIdent(s string) string { return `"` + s + `"` }

func quoteList(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = quoteIdent(c)
	}
	return strings.Join(q, ", ")
}

func checkName(kind, name string) error {
	if len(name) > 63 {
		return fmt.Errorf("schema: generated %s name %q is longer than 63 characters (PostgreSQL would truncate it); shorten the table or column names", kind, name)
	}
	return nil
}
