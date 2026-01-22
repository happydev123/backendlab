package stream

import (
	"context"
	"errors"
	"io"
	"time"
)

// readBufSize is the size of one body read.
const readBufSize = 32 << 10

// maxEmptyReads bounds consecutive reads that return no data and no
// error, which io.Reader discourages but does not forbid.
const maxEmptyReads = 100

const liveTimingLimitation = "Event times are taken when a body read returns; events delivered in the same read share one time, so gaps shorter than one read are reported as zero."

// ReadLive reads an SSE response body as it arrives and checks it
// against exp. Timings are measured with opts.Now relative to opts.Start.
//
// Reading stops at EOF, at the terminal marker (the rest of the chunk
// that carried it is still parsed, so events after the marker in that
// chunk are reported; later chunks are not awaited), at a parser stop
// (limit or invalid data), or when ctx is done or exp.MaxDuration elapses.
//
// A blocked Read cannot be interrupted portably, so ReadLive performs
// reads in one goroutine it owns. When ReadLive stops for any reason other
// than ctx or MaxDuration, no read is in progress and the goroutine has
// exited before ReadLive returns. When ctx is done or MaxDuration elapses
// while a read is in progress, ReadLive returns immediately and the
// goroutine stays inside that single body.Read call until it returns; it
// then exits without touching anything but opts.Now. The caller must
// therefore close the body (for an HTTP response, close resp.Body or
// cancel the request context) after ReadLive returns, which unblocks the
// read. ReadLive never closes body itself.
func ReadLive(ctx context.Context, body io.Reader, exp Expectations, opts Options) *Result {
	opts = opts.withDefaults()
	if opts.Start.IsZero() {
		opts.Start = opts.Now()
	}
	s := newSession(exp, opts, FormatLive, true)
	s.res.addLimitation(liveTimingLimitation)
	since := func(t time.Time) *time.Duration {
		d := t.Sub(opts.Start)
		if d < 0 {
			d = 0
		}
		return &d
	}

	runCtx := ctx
	if exp.MaxDuration > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, max(exp.MaxDuration-opts.Now().Sub(opts.Start), 0))
		defer cancel()
	}
	if runCtx.Err() != nil {
		return endOnContext(s, ctx, *since(opts.Now()))
	}

	rd := startReader(body, opts.Now)
	empty := 0
	for {
		select {
		case r := <-rd.results:
			at := since(r.at)
			if r.n > 0 {
				empty = 0
				if !s.feed(rd.buf[:r.n], at) {
					rd.stop()
					return s.end(s.stop, at, "")
				}
				if s.terminalSeen() {
					rd.stop()
					return s.end(TermTerminalEvent, at, "")
				}
			}
			switch {
			case r.err == io.EOF:
				rd.stop()
				if !s.checkDuration(*at) {
					return s.end(s.stop, at, "")
				}
				return s.end(TermEOF, at, "")
			case r.err != nil:
				rd.stop()
				if runCtx.Err() != nil {
					// Request bodies fail with the context error once the
					// request context ends; classify by the context.
					return endOnContext(s, ctx, *at)
				}
				return s.end(TermReadError, at, opts.Redactor.Error(r.err))
			case r.n == 0:
				empty++
				if empty >= maxEmptyReads {
					rd.stop()
					return s.end(TermReadError, at, io.ErrNoProgress.Error())
				}
			}
			rd.next()
		case <-runCtx.Done():
			rd.abandon()
			return endOnContext(s, ctx, *since(opts.Now()))
		}
	}
}

// endOnContext classifies a stop caused by ctx or by the MaxDuration
// deadline derived from it.
func endOnContext(s *session, ctx context.Context, at time.Duration) *Result {
	switch err := ctx.Err(); {
	case errors.Is(err, context.Canceled):
		return s.end(TermCancelled, &at, err.Error())
	case err != nil:
		return s.end(TermTimeout, &at, err.Error())
	}
	// ctx is still live, so the derived MaxDuration deadline fired.
	s.maxDuration(at)
	return s.end(TermTimeout, &at, "")
}

type readResult struct {
	n   int
	err error
	at  time.Time
}

// bodyReader performs body reads on request, one at a time, in a
// goroutine. Each result carries the time the read returned.
type bodyReader struct {
	body    io.Reader
	now     func() time.Time
	buf     []byte
	req     chan struct{}
	results chan readResult
	exited  chan struct{}
}

func startReader(body io.Reader, now func() time.Time) *bodyReader {
	r := &bodyReader{
		body:    body,
		now:     now,
		buf:     make([]byte, readBufSize),
		req:     make(chan struct{}, 1),
		results: make(chan readResult, 1),
		exited:  make(chan struct{}),
	}
	go r.loop()
	r.next()
	return r
}

func (r *bodyReader) loop() {
	defer close(r.exited)
	for range r.req {
		n, err := r.body.Read(r.buf)
		// results has room for one value and only one read is ever
		// outstanding, so this send never blocks, even after abandon.
		r.results <- readResult{n: n, err: err, at: r.now()}
	}
}

// next requests one more read. The buffer from the previous result must
// no longer be in use.
func (r *bodyReader) next() { r.req <- struct{}{} }

// stop ends the goroutine when no read is outstanding and waits for it.
func (r *bodyReader) stop() {
	close(r.req)
	<-r.exited
}

// abandon ends the goroutine without waiting: a read is outstanding and
// returns only when the body delivers data, fails, or is closed.
func (r *bodyReader) abandon() { close(r.req) }
