package mock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	s := testServer()
	ids := map[string]bool{}
	for _, v := range Variants() {
		rec := httptest.NewRecorder()
		s.handleHealth(v)(rec, httptest.NewRequest(http.MethodGet, "/"+string(v)+"/health", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", v, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != contentTypeJSON {
			t.Errorf("%s: Content-Type %q", v, ct)
		}
		var body healthBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode: %v", v, err)
		}
		if body.Status != "ok" {
			t.Errorf("%s: status %q", v, body.Status)
		}
		if body.GeneratedAt != "2026-03-14T09:26:53.589793Z" {
			t.Errorf("%s: generated_at %q", v, body.GeneratedAt)
		}
		if !strings.HasPrefix(body.RequestID, "req_") || len(body.RequestID) != 28 {
			t.Errorf("%s: request_id %q", v, body.RequestID)
		}
		ids[body.RequestID] = true
	}
	if len(ids) != 2 {
		t.Errorf("request ids repeated across requests: %v", ids)
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusTeapot, "short and stout")
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status %d", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"short and stout"}` {
		t.Fatalf("body %s", got)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("missing Cache-Control: no-store")
	}
}
