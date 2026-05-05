package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/happydev123/backendlab/internal/jsonutil"
)

// maxPanicText bounds the panic value written to the log.
const maxPanicText = 500

// headerTracker records whether a response has started.
type headerTracker struct {
	http.ResponseWriter
	wrote bool
}

func (h *headerTracker) WriteHeader(code int) {
	h.wrote = true
	h.ResponseWriter.WriteHeader(code)
}

func (h *headerTracker) Write(p []byte) (int, error) {
	h.wrote = true
	return h.ResponseWriter.Write(p)
}

func (h *headerTracker) Unwrap() http.ResponseWriter { return h.ResponseWriter }

// Recover turns a handler panic into a 500 response. The panic value and
// stack go to the log with the request ID; the response only says that an
// internal error occurred. If the handler had already started the
// response, the connection is aborted instead. http.ErrAbortHandler is
// passed through unchanged.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tw := &headerTracker{ResponseWriter: w}
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if v == http.ErrAbortHandler {
					panic(v)
				}
				RequestLogger(r.Context(), log).Error("panic while serving request",
					slog.String("route", routePattern(r)),
					slog.String("panic", jsonutil.Truncate(fmt.Sprint(v), maxPanicText)),
					slog.String("stack", string(debug.Stack())),
				)
				if tw.wrote {
					panic(http.ErrAbortHandler)
				}
				writeProblem(tw, r, internalErr())
			}()
			next.ServeHTTP(tw, r)
		})
	}
}
