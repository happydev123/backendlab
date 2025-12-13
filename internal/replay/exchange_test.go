package replay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/testutil"
)

// fakeClock advances by step on every call.
type fakeClock struct {
	mu   sync.Mutex
	now  time.Time
	step time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(c.step)
	return c.now
}

func TestSendAndRead(t *testing.T) {
	t.Setenv(testutil.EnvAsyncClose, "1")
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Kind", r.URL.Path)
		if r.URL.Path == "/big" {
			io.WriteString(w, strings.Repeat("b", 4096))
			return
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	client := &http.Client{Transport: testutil.Transport()}
	clock := &fakeClock{now: time.Unix(0, 0), step: time.Millisecond}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/small", nil)
	s, err := send(client, req, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.readAll(1024)
	if err != nil {
		t.Fatal(err)
	}
	if rec.status != 200 || string(rec.body) != `{"ok":true}` || rec.bytes != 11 || rec.header.Get("X-Kind") != "/small" {
		t.Fatalf("received %+v", rec)
	}
	if rec.headers <= 0 || rec.total <= rec.headers {
		t.Fatalf("timings headers=%s total=%s", rec.headers, rec.total)
	}

	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/big", nil)
	s, err = send(client, req, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	rec, err = s.readAll(1024)
	if !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("expected too large, got %v", err)
	}
	if rec.status != 200 || rec.body != nil {
		t.Fatalf("partial result %+v", rec)
	}
}

func TestTimerFallback(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0), step: time.Second}
	tm := newTimer(clock.Now)
	tm.begin(context.Background())
	if d := tm.headersAfter(time.Unix(103, 0)); d != 2*time.Second {
		t.Fatalf("fallback headers time = %s", d)
	}
	if d := tm.headersAfter(time.Unix(50, 0)); d != 0 {
		t.Fatalf("negative duration not clamped: %s", d)
	}
}
