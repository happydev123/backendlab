package mock

import "net/http"

// The counter endpoints give suites a write with an observable side
// effect. POST /{v}/counter/increment applies the increment first and only
// then waits for the optional delay, so a run interrupted while the
// request is in flight still leaves the side effect behind. That is the
// situation that makes retrying writes unsafe, and the reason mutating
// cases require explicit permission and are never retried.

type countBody struct {
	Count int64 `json:"count"`
}

// increment adds one to the counter of v and returns the new value.
func (s *Server) increment(v Variant) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts[v]++
	return s.counts[v]
}

// Count returns the current counter value for v. Tests use it to observe
// side effects without going through HTTP.
func (s *Server) Count(v Variant) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[v]
}

// resetCounters sets every counter to zero.
func (s *Server) resetCounters() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.counts)
}

// handleIncrement serves POST /{v}/counter/increment[?delay=<duration>].
// The delay parameter is validated before anything changes, so a rejected
// request has no side effect. After the increment, the handler waits for
// the delay (capped at MaxDelay); if the client goes away during the wait
// the increment stays applied and no body is written.
func (s *Server) handleIncrement(v Variant) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, d, err := delayParam(r, "delay", 0, s.maxDelay)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		n := s.increment(v)
		extendWriteDeadline(w, d)
		if err := wait(r.Context(), d); err != nil {
			return
		}
		writeJSON(w, http.StatusOK, countBody{Count: n})
	}
}

// handleCount serves GET /{v}/counter.
func (s *Server) handleCount(v Variant) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, countBody{Count: s.Count(v)})
	}
}
