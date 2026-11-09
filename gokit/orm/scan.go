package orm

import (
	"reflect"

	"github.com/jackc/pgx/v5"

	"github.com/PouryDev/server-infrastructure/gokit/postgres"
)

// scanInto reads every row into the slice behind sv (an addressable []T made by the caller), writing the
// columns straight into the struct fields. The slice grows in place, so no intermediate row values and
// no per-row allocation besides what the driver itself needs.
func scanInto(rows pgx.Rows, cols []*Field, sv reflect.Value) error {
	defer rows.Close()
	dest := make([]any, len(cols))
	for rows.Next() {
		n := sv.Len()
		sv.Grow(1)
		sv.SetLen(n + 1)
		elem := sv.Index(n)
		for i, f := range cols {
			dest[i] = elem.FieldByIndex(f.Index).Addr().Interface()
		}
		if err := rows.Scan(dest...); err != nil {
			return postgres.MapError(err)
		}
	}
	return postgres.MapError(rows.Err())
}

// scanAll is the typed entry point: it runs the query and returns []T.
func scanAll[T any](rows pgx.Rows, cols []*Field, hint int) ([]T, error) {
	out := make([]T, 0, hint)
	if err := scanInto(rows, cols, reflect.ValueOf(&out).Elem()); err != nil {
		return nil, err
	}
	return out, nil
}

// scanReflect is scanAll for a model known only at run time (relation loading).
func scanReflect(rows pgx.Rows, m *Model, cols []*Field, hint int) (reflect.Value, error) {
	sv := reflect.New(reflect.SliceOf(m.Type)).Elem()
	sv.Set(reflect.MakeSlice(sv.Type(), 0, hint))
	return sv, scanInto(rows, cols, sv)
}

// scanOne reads the single row of a RETURNING clause into the fields.
func scanReturning(row pgx.Row, elem reflect.Value, fields []*Field) error {
	dest := make([]any, len(fields))
	for i, f := range fields {
		dest[i] = elem.FieldByIndex(f.Index).Addr().Interface()
	}
	return row.Scan(dest...)
}
