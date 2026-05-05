package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

// RequestIDHeader carries the request ID in requests and responses.
const RequestIDHeader = "X-Request-Id"

// clientRequestID is the only shape of client-supplied request ID that is
// kept: 8 to 64 characters from a small alphabet, so a client cannot
// inject log syntax, control characters or very long values. Anything
// else is replaced by a new random ID.
var clientRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

type requestIDKey struct{}

// RequestID assigns each request an ID, stores it in the request context
// and returns it in the X-Request-Id response header. A client-supplied
// ID is reused only when it matches a strict pattern; it is never trusted
// for anything but correlation.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !clientRequestID.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// RequestIDFrom returns the request ID stored by RequestID, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// RequestLogger returns base with the request_id attribute of ctx.
func RequestLogger(ctx context.Context, base *slog.Logger) *slog.Logger {
	if id := RequestIDFrom(ctx); id != "" {
		return base.With(slog.String("request_id", id))
	}
	return base
}

// newRequestID returns "req_" followed by 20 random base32 characters.
func newRequestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported platforms; an ID is
		// only used for correlation, so fall back to a fixed marker.
		return "req_unavailable"
	}
	return "req_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))[:20]
}
