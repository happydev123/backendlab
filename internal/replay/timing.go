package replay

import (
	"context"
	"net/http/httptrace"
	"sync"
	"time"
)

// Timings are single observations from one run. They describe what
// happened during this request and are not statistical evidence of a
// regression; the report says so in its limitations.

// timer records when a request was sent and when the first response byte
// arrived. httptrace callbacks run on transport goroutines, hence the
// mutex.
type timer struct {
	now       func() time.Time
	start     time.Time
	mu        sync.Mutex
	firstByte time.Time
}

func newTimer(now func() time.Time) *timer {
	return &timer{now: now}
}

// begin marks the send time and returns ctx with a client trace attached.
func (t *timer) begin(ctx context.Context) context.Context {
	t.start = t.now()
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		// With redirects this fires once per hop; the last hop wins,
		// which is the response that is returned.
		GotFirstResponseByte: func() {
			at := t.now()
			t.mu.Lock()
			t.firstByte = at
			t.mu.Unlock()
		},
	})
}

// headersAfter returns the time from send to the first response byte, or
// to fallback when the trace did not fire (for example a response served
// without the trace hook being reached).
func (t *timer) headersAfter(fallback time.Time) time.Duration {
	t.mu.Lock()
	fb := t.firstByte
	t.mu.Unlock()
	if fb.IsZero() {
		fb = fallback
	}
	return nonNegative(fb.Sub(t.start))
}

// since returns the time elapsed from send to now.
func (t *timer) since() time.Duration { return nonNegative(t.now().Sub(t.start)) }

// nonNegative clamps durations from injected or adjusted clocks.
func nonNegative(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}
