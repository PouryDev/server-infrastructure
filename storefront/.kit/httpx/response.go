// Package httpx is the HTTP transport shared by all services: a router with composable middleware,
// JSON request/response helpers, uniform error bodies, authentication middleware, health endpoints,
// OpenTelemetry instrumentation and a server with graceful shutdown. It sits in the outermost
// (adapter) layer: use cases never import it.
package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

var (
	hdrJSON    = []string{"application/json; charset=utf-8"}
	hdrNoStore = []string{"no-store"}
)

// ErrorBody is the JSON shape of every error response.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail describes one error. Message is always safe to show; internal causes are only logged.
type ErrorDetail struct {
	Code      string              `json:"code"`
	Message   string              `json:"message"`
	Fields    []apperr.FieldError `json:"fields,omitempty"`
	RequestID string              `json:"request_id,omitempty"`
}

// JSON writes v as the response body. Responses are not cacheable unless the handler already set a
// Cache-Control header (the CDN in front must never cache API data by accident).
func JSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		slog.Error("encode response", "error", err)
		http.Error(w, `{"error":{"code":"internal","message":"internal error"}}`, http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h["Content-Type"] = hdrJSON
	if _, set := h["Cache-Control"]; !set {
		h["Cache-Control"] = hdrNoStore
	}
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// NoContent writes an empty 204.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// codeStatus lets a specific error code pick a more precise status than its kind would.
var codeStatus = map[string]int{
	"body_too_large":         http.StatusRequestEntityTooLarge,
	"unsupported_media_type": http.StatusUnsupportedMediaType,
}

// StatusOf maps an error to an HTTP status.
func StatusOf(err error) int {
	e, ok := apperr.As(err)
	if !ok {
		return http.StatusInternalServerError
	}
	if s, ok := codeStatus[e.Code]; ok {
		return s
	}
	switch e.Kind {
	case apperr.KindInvalid:
		return http.StatusBadRequest
	case apperr.KindUnauthorized:
		return http.StatusUnauthorized
	case apperr.KindForbidden:
		return http.StatusForbidden
	case apperr.KindNotFound:
		return http.StatusNotFound
	case apperr.KindConflict:
		return http.StatusConflict
	case apperr.KindUnavailable:
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

// WriteError writes the error response for err. Server errors are logged with their cause and shown
// to the client as a generic message; the request id ties the two together.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	status := StatusOf(err)
	detail := ErrorDetail{Code: apperr.KindInternal.String(), Message: "internal error", RequestID: RequestIDFrom(r.Context())}

	var e *apperr.Error
	if errors.As(err, &e) && status < http.StatusInternalServerError {
		detail.Code, detail.Message, detail.Fields = e.Code, e.Message, e.Fields
	} else if errors.As(err, &e) && e.Kind == apperr.KindUnavailable {
		detail.Code, detail.Message = e.Code, e.Message
	}
	if status >= http.StatusInternalServerError {
		LoggerFrom(r.Context()).ErrorContext(r.Context(), "request failed", "error", err.Error(), "status", status)
	}
	switch status {
	case http.StatusUnauthorized:
		w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	case http.StatusServiceUnavailable:
		w.Header().Set("Retry-After", "1")
	}
	JSON(w, status, ErrorBody{Error: detail})
}

// HandlerFunc is an http.HandlerFunc that returns an error; a non-nil error is written with WriteError.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// ServeHTTP makes HandlerFunc an http.Handler.
func (f HandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := f(w, r); err != nil {
		WriteError(w, r, err)
	}
}
