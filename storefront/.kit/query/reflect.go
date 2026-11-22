package query

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	timeType = reflect.TypeFor[time.Time]()
	uuidType = reflect.TypeFor[uuid.UUID]()
)

// FieldsOf derives the field list from the `db` tags of T (the row struct a repository scans into).
//
//	type userRow struct {
//	    ID    uuid.UUID `db:"id"`
//	    Email string    `db:"email" json:"email"`
//	    Hash  string    `db:"password_hash" query:"-"`           // never queryable
//	    Notes string    `db:"notes" query:"nofilter,nosort"`
//	}
//
// The client-facing name is the json tag name, falling back to the db name; `query:"name=x"` overrides
// it. Fields without a db tag, with query:"-", or of an unsupported type are skipped. table, when not
// empty, qualifies every column ("u" gives "u.email").
func FieldsOf[T any](table string) ([]Field, error) {
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("query: FieldsOf needs a struct, got %s", t)
	}
	var out []Field
	collect(t, table, &out)
	return out, nil
}

// NewSchemaFor is FieldsOf followed by NewSchema.
func NewSchemaFor[T any](table string, opts ...Option) (*Schema, error) {
	fields, err := FieldsOf[T](table)
	if err != nil {
		return nil, err
	}
	return NewSchema(fields, opts...)
}

func collect(t reflect.Type, table string, out *[]Field) {
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct && sf.Tag.Get("db") == "" {
			collect(sf.Type, table, out)
			continue
		}
		col := strings.Split(sf.Tag.Get("db"), ",")[0]
		if col == "" || col == "-" {
			continue
		}
		name := strings.Split(sf.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			name = col
		}
		typ, ok := typeOf(sf.Type)
		if !ok {
			continue
		}
		f := Field{Name: name, Column: col, Type: typ}
		if table != "" {
			f.Column = table + "." + col
		}
		skip := false
		for _, tok := range strings.Split(sf.Tag.Get("query"), ",") {
			switch tok = strings.TrimSpace(tok); {
			case tok == "-":
				skip = true
			case tok == "nofilter":
				f.NoFilter = true
			case tok == "nosort":
				f.NoSort = true
			case strings.HasPrefix(tok, "name="):
				f.Name = strings.TrimPrefix(tok, "name=")
			}
		}
		if !skip {
			*out = append(*out, f)
		}
	}
}

func typeOf(t reflect.Type) (Type, bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case timeType:
		return TypeTime, true
	case uuidType:
		return TypeUUID, true
	}
	switch t.Kind() {
	case reflect.String:
		return TypeString, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return TypeInt, true
	case reflect.Float32, reflect.Float64:
		return TypeFloat, true
	case reflect.Bool:
		return TypeBool, true
	}
	return 0, false
}
