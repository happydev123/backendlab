package mock

import (
	"bytes"
	"context"
	"net/http"
	"sort"
	"unicode/utf8"
)

// The stream endpoints write a valid event stream in deliberately awkward
// pieces, flushing after each one, so a client parser has to cope with the
// framing that real proxies and servers produce:
//
//   - a line split in the middle ("da" | "ta: ..."),
//   - a CRLF line ending split between the CR and the LF,
//   - a multibyte UTF-8 character split between its bytes.
//
// Concatenating the pieces always yields the original payload, so the
// split points change only the transport framing, never the content.

// lineSplitOffset is where a line of sufficient length is split.
const lineSplitOffset = 2

// fragments splits payload at the awkward offsets described above. The
// split is deterministic, every fragment is non-empty, and the fragments
// concatenate to payload.
func fragments(payload []byte) [][]byte {
	cuts := map[int]bool{}
	lineStart := 0
	for i := 0; i < len(payload); {
		b := payload[i]
		switch {
		case b == '\r' || b == '\n':
			if b == '\r' && i+1 < len(payload) && payload[i+1] == '\n' {
				cuts[i+1] = true // between CR and LF
			}
			if i-lineStart > 2*lineSplitOffset {
				cuts[lineStart+lineSplitOffset] = true // inside the line
			}
			i++
			if b == '\r' && i < len(payload) && payload[i] == '\n' {
				i++
			}
			lineStart = i
		case b >= utf8.RuneSelf:
			_, size := utf8.DecodeRune(payload[i:])
			if size > 1 {
				cuts[i+1] = true // inside a multibyte character
			}
			i += size
		default:
			i++
		}
	}
	if len(payload)-lineStart > 2*lineSplitOffset {
		cuts[lineStart+lineSplitOffset] = true
	}

	offsets := make([]int, 0, len(cuts))
	for c := range cuts {
		if c > 0 && c < len(payload) {
			offsets = append(offsets, c)
		}
	}
	sort.Ints(offsets)

	out := make([][]byte, 0, len(offsets)+1)
	prev := 0
	for _, c := range offsets {
		out = append(out, payload[prev:c])
		prev = c
	}
	if prev < len(payload) {
		out = append(out, payload[prev:])
	}
	return out
}

// startEventStream writes the event-stream response headers and flushes
// them so the client sees the response start before the first event.
func startEventStream(w http.ResponseWriter) error {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	// Ask reverse proxies not to buffer the stream.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return http.NewResponseController(w).Flush()
}

// writeFragmented writes payload in the pieces produced by fragments,
// flushing after each piece. It stops early, returning the error, when the
// client goes away or the writer cannot flush.
func writeFragmented(ctx context.Context, w http.ResponseWriter, payload []byte) error {
	rc := http.NewResponseController(w)
	for _, f := range fragments(payload) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := w.Write(f); err != nil {
			return err
		}
		if err := rc.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// eventBlock renders one event with the given line ending ("\n" or
// "\r\n"). Empty fields are omitted; the block ends with a blank line.
func eventBlock(eol, id, event, data string) []byte {
	var b bytes.Buffer
	if id != "" {
		b.WriteString("id: " + id + eol)
	}
	if event != "" {
		b.WriteString("event: " + event + eol)
	}
	b.WriteString("data: " + data + eol)
	b.WriteString(eol)
	return b.Bytes()
}
