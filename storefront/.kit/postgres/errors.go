package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// SQLSTATE codes worth classifying; everything else is an internal error.
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
	codeNotNullViolation    = "23502"
	codeCheckViolation      = "23514"
	codeSerialization       = "40001"
	codeDeadlock            = "40P01"
	codeQueryCanceled       = "57014"
	codeCannotConnectNow    = "57P03"
	codeTooManyConnections  = "53300"
)

// MapError converts a driver error into an *apperr.Error. The original error stays reachable with
// errors.Unwrap, so callers can still inspect it (see IsUniqueViolation). Errors that are already
// classified pass through unchanged.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := apperr.As(err); ok {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.Wrap(apperr.KindNotFound, "", "resource not found", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return apperr.Wrap(apperr.KindUnavailable, "timeout", "the request took too long", err)
	}
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		if pgconn.SafeToRetry(err) || pgconn.Timeout(err) {
			return apperr.Wrap(apperr.KindUnavailable, "", "database temporarily unavailable", err)
		}
		return apperr.Internal(err)
	}
	switch pe.Code {
	case codeUniqueViolation:
		return apperr.Wrap(apperr.KindConflict, "already_exists", "resource already exists", err)
	case codeForeignKeyViolation:
		return apperr.Wrap(apperr.KindConflict, "foreign_key_violation", "resource is referenced by or references another resource", err)
	case codeNotNullViolation, codeCheckViolation:
		return apperr.Wrap(apperr.KindInvalid, "constraint_violation", "value violates a constraint", err)
	case codeSerialization, codeDeadlock:
		return apperr.Wrap(apperr.KindUnavailable, "retry", "conflicting concurrent update, try again", err)
	case codeQueryCanceled, codeCannotConnectNow, codeTooManyConnections:
		return apperr.Wrap(apperr.KindUnavailable, "", "database temporarily unavailable", err)
	}
	// Class 22 (data exception): malformed input such as an invalid UUID or a value out of range.
	if len(pe.Code) == 5 && pe.Code[:2] == "22" {
		return apperr.Wrap(apperr.KindInvalid, "invalid_value", "invalid value", err)
	}
	return apperr.Internal(err)
}

// ConstraintName returns the violated constraint's name when err comes from a constraint violation.
func ConstraintName(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.ConstraintName
	}
	return ""
}

// IsUniqueViolation reports whether err is a unique violation, optionally of a specific constraint
// (pass "" to match any). Use it to turn a generic conflict into a precise domain error.
func IsUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != codeUniqueViolation {
		return false
	}
	return constraint == "" || pe.ConstraintName == constraint
}
