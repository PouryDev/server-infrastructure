package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
)

// Validator is implemented by request DTOs that can check themselves. Validate should return an
// *apperr.Error (apperr.Invalid with field errors); any other error becomes a plain invalid-input error.
type Validator interface{ Validate() error }

// Decode reads one JSON object from the request body into T. It rejects unknown fields, trailing
// data, a wrong content type and (via BodyLimit) oversized bodies, then runs T.Validate if present.
func Decode[T any](r *http.Request) (T, error) {
	var v T
	if ct := r.Header.Get("Content-Type"); ct != "" {
		if mt, _, err := mime.ParseMediaType(ct); err != nil || mt != "application/json" {
			return v, apperr.New(apperr.KindInvalid, "unsupported_media_type", "Content-Type must be application/json")
		}
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, decodeError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return v, apperr.Invalid("request body must contain a single JSON object")
	}
	if val, ok := any(&v).(Validator); ok {
		if err := val.Validate(); err != nil {
			if _, ok := apperr.As(err); ok {
				return v, err
			}
			return v, apperr.Invalid(err.Error())
		}
	}
	return v, nil
}

func decodeError(err error) error {
	var (
		syntax *json.SyntaxError
		typ    *json.UnmarshalTypeError
		tooBig *http.MaxBytesError
	)
	switch {
	case errors.As(err, &tooBig):
		return apperr.New(apperr.KindInvalid, "body_too_large", "request body too large")
	case errors.Is(err, io.EOF):
		return apperr.Invalid("request body is empty")
	case errors.As(err, &syntax), errors.Is(err, io.ErrUnexpectedEOF):
		return apperr.Invalid("request body is not valid JSON")
	case errors.As(err, &typ):
		return apperr.Invalid("invalid input", apperr.FieldError{Field: typ.Field, Message: "must be " + typ.Type.String()})
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		name := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field"), ` "`)
		return apperr.Invalid("invalid input", apperr.FieldError{Field: name, Message: "unknown field"})
	}
	return apperr.Invalid("invalid request body")
}

// ListParams parses filter, sort and pagination parameters from the query string. Validation against
// a schema happens in the repository (query.Schema.Compile).
func ListParams(r *http.Request) (query.Params, error) {
	// r.URL.Query() silently drops malformed pairs (e.g. a ";"), which would hide a broken filter.
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return query.Params{}, apperr.Invalid("malformed query string")
	}
	return query.ParseValues(values)
}

// ClientIP returns the visitor address. nginx sets X-Real-IP from the CDN's real-IP data and the
// application is reachable only through nginx, so the header can be trusted here.
func ClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}
