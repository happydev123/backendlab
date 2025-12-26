package mock

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDelayParam(t *testing.T) {
	const def, limit = time.Second, 10 * time.Second
	tests := []struct {
		query      string
		want       time.Duration
		wantCapped bool
		wantErr    bool
	}{
		{"", def, false, false},
		{"delay=250ms", 250 * time.Millisecond, false, false},
		{"delay=0s", 0, false, false},
		{"delay=10s", limit, false, false},
		{"delay=11s", limit, true, false},
		{"delay=1000h", limit, true, false},
		{"delay=-1s", 0, false, true},
		{"delay=5", 0, false, true},
		{"delay=soon", 0, false, true},
		{"delay=99999999999h", 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/baseline/slow?"+tt.query, nil)
			requested, got, err := delayParam(r, "delay", def, limit)
			capped := got < requested
			if tt.wantErr {
				if !errors.Is(err, errInvalidDelay) {
					t.Fatalf("err = %v, want errInvalidDelay", err)
				}
				return
			}
			if err != nil || got != tt.want || capped != tt.wantCapped {
				t.Fatalf("delayParam = %s, %v, %v; want %s, %v", got, capped, err, tt.want, tt.wantCapped)
			}
		})
	}
}

func TestDelayParamDefaultIsCapped(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/baseline/slow", nil)
	requested, got, err := delayParam(r, "delay", time.Second, time.Millisecond)
	if err != nil || got != time.Millisecond || requested != time.Second {
		t.Fatalf("got %s, %s, %v", requested, got, err)
	}
}

func TestWait(t *testing.T) {
	if err := wait(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("wait elapsed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait on cancelled ctx = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("wait ignored cancellation")
	}
	if err := wait(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("zero wait on cancelled ctx = %v", err)
	}
}

func TestSlowCapsDelay(t *testing.T) {
	s := testServer() // MaxDelay 20ms
	rec := httptest.NewRecorder()
	start := time.Now()
	s.handleSlow(Candidate)(rec, httptest.NewRequest(http.MethodGet, "/candidate/slow?delay=1h", nil))
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("handler took %s despite the 20ms cap", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body slowBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := slowBody{Requested: "1h0m0s", Waited: "20ms", Capped: true}
	if body != want {
		t.Fatalf("body %+v, want %+v", body, want)
	}
}

func TestSlowUncapped(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer().handleSlow(Baseline)(rec, httptest.NewRequest(http.MethodGet, "/baseline/slow?delay=1ms", nil))
	var body slowBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body != (slowBody{Requested: "1ms", Waited: "1ms"}) {
		t.Fatalf("body %+v", body)
	}
}

func TestSlowHonorsCancellation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxDelay = time.Minute
	s := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/baseline/slow?delay=30s", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	start := time.Now()
	s.handleSlow(Baseline)(rec, req)
	if time.Since(start) > 5*time.Second {
		t.Fatal("handler did not return on cancellation")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("cancelled request wrote %q", rec.Body.String())
	}
}

func TestSlowRejectsBadDelay(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer().handleSlow(Baseline)(rec, httptest.NewRequest(http.MethodGet, "/baseline/slow?delay=-5s", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
}
