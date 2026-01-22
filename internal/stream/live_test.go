package stream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/testutil"
)

var clockBase = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fakeClock is a settable clock safe for concurrent use.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: clockBase} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Set moves the clock to clockBase plus d.
func (c *fakeClock) Set(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = clockBase.Add(d)
}

// scriptReader returns one scripted chunk per Read and sets the clock to
// the chunk's time first, so ReadLive sees deterministic receive times.
type scriptReader struct {
	clock  *fakeClock
	chunks []timedChunk
	end    time.Duration
	reads  int
}

func (s *scriptReader) Read(p []byte) (int, error) {
	if s.reads >= len(s.chunks) {
		s.clock.Set(s.end)
		return 0, io.EOF
	}
	c := s.chunks[s.reads]
	s.reads++
	s.clock.Set(c.at)
	return copy(p, c.data), nil
}

func liveOpts(c *fakeClock) Options {
	return Options{CaseID: "live", Now: c.Now, Start: clockBase}
}

func TestReadLiveScriptedTiming(t *testing.T) {
	clock := newFakeClock()
	body := &scriptReader{clock: clock, end: 2 * time.Second, chunks: []timedChunk{
		{300 * time.Millisecond, ": hello\n\n"},
		{450 * time.Millisecond, "event: start\ndata: {}\n\n"},
		{700 * time.Millisecond, "data: a\n\ndata: b\n\n"},
		{1900 * time.Millisecond, "data: c\n\n"},
	}}
	exp := Expectations{MaxGap: time.Second, RequiredEvents: []string{"start"}}
	r := ReadLive(context.Background(), body, exp, liveOpts(clock))
	if r.Termination != TermEOF || r.EventCount != 4 || r.Comments != 1 || r.Format != FormatLive {
		t.Fatalf("result = %+v", r)
	}
	if *r.FirstEvent != 450*time.Millisecond || *r.Duration != 2*time.Second {
		t.Fatalf("first=%v duration=%v", *r.FirstEvent, *r.Duration)
	}
	g := r.Gaps
	if g.Count != 3 || g.Max != 1200*time.Millisecond || g.P50 != 250*time.Millisecond || g.MaxAfter != 3 {
		t.Fatalf("gaps = %+v", g)
	}
	f := findCode(r.Findings, CodeMaxGapExceeded)
	if f == nil || f.Location != "event:3" || f.CaseID != "live" {
		t.Fatalf("findings = %+v", r.Findings)
	}
	if len(r.Limitations) != 1 || !strings.Contains(r.Limitations[0], "same read") {
		t.Fatalf("limitations = %v", r.Limitations)
	}
}

func TestReadLiveMaxDurationFakeClock(t *testing.T) {
	clock := newFakeClock()
	body := &scriptReader{clock: clock, end: 5 * time.Second, chunks: []timedChunk{
		{100 * time.Millisecond, "data: a\n\n"},
		{2 * time.Second, "data: b\n\n"},
	}}
	r := ReadLive(context.Background(), body, Expectations{MaxDuration: time.Second}, liveOpts(clock))
	if r.Termination != TermTimeout || r.EventCount != 1 || findCode(r.Findings, CodeMaxDuration) == nil {
		t.Fatalf("result = %+v", r)
	}
	if body.reads != 2 {
		t.Fatalf("reads = %d; reading must stop once max_duration is exceeded", body.reads)
	}
}

func TestReadLiveEOFAfterMaxDuration(t *testing.T) {
	clock := newFakeClock()
	body := &scriptReader{clock: clock, end: 3 * time.Second, chunks: []timedChunk{{time.Millisecond, "data: a\n\n"}}}
	r := ReadLive(context.Background(), body, Expectations{MaxDuration: time.Second}, liveOpts(clock))
	if r.Termination != TermTimeout || findCode(r.Findings, CodeMaxDuration) == nil {
		t.Fatalf("result = %+v", r)
	}
}

// consumeClock wraps an HTTP body. After each Read it sets the clock to
// the scripted time of the part that contains the last byte received and
// signals the handler, which writes the next part only then. This keeps
// parts from coalescing into one read, so receive times are exact.
type consumeClock struct {
	body     io.Reader
	clock    *fakeClock
	parts    []timedChunk
	consumed []chan struct{}
	total    int
	next     int
}

func (c *consumeClock) Read(p []byte) (int, error) {
	n, err := c.body.Read(p)
	c.total += n
	end := 0
	for _, part := range c.parts {
		end += len(part.data)
		if n > 0 && c.total <= end {
			c.clock.Set(part.at)
			break
		}
	}
	for c.next < len(c.parts) && c.total >= c.partEnd(c.next) {
		close(c.consumed[c.next])
		c.next++
	}
	return n, err
}

