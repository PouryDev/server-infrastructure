package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Select runs a query and scans every row into T, matching columns to fields by `db` tag (columns
// without a field and fields without a column are both tolerated). It never returns a nil slice.
func Select[T any](ctx context.Context, q Querier, sql string, args ...any) ([]T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, MapError(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[T])
	if err != nil {
		return nil, MapError(err)
	}
	if out == nil {
		out = []T{}
	}
	return out, nil
}

// SelectOne scans exactly one row; no row is an apperr.KindNotFound error.
func SelectOne[T any](ctx context.Context, q Querier, sql string, args ...any) (T, error) {
	var zero T
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return zero, MapError(err)
	}
	v, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[T])
	if err != nil {
		return zero, MapError(err)
	}
	return v, nil
}

// Value scans the first column of the first row into a scalar (count(*), EXISTS, ...).
func Value[T any](ctx context.Context, q Querier, sql string, args ...any) (T, error) {
	var zero T
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return zero, MapError(err)
	}
	v, err := pgx.CollectOneRow(rows, pgx.RowTo[T])
	if err != nil {
		return zero, MapError(err)
	}
	return v, nil
}

// Exec runs a statement and returns the number of affected rows.
func Exec(ctx context.Context, q Querier, sql string, args ...any) (int64, error) {
	tag, err := q.Exec(ctx, sql, args...)
	if err != nil {
		return 0, MapError(err)
	}
	return tag.RowsAffected(), nil
}
