package orm

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
)

var (
	timeType   = reflect.TypeFor[time.Time]()
	uuidType   = reflect.TypeFor[uuid.UUID]()
	bytesType  = reflect.TypeFor[[]byte]()
	valuerType = reflect.TypeFor[driver.Valuer]()
)

// SQL type names are kept in the form PostgreSQL's format_type() reports them, so the schema differ can
// compare model and database without a translation table.
const (
	TypeText        = "text"
	TypeSmallint    = "smallint"
	TypeInteger     = "integer"
	TypeBigint      = "bigint"
	TypeReal        = "real"
	TypeDouble      = "double precision"
	TypeBoolean     = "boolean"
	TypeTimestamptz = "timestamp with time zone"
	TypeUUID        = "uuid"
	TypeBytea       = "bytea"
	TypeJSONB       = "jsonb"
)

// sqlTypeOf derives the column type of a Go type. size > 0 turns text into varchar(size).
func sqlTypeOf(t reflect.Type, size int) (string, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case timeType:
		return TypeTimestamptz, nil
	case uuidType:
		return TypeUUID, nil
	case bytesType:
		return TypeBytea, nil
	}
	switch t.Kind() {
	case reflect.String:
		if size > 0 {
			return fmt.Sprintf("character varying(%d)", size), nil
		}
		return TypeText, nil
	case reflect.Bool:
		return TypeBoolean, nil
	case reflect.Int8, reflect.Int16, reflect.Uint8:
		return TypeSmallint, nil
	case reflect.Int32, reflect.Uint16:
		return TypeInteger, nil
	case reflect.Int, reflect.Int64, reflect.Uint32:
		return TypeBigint, nil
	case reflect.Float32:
		return TypeReal, nil
	case reflect.Float64:
		return TypeDouble, nil
	case reflect.Slice, reflect.Array:
		// Slices of scalars are Postgres arrays; everything else is stored as JSON.
		if t.Kind() == reflect.Slice {
			if elem, err := sqlTypeOf(t.Elem(), 0); err == nil && elem != TypeJSONB && t.Elem().Kind() != reflect.Slice {
				return elem + "[]", nil
			}
		}
		return TypeJSONB, nil
	case reflect.Map, reflect.Struct:
		if reflect.PointerTo(t).Implements(valuerType) || t.Implements(valuerType) {
			return "", fmt.Errorf("type %s implements driver.Valuer: declare its column type with orm:\"type=...\"", t)
		}
		return TypeJSONB, nil
	}
	return "", fmt.Errorf("unsupported Go type %s: declare the column type with orm:\"type=...\" or skip it with orm:\"-\"", t)
}