// partEnd returns the stream offset just after part i.
func (c *consumeClock) partEnd(i int) int {
	end := 0
	for _, part := range c.parts[:i+1] {
		end += len(part.data)
	}
	return end
}

func TestReadLiveHTTPFragmented(t *testing.T) {
	// Writes split a two-byte character, a four-byte character and a CRLF
	// pair across flushes.
	parts := []timedChunk{
		{10 * time.Millisecond, "data: h\xC3"},
		{30 * time.Millisecond, "\xA9llo\n\n"},
		{100 * time.Millisecond, "data: \xF0\x9F"},
		{250 * time.Millisecond, "\x98\x80 ok\r"},
		{260 * time.Millisecond, "\n\r\n"},
		{400 * time.Millisecond, "data: [DONE]\n\n"},
	}
	consumed := make([]chan struct{}, len(parts))
	for i := range consumed {
		consumed[i] = make(chan struct{})
	}
	release := testutil.Release(t)
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for i, p := range parts {
			if _, err := io.WriteString(w, p.data); err != nil {
				return
			}
			fl.Flush()
			select {
			case <-consumed[i]:
			case <-r.Context().Done():
				return
			case <-release:
				return
			case <-time.After(2 * time.Second):
				return
			}
		}
	}))
	client := &http.Client{Transport: testutil.Transport()}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	clock := newFakeClock()
	body := &consumeClock{body: resp.Body, clock: clock, parts: parts, consumed: consumed}
	exp := Expectations{TerminalData: "[DONE]", RequireTerminal: true, MaxGap: 200 * time.Millisecond}
	r := ReadLive(context.Background(), body, exp, liveOpts(clock))

	if r.Termination != TermTerminalEvent || r.EventCount != 3 || r.TerminalIndex != 2 {
		t.Fatalf("result = %+v", r)
	}
	if r.Events[0].Data != "héllo" || r.Events[1].Data != "😀 ok" {
		t.Fatalf("events = %q, %q", r.Events[0].Data, r.Events[1].Data)
	}
	wantAt := []time.Duration{30 * time.Millisecond, 260 * time.Millisecond, 400 * time.Millisecond}
	for i, ev := range r.Events {
		if ev.At == nil || *ev.At != wantAt[i] {
			t.Fatalf("event %d at %v, want %v", i, ev.At, wantAt[i])
		}
	}
	if *r.FirstEvent != 30*time.Millisecond || *r.Duration != 400*time.Millisecond || *r.MaxGap != 230*time.Millisecond {
		t.Fatalf("first=%v duration=%v maxgap=%v", *r.FirstEvent, *r.Duration, *r.MaxGap)
	}
	if f := findCode(r.Findings, CodeMaxGapExceeded); f == nil || f.Location != "event:1" {
		t.Fatalf("findings = %+v", r.Findings)
	}
	if findCode(r.Findings, CodeMissingTerminal) != nil {
		t.Fatal("terminal marker seen but reported missing")
	}
}

// blockingHandler writes one event, then waits (bounded) without ending
// the response. It signals on wrote after the event is flushed.
func blockingHandler(t *testing.T, wrote chan<- struct{}) http.Handler {
	release := testutil.Release(t)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		if wrote != nil {
			close(wrote)
		}
		select {
		case <-r.Context().Done():
		case <-release:
		case <-time.After(2 * time.Second):
		}
	})
}

func TestReadLiveTimeoutHTTP(t *testing.T) {
	srv := testutil.NewServer(t, blockingHandler(t, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: testutil.Transport()}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := ReadLive(ctx, resp.Body, Expectations{TerminalData: "[DONE]"}, Options{})
	if r.Termination != TermTimeout {
		t.Fatalf("termination = %s", r.Termination)
	}
	f := findCode(r.Findings, CodeTimeout)
	if f == nil || !f.Failing() || f.Observed != context.DeadlineExceeded.Error() {
		t.Fatalf("findings = %+v", r.Findings)
	}
	if r.EventCount != 1 || r.Duration == nil || r.FirstEvent == nil {
		t.Fatalf("result = %+v", r)
	}
	if findCode(r.Findings, CodeEOFBeforeTerminal) != nil {
		t.Fatal("timeout misclassified as EOF")
	}
}

func TestReadLiveCancelHTTP(t *testing.T) {
	wrote := make(chan struct{})
	srv := testutil.NewServer(t, blockingHandler(t, wrote))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: testutil.Transport()}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	go func() {
		<-wrote
		cancel()
	}()
	r := ReadLive(ctx, resp.Body, Expectations{}, Options{})
	if r.Termination != TermCancelled {
		t.Fatalf("termination = %s", r.Termination)
	}
	if f := findCode(r.Findings, CodeCancelled); f == nil || f.Failing() {
		t.Fatalf("findings = %+v", r.Findings)
	}
}

