package mock

import "net/http"

// Control endpoints live under /_mock, outside both variant prefixes, so a
// suite targeting /baseline or /candidate cannot reach them through a case
// path (suite paths may not contain dot segments). They are development
// controls with no authentication: the service binds to loopback by
// default and must never be exposed on a shared network.

type resetBody struct {
	Status string            `json:"status"`
	Counts map[Variant]int64 `json:"counts"`
}

// handleReset serves POST /_mock/reset. It sets both counters to zero and
// returns the values after the reset.
func (s *Server) handleReset(w http.ResponseWriter, _ *http.Request) {
	s.resetCounters()
	counts := make(map[Variant]int64, 2)
	for _, v := range Variants() {
		counts[v] = s.Count(v)
	}
	writeJSON(w, http.StatusOK, resetBody{Status: "reset", Counts: counts})
}

type configBody struct {
	InvoiceTotalDelta string    `json:"invoice_total_delta"`
	MaxDelay          string    `json:"max_delay"`
	Variants          []Variant `json:"variants"`
}

// handleConfig serves GET /_mock/config, which reports the effective
// settings so a person reading a comparison report can confirm which drift
// the candidate was configured with.
func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, configBody{
		InvoiceTotalDelta: s.delta.StringFixed(2),
		MaxDelay:          s.maxDelay.String(),
		Variants:          Variants(),
	})
}
