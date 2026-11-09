package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
)

func TestMapError(t *testing.T) {
	pg := func(code, constraint string) error {
		return fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: code, ConstraintName: constraint})
	}
	cases := []struct {
		name string
		err  error
		want apperr.Kind
	}{
		{"no rows", pgx.ErrNoRows, apperr.KindNotFound},
		{"unique", pg("23505", "users_email_key"), apperr.KindConflict},
		{"fk", pg("23503", ""), apperr.KindConflict},
		{"not null", pg("23502", ""), apperr.KindInvalid},
		{"bad uuid text", pg("22P02", ""), apperr.KindInvalid},
		{"deadlock", pg("40P01", ""), apperr.KindUnavailable},
		{"too many conns", pg("53300", ""), apperr.KindUnavailable},
		{"timeout", context.DeadlineExceeded, apperr.KindUnavailable},
		{"syntax error", pg("42601", ""), apperr.KindInternal},
		{"unknown", errors.New("boom"), apperr.KindInternal},
	}
	for _, c := range cases {
		if got := apperr.KindOf(MapError(c.err)); got != c.want {
			t.Errorf("%s: kind = %v, want %v", c.name, got, c.want)
		}
	}
	if MapError(nil) != nil {
		t.Error("nil must stay nil")
	}
	already := apperr.Forbidden("no")
	if !errors.Is(MapError(already), already) {
		t.Error("classified errors must pass through")
	}
}

func TestIsUniqueViolation(t *testing.T) {
	err := MapError(&pgconn.PgError{Code: "23505", ConstraintName: "users_email_key"})
	if !IsUniqueViolation(err, "users_email_key") || !IsUniqueViolation(err, "") || IsUniqueViolation(err, "other") {
		t.Fatal("constraint matching broken")
	}
	if ConstraintName(err) != "users_email_key" {
		t.Fatal("ConstraintName")
	}
}

func TestBuildListSQL(t *testing.T) {
	st := query.Statement{Where: "u.age > $2", OrderBy: "u.id ASC", Limit: 10, Offset: 20}
	list, count := buildListSQL(ListQuery{
		Select: "SELECT u.id", From: "FROM users u", Where: "u.tenant_id = $1 OR u.public", Args: []any{1},
	}, st)
	wantList := "SELECT u.id FROM users u WHERE (u.tenant_id = $1 OR u.public) AND u.age > $2 ORDER BY u.id ASC LIMIT 11 OFFSET 20"
	wantCount := "SELECT count(*) FROM users u WHERE (u.tenant_id = $1 OR u.public) AND u.age > $2"
	if list != wantList || count != wantCount {
		t.Fatalf("\n%s\n%s", list, count)
	}

	list, count = buildListSQL(ListQuery{Select: "SELECT 1", From: "FROM t"}, query.Statement{Limit: 5})
	if list != "SELECT 1 FROM t LIMIT 6 OFFSET 0" || count != "SELECT count(*) FROM t" {
		t.Fatalf("\n%s\n%s", list, count)
	}
}