// trackingReader reports when a Read returns, so tests can confirm that
// the reader goroutine finished.
type trackingReader struct {
	r        io.Reader
	reads    atomic.Int32
	returned chan error
	started  chan int32 // optional: receives the count when a Read starts
}

func (tr *trackingReader) Read(p []byte) (int, error) {
	count := tr.reads.Add(1)
	if tr.started != nil {
		tr.started <- count
	}
	n, err := tr.r.Read(p)
	if err != nil {
		select {
		case tr.returned <- err:
		default:
		}
	}
	return n, err
}

func TestReadLiveCancelReleasesGoroutineOnClose(t *testing.T) {
	pr, pw := io.Pipe()
	body := &trackingReader{r: pr, returned: make(chan error, 1), started: make(chan int32, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = pw.Write([]byte("data: a\n\n"))
		// Cancel once the second read has started: it blocks in the pipe,
		// so ReadLive returns while a read is outstanding.
		for n := range body.started {
			if n == 2 {
				cancel()
				return
			}
		}
	}()
	r := ReadLive(ctx, body, Expectations{}, Options{})
	if r.Termination != TermCancelled || r.EventCount != 1 {
		t.Fatalf("result = %+v", r)
	}
	// The abandoned read is still blocked in the pipe; closing the body
	// unblocks it and the goroutine exits.
	_ = pr.Close()
	select {
	case err := <-body.returned:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("read returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader goroutine did not return after the body was closed")
	}
}

func TestReadLiveMaxDurationRealTimer(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	defer pr.Close()
	r := ReadLive(context.Background(), pr, Expectations{MaxDuration: 50 * time.Millisecond}, Options{})
	if r.Termination != TermTimeout || findCode(r.Findings, CodeMaxDuration) == nil || findCode(r.Findings, CodeTimeout) != nil {
		t.Fatalf("result = %+v", r)
	}
}

func TestReadLiveTerminalDoesNotWait(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	body := &trackingReader{r: pr, returned: make(chan error, 1)}
	go func() {
		_, _ = pw.Write([]byte("data: x\n\ndata: [DONE]\n\ndata: late\n\n"))
	}()
	r := ReadLive(context.Background(), body, Expectations{TerminalData: "[DONE]"}, Options{})
	if r.Termination != TermTerminalEvent || r.EventCount != 3 {
		t.Fatalf("result = %+v", r)
	}
	if f := findCode(r.Findings, CodeAfterTerminal); f == nil || f.Location != "event:2" {
		t.Fatalf("findings = %+v", r.Findings)
	}
	// No read is outstanding: ReadLive stopped its goroutine before
	// returning instead of waiting for more data.
	if n := body.reads.Load(); n != 1 {
		t.Fatalf("reads = %d, want 1", n)
	}
	_ = pw.Close()
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, nil }

func TestReadLiveReadErrors(t *testing.T) {
	red := redact.Default().WithSecrets("s3cret-token")
	r := ReadLive(context.Background(), errReader{errors.New("connection reset (s3cret-token)")}, Expectations{}, Options{Redactor: red})
	if r.Termination != TermReadError {
		t.Fatalf("termination = %s", r.Termination)
	}
	f := findCode(r.Findings, CodeReadError)
	if f == nil || !f.Failing() || strings.Contains(f.Observed, "s3cret") || !strings.Contains(f.Observed, "[REDACTED]") {
		t.Fatalf("finding = %+v", f)
	}

	r = ReadLive(context.Background(), emptyReader{}, Expectations{}, Options{})
	if r.Termination != TermReadError || findCode(r.Findings, CodeReadError).Observed != io.ErrNoProgress.Error() {
		t.Fatalf("result = %+v", r)
	}
}

func TestReadLiveContextAlreadyDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body := &trackingReader{r: strings.NewReader("data: a\n\n"), returned: make(chan error, 1)}
	r := ReadLive(ctx, body, Expectations{}, Options{})
	if r.Termination != TermCancelled || body.reads.Load() != 0 {
		t.Fatalf("termination = %s, reads = %d", r.Termination, body.reads.Load())
	}
}
