package stream

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/happydev123/backendlab/internal/finding"
)

const (
	sseTimingLimitation       = "Saved SSE text has no reliable timing: first-event time, gaps and duration are unknown, and none is inferred from position in the file."
	recordingTimingLimitation = "Event times come from recorded chunk offsets; events in the same chunk share one time."
	recordingNoEndLimitation  = "The recording has no end record, so it may have been cut off; it is treated as ending at its last chunk."
	recordingStopLimitation   = "The recording was stopped by the reader before the response body ended; data after that point was not captured."
)

// InspectFile inspects a saved stream file. See InspectReader.
func InspectFile(path string, exp Expectations, opts Options) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open stream file: %w", err)
	}
	defer f.Close()
	return InspectReader(f, filepath.Base(path), exp, opts)
}

// InspectReader inspects a saved stream and detects its format from the
// content: input whose first non-whitespace byte is '{' must be a timed
// recording (see RecordingFormat); anything else is plain SSE text.
// Plain text has no timing, so timing fields stay nil and timing
// expectations are reported as inconclusive.
//
// The whole input is read (a terminal marker does not stop inspection)
// unless the parser stops at a limit or invalid data. An error is returned
// only when the input cannot be read or the recording is malformed;
// problems with the stream itself are findings. name labels error messages.
func InspectReader(r io.Reader, name string, exp Expectations, opts Options) (*Result, error) {
	br := bufio.NewReaderSize(r, readBufSize)
	isRec, err := detectRecording(br)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if isRec {
		res, err := inspectRecording(br, exp, opts)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return res, nil
	}
	res, err := inspectSSE(br, exp, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return res, nil
}

// detectRecording peeks at the start of the input.
func detectRecording(br *bufio.Reader) (bool, error) {
	head, err := br.Peek(512)
	if err != nil && err != io.EOF && !errors.Is(err, bufio.ErrBufferFull) {
		return false, err
	}
	head = bytes.TrimLeft(head, " \t\r\n")
	return len(head) > 0 && head[0] == '{', nil
}

func inspectSSE(br *bufio.Reader, exp Expectations, opts Options) (*Result, error) {
	s := newSession(exp, opts, FormatSSE, false)
	s.res.addLimitation(sseTimingLimitation)
	buf := make([]byte, readBufSize)
	for {
		n, err := br.Read(buf)
		if n > 0 && !s.feed(buf[:n], nil) {
			return s.end(s.stop, nil, ""), nil
		}
		if err == io.EOF {
			return s.end(TermEOF, nil, ""), nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func inspectRecording(br *bufio.Reader, exp Expectations, opts Options) (*Result, error) {
	// Detection guarantees a '{' after optional whitespace, so blank
	// lines before the header are skipped.
	lineNo := 0
	var line []byte
	for len(bytes.TrimSpace(line)) == 0 {
		var err error
		if line, err = readLine(br); err != nil {
			return nil, fmt.Errorf("recording header: %w", err)
		}
		lineNo++
	}
	if err := parseHeader(bytes.TrimSpace(line)); err != nil {
		return nil, err
	}
	rr := &recordingReader{br: br, line: lineNo}
	s := newSession(exp, opts, FormatRecording, true)
	s.res.addLimitation(recordingTimingLimitation)
	for {
		chunk, at, err := rr.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if !s.feed(chunk, &at) {
			return s.end(s.stop, &at, ""), nil
		}
	}
	return endRecording(s, rr), nil
}

// endRecording maps the end record to a termination.
func endRecording(s *session, rr *recordingReader) *Result {
	if rr.end == "" {
		s.res.addLimitation(recordingNoEndLimitation)
		return s.end(TermEOF, nil, "")
	}
	at := rr.endAt
	if !s.checkDuration(at) {
		return s.end(s.stop, &at, "")
	}
	switch rr.end {
	case EndEOF:
		return s.end(TermEOF, &at, "")
	case EndReadError:
		return s.end(TermReadError, &at, "a body read failed during the recorded live read")
	case EndLimit:
		s.sink.add(finding.Finding{
			Code: CodeRecordingTruncated, Kind: finding.KindInconclusive, Severity: finding.SeverityError,
			Message: "the recording stopped at its byte bound; the rest of the stream was not captured",
		})
		s.stop = TermLimitExceeded
		return s.end(TermLimitExceeded, &at, "")
	}
	// EndStopped: the live reader stopped before the body ended.
	if s.terminalSeen() {
		return s.end(TermTerminalEvent, &at, "")
	}
	s.res.addLimitation(recordingStopLimitation)
	switch rr.termination {
	case TermTimeout:
		return s.end(TermTimeout, &at, "recorded live read timed out")
	case TermReadError:
		return s.end(TermReadError, &at, "a body read failed during the recorded live read")
	}
	return s.end(TermCancelled, &at, "")
}
