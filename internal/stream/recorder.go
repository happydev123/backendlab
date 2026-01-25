package stream

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
	"unicode/utf8"
)

// Recorder wraps a live response body and writes everything read through
// it to w in the RecordingFormat, so the stream can be inspected later
// with its timing. Reads pass through unchanged; a failure to write the
// recording never alters what the reader sees and is reported by Close.
//
// The recorded chunk bytes are bounded by Options.Limits.MaxStreamBytes.
// When the bound is reached the recorder writes an end record with
// EndLimit and records nothing further.
//
// Read may be called from ReadLive's reader goroutine while Close is
// called from the caller's goroutine; the methods are synchronized.
type Recorder struct {
	mu       sync.Mutex
	body     io.Reader
	w        io.Writer
	now      func() time.Time
	start    time.Time
	max      int64
	recorded int64
	last     time.Duration
	ended    bool
	err      error
}

// NewRecorder writes the recording header to w and returns a reader
// wrapping body. Offsets are measured with opts.Now from opts.Start
// (defaulting to the current time).
func NewRecorder(body io.Reader, w io.Writer, opts Options) (*Recorder, error) {
	opts = opts.withDefaults()
	if opts.Start.IsZero() {
		opts.Start = opts.Now()
	}
	r := &Recorder{body: body, w: w, now: opts.Now, start: opts.Start, max: opts.Limits.MaxStreamBytes}
	header, err := json.Marshal(recordHeader{Format: RecordingFormat})
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(append(header, '\n')); err != nil {
		return nil, fmt.Errorf("write recording header: %w", err)
	}
	return r, nil
}

// Read reads from the body and records what it returned.
func (r *Recorder) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended || r.err != nil {
		return n, err
	}
	at := r.offset()
	if n > 0 {
		data := p[:n]
		over := int64(n) > r.max-r.recorded
		if over {
			data = data[:r.max-r.recorded]
		}
		r.recorded += int64(len(data))
		for len(data) > 0 && r.err == nil {
			part := data[:min(len(data), maxRecordChunk)]
			data = data[len(part):]
			r.writeChunk(at, part)
		}
		if over {
			r.writeEnd(at, EndLimit, "")
			return n, err
		}
	}
	switch {
	case err == io.EOF:
		r.writeEnd(at, EndEOF, "")
	case err != nil:
		r.writeEnd(at, EndReadError, "")
	}
	return n, err
}

// Close ends the recording. If the body has not ended it writes an end
// record with EndStopped. It does not close the body or w, and returns
// the first error from writing the recording.
func (r *Recorder) Close() error { return r.Finish("") }

// Finish is Close with the live Termination noted in the end record when
// the body had not ended, so a later inspection can classify a timeout or
// cancellation the same way the live read did.
func (r *Recorder) Finish(term Termination) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ended && r.err == nil {
		if term != "" && !term.Known() {
			term = ""
		}
		r.writeEnd(r.offset(), EndStopped, term)
	}
	r.ended = true
	return r.err
}

// Recorded returns the number of chunk bytes recorded.
func (r *Recorder) Recorded() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recorded
}

// offset returns a non-decreasing time since start.
func (r *Recorder) offset() time.Duration {
	at := r.now().Sub(r.start)
	if at < r.last {
		at = r.last
	}
	if at > maxOffset {
		at = maxOffset
	}
	r.last = at
	return at
}

func (r *Recorder) writeChunk(at time.Duration, data []byte) {
	rec := record{OffsetMS: formatOffset(at)}
	if utf8.Valid(data) {
		s := string(data)
		rec.Chunk = &s
	} else {
		s := base64.StdEncoding.EncodeToString(data)
		rec.ChunkB64 = &s
	}
	r.write(rec)
}

func (r *Recorder) writeEnd(at time.Duration, reason string, term Termination) {
	r.write(record{OffsetMS: formatOffset(at), End: reason, Termination: term})
	r.ended = true
}

func (r *Recorder) write(rec record) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil {
		r.err = err
		return
	}
	if _, err := r.w.Write(buf.Bytes()); err != nil {
		r.err = fmt.Errorf("write recording: %w", err)
	}
}
