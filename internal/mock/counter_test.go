package mock

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func postIncrement(s *Server, v Variant, query string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	target := "/" + string(v) + "/counter/increment"
	if query != "" {
		target += "?" + query
	}
	s.handleIncrement(v)(rec, httptest.NewRequest(http.MethodPost, target, nil))
	return rec
}

func decodeCount(t *testing.T, rec *httptest.ResponseRecorder) int64 {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body countBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Count
}

func TestCounterIncrementAndRead(t *testing.T) {
	s := testServer()
	for want := int64(1); want <= 3; want++ {
		if got := decodeCount(t, postIncrement(s, Baseline, "")); got != want {
			t.Fatalf("increment returned %d, want %d", got, want)
		}
	}
	rec := httptest.NewRecorder()
	s.handleCount(Baseline)(rec, httptest.NewRequest(http.MethodGet, "/baseline/counter", nil))
	if got := decodeCount(t, rec); got != 3 {
		t.Fatalf("GET counter = %d, want 3", got)
	}
	if s.Count(Candidate) != 0 {
		t.Fatalf("candidate counter changed: %d", s.Count(Candidate))
	}
}

func TestCounterConcurrent(t *testing.T) {
	s := testServer()
	const workers, each = 16, 25
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		v := Variants()[i%2]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				postIncrement(s, v, "")
			}
		}()
	}
	wg.Wait()
	for _, v := range Variants() {
		if got, want := s.Count(v), int64(workers/2*each); got != want {
			t.Errorf("%s count %d, want %d", v, got, want)
		}
	}
}

func TestCounterSideEffectSurvivesCancellation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxDelay = time.Minute
	s := New(cfg)
	// A context that is already cancelled stands in for a client that goes
	// away while the request is in flight: the handler increments, then
	// its wait ends immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/candidate/counter/increment?delay=30s", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	start := time.Now()
	s.handleIncrement(Candidate)(rec, req)
	if time.Since(start) > 5*time.Second {
		t.Fatal("handler did not return after cancellation")
	}
	if rec.Body.Len() != 0 {
		t.Errorf("interrupted request wrote %q", rec.Body.String())
	}
	if got := s.Count(Candidate); got != 1 {
		t.Fatalf("count after interrupted request = %d, want 1", got)
	}
}

func TestCounterDelayIsCapped(t *testing.T) {
	s := testServer() // MaxDelay 20ms
	start := time.Now()
	if got := decodeCount(t, postIncrement(s, Baseline, "delay=1h")); got != 1 {
		t.Fatalf("count %d", got)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("delay was not capped")
	}
}

func TestCounterRejectsBadDelayWithoutSideEffect(t *testing.T) {
	s := testServer()
	rec := postIncrement(s, Baseline, "delay=-1s")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	if s.Count(Baseline) != 0 {
		t.Fatal("rejected request changed the counter")
	}
}

func TestReset(t *testing.T) {
	s := testServer()
	postIncrement(s, Baseline, "")
	postIncrement(s, Candidate, "")
	rec := httptest.NewRecorder()
	s.handleReset(rec, httptest.NewRequest(http.MethodPost, "/_mock/reset", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"status":"reset","counts":{"baseline":0,"candidate":0}}` {
		t.Fatalf("body %s", got)
	}
	if s.Count(Baseline) != 0 || s.Count(Candidate) != 0 {
		t.Fatal("counters not reset")
	}
	if got := decodeCount(t, postIncrement(s, Baseline, "")); got != 1 {
		t.Fatalf("increment after reset = %d", got)
	}
}

func TestConfigEndpoint(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer().handleConfig(rec, httptest.NewRequest(http.MethodGet, "/_mock/config", nil))
	want := `{"invoice_total_delta":"0.05","max_delay":"20ms","variants":["baseline","candidate"]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("body %s, want %s", got, want)
	}
}
