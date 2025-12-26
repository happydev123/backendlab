package mock

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// writeDeadlineMargin is added to a handler's expected run time when it
// extends the connection write deadline, covering the response itself.
const writeDeadlineMargin = 10 * time.Second

// errInvalidDelay reports a malformed delay or gap query parameter.
var errInvalidDelay = errors.New("invalid duration")

// delayParam reads the duration query parameter name. A missing parameter
// yields def. It returns the requested duration and the effective one,
// which is capped at limit. Negative and malformed values are rejected.
func delayParam(r *http.Request, name string, def, limit time.Duration) (requested, effective time.Duration, err error) {
	requested = def
	if raw := r.URL.Query().Get(name); raw != "" {
		requested, err = time.ParseDuration(raw)
		if err != nil {
			return 0, 0, fmt.Errorf("%w: %s=%q is not a Go duration such as 250ms or 2s", errInvalidDelay, name, raw)
		}
		if requested < 0 {
			return 0, 0, fmt.Errorf("%w: %s must not be negative", errInvalidDelay, name)
		}
	}
	return requested, min(requested, limit), nil
}

// wait blocks for d or until ctx is done, whichever is first. It returns
// ctx.Err() when the context ended the wait.
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// extendWriteDeadline lets one response run past the http.Server
// WriteTimeout by up to d plus a margin. Response writers that do not
// support deadlines (such as httptest.ResponseRecorder) are left alone.
func extendWriteDeadline(w http.ResponseWriter, d time.Duration) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d + writeDeadlineMargin))
}

type slowBody struct {
	Requested string `json:"requested"`
	Waited    string `json:"waited"`
	Capped    bool   `json:"capped"`
}

// handleSlow serves GET /{v}/slow?delay=<duration>. It waits for the
// requested delay (default 1s, capped at the server's MaxDelay) and then
// reports what it did. If the client disconnects or times out first, the
// handler returns without writing a body.
func (s *Server) handleSlow(Variant) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requested, d, err := delayParam(r, "delay", time.Second, s.maxDelay)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		extendWriteDeadline(w, d)
		if err := wait(r.Context(), d); err != nil {
			return
		}
		writeJSON(w, http.StatusOK, slowBody{
			Requested: requested.String(),
			Waited:    d.String(),
			Capped:    d < requested,
		})
	}
}
