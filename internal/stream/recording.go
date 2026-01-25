package stream

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/happydev123/backendlab/internal/jsonutil"
)

// RecordingFormat identifies the timed recording format, a JSON Lines
// file. The first line is the header {"format":"backendlab.sse-recording/v1"}.
// Every further line is one record:
//
//	{"offset_ms":12.5,"chunk":"data: hi\n\n"}     chunk received (valid UTF-8)
//	{"offset_ms":13,"chunk_b64":"8J+Y"}           chunk received (raw bytes)
//	{"offset_ms":40,"end":"eof"}                  how recording ended
//
// offset_ms is milliseconds since the start of the request, a JSON number
// that never decreases. A chunk record carries exactly one of chunk or
// chunk_b64 (standard base64, used when the received bytes are not valid
// UTF-8 on their own, for example a multibyte character split across
// reads). The optional last record has "end" set to one of the End*
// values and, for EndStopped, may name the live Termination.
const RecordingFormat = "backendlab.sse-recording/v1"

// End reasons written in the final record.
const (
	EndEOF       = "eof"            // the body ended
	EndReadError = "read_error"     // a body read failed
	EndStopped   = "stopped"        // the reader stopped before the body ended
	EndLimit     = "limit_exceeded" // the recording bound was reached
)

const (
	// maxRecordLine bounds one line of a recording file.
	maxRecordLine = 8 << 20
	// maxRecordChunk bounds the bytes stored in one chunk record; longer
	// reads are split into several records with the same offset.
	maxRecordChunk = 1 << 20
	// maxOffset bounds offset_ms values.
	maxOffset = 24 * time.Hour
)

type recordHeader struct {
	Format string `json:"format"`
}

type record struct {
	OffsetMS    json.Number `json:"offset_ms"`
	Chunk       *string     `json:"chunk,omitempty"`
	ChunkB64    *string     `json:"chunk_b64,omitempty"`
	End         string      `json:"end,omitempty"`
	Termination Termination `json:"termination,omitempty"`
}

// recordingReader decodes chunk records after the header.
type recordingReader struct {
	br   *bufio.Reader
	line int
	last time.Duration
	// end and endAt are set once the end record was read.
	end         string
	endAt       time.Duration
	termination Termination
}

// readLine returns the next line without its terminator, bounded by
// maxRecordLine. It returns io.EOF only when no bytes remain.
func readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		frag, err := br.ReadSlice('\n')
		if len(line)+len(frag) > maxRecordLine {
			return nil, fmt.Errorf("recording line exceeds %d bytes", maxRecordLine)
		}
		line = append(line, frag...)
		switch {
		case err == nil:
			return bytes.TrimRight(line, "\r\n"), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case err == io.EOF && len(line) > 0:
			return line, nil
		default:
			return nil, err
		}
	}
}

// parseHeader checks the first line of a recording.
func parseHeader(line []byte) error {
	var h recordHeader
	if err := jsonutil.DecodeStrict(line, &h); err != nil {
		return fmt.Errorf("recording header: %w", err)
	}
	if h.Format != RecordingFormat {
		return fmt.Errorf("unsupported recording format %q (want %q)", h.Format, RecordingFormat)
	}
	return nil
}

// next returns the next chunk and its offset, or io.EOF after the last
// record. Malformed records are errors naming the line.
func (rr *recordingReader) next() ([]byte, time.Duration, error) {
	for {
		raw, err := readLine(rr.br)
		if err == io.EOF {
			return nil, 0, io.EOF
		}
		rr.line++
		if err != nil {
			return nil, 0, fmt.Errorf("recording line %d: %w", rr.line, err)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		if rr.end != "" {
			return nil, 0, fmt.Errorf("recording line %d: data after the end record", rr.line)
		}
		chunk, at, err := rr.decode(raw)
		if err != nil {
			return nil, 0, fmt.Errorf("recording line %d: %w", rr.line, err)
		}
		if rr.end == "" {
			return chunk, at, nil
		}
		// The end record must be last; the loop continues to check that
		// only blank lines follow it.
	}
}

func (rr *recordingReader) decode(raw []byte) ([]byte, time.Duration, error) {
	var rec record
	if err := jsonutil.DecodeStrict(raw, &rec); err != nil {
		return nil, 0, err
	}
	at, err := parseOffset(rec.OffsetMS)
	if err != nil {
		return nil, 0, err
	}
	if at < rr.last {
		return nil, 0, fmt.Errorf("offset_ms %s is smaller than the previous offset", rec.OffsetMS)
	}
	rr.last = at
	set := 0
	for _, ok := range []bool{rec.Chunk != nil, rec.ChunkB64 != nil, rec.End != ""} {
		if ok {
			set++
		}
	}
	if set != 1 {
		return nil, 0, errors.New("record needs exactly one of chunk, chunk_b64 or end")
	}
	if rec.Termination != "" && rec.End != EndStopped {
		return nil, 0, errors.New("termination is only allowed on an end record with end \"stopped\"")
	}
	switch {
	case rec.Chunk != nil:
		return []byte(*rec.Chunk), at, nil
	case rec.ChunkB64 != nil:
		data, err := base64.StdEncoding.DecodeString(*rec.ChunkB64)
		if err != nil {
			return nil, 0, fmt.Errorf("chunk_b64: %w", err)
		}
		return data, at, nil
	}
	switch rec.End {
	case EndEOF, EndReadError, EndStopped, EndLimit:
	default:
		return nil, 0, fmt.Errorf("unknown end reason %q", rec.End)
	}
	if rec.Termination != "" && !rec.Termination.Known() {
		return nil, 0, fmt.Errorf("unknown termination %q", rec.Termination)
	}
	rr.end, rr.endAt, rr.termination = rec.End, at, rec.Termination
	return nil, at, nil
}

// parseOffset converts offset_ms to a duration, rejecting negative,
// non-finite and oversized values.
func parseOffset(n json.Number) (time.Duration, error) {
	if n == "" {
		return 0, errors.New("offset_ms is required")
	}
	ms, err := strconv.ParseFloat(string(n), 64)
	if err != nil || math.IsNaN(ms) || math.IsInf(ms, 0) {
		return 0, fmt.Errorf("offset_ms %q is not a finite number", n)
	}
	if ms < 0 || ms > float64(maxOffset/time.Millisecond) {
		return 0, fmt.Errorf("offset_ms %s is outside 0..%d", n, maxOffset/time.Millisecond)
	}
	return time.Duration(math.Round(ms * float64(time.Millisecond))), nil
}

// formatOffset renders d as offset_ms with microsecond precision.
func formatOffset(d time.Duration) json.Number {
	return json.Number(strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', -1, 64))
}
