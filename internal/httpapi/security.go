package httpapi

import (
	"log/slog"
	"net/http"
)

// ContentSecurityPolicy allows scripts, styles and images from the server
// itself only. There are no inline scripts or styles anywhere in the
// dashboard, so no 'unsafe-inline' or nonces are needed. frame-ancestors
// 'none' forbids embedding the pages in frames.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; " +
	"connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'"

// SecurityHeaders sets the response headers every page and API response
// carries. Responses are marked no-store by default because they show
// operator data; static asset handlers may override Cache-Control.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", ContentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// LimitBody bounds the request body to max bytes. A declared
// Content-Length above max is rejected with 413 before the handler runs;
// otherwise the body is wrapped in http.MaxBytesReader so reading past
// max fails and decoders map the error to 413.
func LimitBody(max int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > max {
				writeProblem(w, r, Errorf(http.StatusRequestEntityTooLarge, CodeTooLarge, "request body exceeds %d bytes", max))
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, max)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Stack returns the middleware every server route uses, outermost first:
// request ID, access log, panic recovery and security headers. The
// access log sits outside recovery so a recovered panic is logged as 500.
func Stack(log *slog.Logger) []func(http.Handler) http.Handler {
	return []func(http.Handler) http.Handler{RequestID, AccessLog(log), Recover(log), SecurityHeaders}
}
