// Package httpapi is the HTTP layer of "backendlab serve": shared
// middleware (request IDs, access log, panic recovery, security headers,
// body limits, sessions and CSRF checks) and the JSON API under /api/v1.
// The dashboard package renders HTML on top of the same operations.
//
// Every API error uses one envelope:
//
//	{"error": {"code": "invalid_request", "message": "..."}}
//
// Messages are written for the operator and never contain secrets, raw
// upstream bodies or internal error text; internal failures are logged
// with the request ID and answered with a generic message.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/happydev123/backendlab/internal/jsonutil"
)

// Error codes used in the envelope.
const (
	CodeInvalidRequest     = "invalid_request"
	CodeInvalidSubmission  = "invalid_submission"
	CodeUnknownSuite       = "unknown_suite"
	CodeUnauthenticated    = "unauthenticated"
	CodeInvalidCredentials = "invalid_credentials"
	CodeCSRF               = "csrf_failed"
	CodeWritesNotPermitted = "writes_not_permitted"
	CodeNotFound           = "not_found"
	CodeNoReport           = "no_report"
	CodeMethodNotAllowed   = "method_not_allowed"
	CodeInvalidState       = "invalid_state"
	CodeSuiteChanged       = "suite_changed"
	CodeTooLarge           = "too_large"
	CodeUnsupportedMedia   = "unsupported_media_type"
	CodeThrottled          = "throttled"
	CodeInternal           = "internal"
	CodeUnavailable        = "unavailable"
	CodeNotReady           = "not_ready"
)

// Error is an API error: an HTTP status, a stable code and a message that
// is safe to show. Operations shared with the dashboard return it so both
// front ends answer the same condition with the same status.
type Error struct {
	Status  int
	Code    string
	Message string
	// RetryAfter, when positive, is sent as the Retry-After header.
	RetryAfter time.Duration
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errorf returns an Error with a formatted message.
func Errorf(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

// internalErr is the generic answer to failures that are logged instead.
func internalErr() *Error {
	return &Error{Status: http.StatusInternalServerError, Code: CodeInternal, Message: "internal server error"}
}

type envelope struct {
	Error envelopeBody `json:"error"`
}

type envelopeBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteError writes e as the JSON error envelope.
func WriteError(w http.ResponseWriter, e *Error) {
	if e.RetryAfter > 0 {
		secs := int((e.RetryAfter + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	writeJSON(w, e.Status, envelope{Error: envelopeBody{Code: e.Code, Message: e.Message}})
}

// writeProblem answers with the JSON envelope for API paths and with
// plain text elsewhere. Middleware shared with the dashboard uses it.
func writeProblem(w http.ResponseWriter, r *http.Request, e *Error) {
	if isAPIPath(r.URL.Path) {
		WriteError(w, e)
		return
	}
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(e.RetryAfter/time.Second)))
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(e.Status)
	io.WriteString(w, e.Message+"\n") //nolint:errcheck // client went away
}

func isAPIPath(p string) bool { return p == "/api" || strings.HasPrefix(p, "/api/") }

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		data = []byte(`{"error":{"code":"internal","message":"internal server error"}}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(append(data, '\n')) //nolint:errcheck // client went away
}

// decodeJSON reads one JSON object from the request body into dst.
// Unknown fields, trailing data and non-JSON content types are rejected.
// The body must already be bounded (LimitBody); exceeding the bound maps
// to 413.
func decodeJSON(r *http.Request, dst any) *Error {
	ct := r.Header.Get("Content-Type")
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil || mt != "application/json" {
		return Errorf(http.StatusUnsupportedMediaType, CodeUnsupportedMedia, "request body must be application/json")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return bodyError(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err != nil {
			if e := bodyError(err); e.Status == http.StatusRequestEntityTooLarge {
				return e
			}
		}
		return Errorf(http.StatusBadRequest, CodeInvalidRequest, "request body must hold exactly one JSON object")
	}
	return nil
}

// bodyError maps a body read or decode error to an API error without
// echoing request content.
func bodyError(err error) *Error {
	var tooBig *http.MaxBytesError
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &tooBig):
		return Errorf(http.StatusRequestEntityTooLarge, CodeTooLarge, "request body exceeds %d bytes", tooBig.Limit)
	case errors.Is(err, io.EOF):
		return Errorf(http.StatusBadRequest, CodeInvalidRequest, "request body is empty")
	case errors.As(err, &syntax), errors.Is(err, io.ErrUnexpectedEOF):
		return Errorf(http.StatusBadRequest, CodeInvalidRequest, "request body is not valid JSON")
	case errors.As(err, &typ):
		return Errorf(http.StatusBadRequest, CodeInvalidRequest, "field %q has the wrong type", typ.Field)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		// encoding/json has no typed error for unknown fields.
		return Errorf(http.StatusBadRequest, CodeInvalidRequest, "unknown field %s", jsonutil.Truncate(strings.TrimPrefix(err.Error(), "json: unknown field "), 64))
	}
	return Errorf(http.StatusBadRequest, CodeInvalidRequest, "request body could not be read")
}
