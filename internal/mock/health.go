package mock

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

const contentTypeJSON = "application/json"

// writeJSON encodes v and writes it with the given status. Encoding errors
// cannot happen for the fixed response types in this package, but are
// still reported as a 500 rather than ignored.
func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal error: response encoding failed", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", contentTypeJSON)
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

type errorBody struct {
	Error string `json:"error"`
}

// writeError writes a JSON error document.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// newRequestID returns a random identifier. It differs on every call, so
// suites must exclude it from comparison.
func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails on supported platforms
	return "req_" + hex.EncodeToString(b[:])
}

type healthBody struct {
	Status      string `json:"status"`
	RequestID   string `json:"request_id"`
	GeneratedAt string `json:"generated_at"`
}

// handleHealth serves GET /{v}/health. Both variants return the same
// document apart from the volatile request_id and generated_at fields.
func (s *Server) handleHealth(Variant) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, healthBody{
			Status:      "ok",
			RequestID:   newRequestID(),
			GeneratedAt: s.timestamp(),
		})
	}
}
