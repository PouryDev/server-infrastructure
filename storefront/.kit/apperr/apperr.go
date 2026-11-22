// Package apperr is the error vocabulary shared by every layer.
//
// Use cases and repositories return *Error (or wrap one); the transport layer maps the Kind to a
// protocol status (HTTP, gRPC, exit code) and never inspects driver-specific errors.
package apperr

import (
	"errors"
	"fmt"
)

// Kind classifies an error independently of any transport.
type Kind uint8

const (
	KindInternal Kind = iota
	KindInvalid
	KindUnauthorized
	KindForbidden
	KindNotFound
	KindConflict
	KindUnavailable
)

var kindCodes = map[Kind]string{
	KindInternal:     "internal",
	KindInvalid:      "invalid_argument",
	KindUnauthorized: "unauthorized",
	KindForbidden:    "forbidden",
	KindNotFound:     "not_found",
	KindConflict:     "conflict",
	KindUnavailable:  "unavailable",
}

// String returns the default machine-readable code of the kind.
func (k Kind) String() string {
	if c, ok := kindCodes[k]; ok {
		return c
	}
	return kindCodes[KindInternal]
}

// FieldError describes one invalid input field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error is a classified application error.
type Error struct {
	Kind    Kind
	Code    string // stable machine-readable code; defaults to the kind's code
	Message string // safe to show to clients
	Fields  []FieldError
	Err     error // underlying cause, never shown to clients
}

func (e *Error) Error() string {
	switch {
	case e.Err != nil && e.Message != "":
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	case e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	case e.Message != "":
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Err }

// Is lets errors.Is match two *Error values by kind and code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind && t.Code == e.Code
}

// New builds an error. An empty code falls back to the kind's default code.
func New(kind Kind, code, message string) *Error {
	if code == "" {
		code = kind.String()
	}
	return &Error{Kind: kind, Code: code, Message: message}
}

// Wrap attaches a cause to a new classified error.
func Wrap(kind Kind, code, message string, cause error) *Error {
	e := New(kind, code, message)
	e.Err = cause
	return e
}

func Invalid(message string, fields ...FieldError) *Error {
	e := New(KindInvalid, "", message)
	e.Fields = fields
	return e
}

func NotFound(entity string) *Error {
	return New(KindNotFound, "", entity+" not found")
}

func Conflict(message string) *Error     { return New(KindConflict, "", message) }
func Unauthorized(message string) *Error { return New(KindUnauthorized, "", message) }
func Forbidden(message string) *Error    { return New(KindForbidden, "", message) }
func Unavailable(message string) *Error  { return New(KindUnavailable, "", message) }
func Internal(cause error) *Error        { return Wrap(KindInternal, "", "internal error", cause) }
func InvalidField(field, msg string) *Error {
	return Invalid("invalid input", FieldError{Field: field, Message: msg})
}

// As returns the first *Error in err's chain.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// KindOf returns the kind of err; unclassified errors are KindInternal.
func KindOf(err error) Kind {
	if e, ok := As(err); ok {
		return e.Kind
	}
	return KindInternal
}

// Is reports whether err is classified as kind.
func Is(err error, kind Kind) bool {
	e, ok := As(err)
	return ok && e.Kind == kind
}
