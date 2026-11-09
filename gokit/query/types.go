// Package query turns untrusted list-request parameters (filters, sorting, pagination) into safe,
// parameterised SQL fragments.
//
// It has no database dependency: a Schema describes which fields exist, how they map to columns and
// which of them a caller may filter or sort on (allow-list and/or deny-list, evaluated per request
// through Schema.Restrict). Column names only ever come from the Schema, never from the request, and
// every value is passed as a bind argument.
package query

import "fmt"

// Type is the value type of a field; it decides how request strings are parsed.
type Type uint8

const (
	TypeString Type = iota
	TypeInt
	TypeFloat
	TypeBool
	TypeTime
	TypeUUID
)

func (t Type) String() string {
	switch t {
	case TypeString:
		return "string"
	case TypeInt:
		return "int"
	case TypeFloat:
		return "float"
	case TypeBool:
		return "bool"
	case TypeTime:
		return "time"
	case TypeUUID:
		return "uuid"
	}
	return fmt.Sprintf("type(%d)", t)
}

// Operator is a comparison a client may request on a field.
type Operator string

const (
	OpEq      Operator = "eq"
	OpNe      Operator = "ne"
	OpGt      Operator = "gt"
	OpGte     Operator = "gte"
	OpLt      Operator = "lt"
	OpLte     Operator = "lte"
	OpLike    Operator = "like"   // case-insensitive "contains"
	OpPrefix  Operator = "prefix" // case-insensitive "starts with"
	OpIn      Operator = "in"
	OpNin     Operator = "nin"
	OpBetween Operator = "between" // inclusive, two values
	OpNull    Operator = "null"    // value true = IS NULL, false = IS NOT NULL
)

var defaultOperators = map[Type][]Operator{
	TypeString: {OpEq, OpNe, OpLike, OpPrefix, OpIn, OpNin, OpNull},
	TypeInt:    {OpEq, OpNe, OpGt, OpGte, OpLt, OpLte, OpIn, OpNin, OpBetween, OpNull},
	TypeFloat:  {OpEq, OpNe, OpGt, OpGte, OpLt, OpLte, OpBetween, OpNull},
	TypeTime:   {OpEq, OpNe, OpGt, OpGte, OpLt, OpLte, OpBetween, OpNull},
	TypeBool:   {OpEq, OpNe, OpNull},
	TypeUUID:   {OpEq, OpNe, OpIn, OpNin, OpNull},
}

// Field describes one queryable attribute.
type Field struct {
	Name   string // name used by clients (query string)
	Column string // SQL column, optionally table-qualified: "email" or "u.email"
	Type   Type
	// NoFilter / NoSort switch off one capability for this field; the zero value allows both.
	NoFilter bool
	NoSort   bool
	// Operators narrows the operators allowed on this field (default: all that make sense for Type).
	Operators []Operator
}

func (f Field) allows(op Operator) bool {
	ops := f.Operators
	if len(ops) == 0 {
		ops = defaultOperators[f.Type]
	}
	for _, o := range ops {
		if o == op {
			return true
		}
	}
	return false
}

// Filter is one raw, not yet validated, client condition.
type Filter struct {
	Field  string
	Op     Operator
	Values []string
}

// Sort is one sort key.
type Sort struct {
	Field string
	Desc  bool
}

// Params is the transport-independent form of a list request.
type Params struct {
	Filters  []Filter
	Sort     []Sort
	Page     int // 1-based; 0 means the first page
	PageSize int // 0 means the schema default
}
