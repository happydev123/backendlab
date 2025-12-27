package mock

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// Terminal marker shared by every stream endpoint: an event of type "done"
// whose data is "[DONE]".
const (
	terminalEvent = "done"
	terminalData  = "[DONE]"
)

// defaultGap is the pause used by the gap stream when no gap is requested.
const defaultGap = 2 * time.Second

// deltaJSON encodes a chat-delta payload. encoding/json writes non-ASCII
// characters as raw UTF-8, which is what the fragmentation tests rely on.
func deltaJSON(text string) string {
	data, _ := json.Marshal(struct {
		Delta string `json:"delta"`
	}{text}) // a struct with one string field always encodes
	return string(data)
}

// chatDeltas returns the text pieces of the chat stream. Both variants
// produce the same concatenated text ("Grüße aus 日本") but split it
// differently, so a chat-delta adapter sees equal output while raw event
// comparison sees different events.
func chatDeltas(v Variant) []string {
	if v == Candidate {
		return []string{"Grü", "ße aus ", "日本"}
	}
	return []string{"Grüße", " aus ", "日本"}
}

// chatPayload builds the complete chat stream. Line endings alternate
// between CRLF and LF per event (both are valid), a comment line precedes
// the terminal event, and the stream ends with the terminal marker.
func chatPayload(v Variant) []byte {
	var b bytes.Buffer
	for i, d := range chatDeltas(v) {
		eol := "\r\n"
		if i%2 == 1 {
			eol = "\n"
		}
		b.Write(eventBlock(eol, strconv.Itoa(i+1), "delta", deltaJSON(d)))
	}
	b.WriteString(": generation finished\r\n\r\n")
	b.Write(eventBlock("\r\n", "", terminalEvent, terminalData))
	return b.Bytes()
}

// gapPayloads returns the parts written before and after the pause.
func gapPayloads() (before, after []byte) {
	before = eventBlock("\n", "1", "delta", deltaJSON("waiting for "))
	var b bytes.Buffer
	b.Write(eventBlock("\n", "2", "delta", deltaJSON("the next token")))
	b.Write(eventBlock("\n", "", terminalEvent, terminalData))
	return before, b.Bytes()
}

// truncatedPayload ends in the middle of an event: two complete delta
// events followed by an unterminated data line, and no terminal marker.
// Per the event-stream format, the incomplete final event is discarded by
// a conforming parser.
func truncatedPayload() []byte {
	var b bytes.Buffer
	b.Write(eventBlock("\r\n", "1", "delta", deltaJSON("This answer ")))
	b.Write(eventBlock("\r\n", "2", "delta", deltaJSON("stops ")))
	b.WriteString(`data: {"delta":"befo`)
	return b.Bytes()
}

// handleChatStream serves GET /{v}/stream/chat.
func (s *Server) handleChatStream(v Variant) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		extendWriteDeadline(w, 0)
		if err := startEventStream(w); err != nil {
			return
		}
		_ = writeFragmented(r.Context(), w, chatPayload(v))
	}
}

// handleGapStream serves GET /{v}/stream/gap?gap=<duration>. It writes one
// event, pauses for the requested gap (default 2s, capped at MaxDelay and
// cut short when the client goes away), then completes the stream.
func (s *Server) handleGapStream(Variant) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, gap, err := delayParam(r, "gap", defaultGap, s.maxDelay)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		extendWriteDeadline(w, gap)
		if err := startEventStream(w); err != nil {
			return
		}
		before, after := gapPayloads()
		if err := writeFragmented(r.Context(), w, before); err != nil {
			return
		}
		if err := wait(r.Context(), gap); err != nil {
			return
		}
		_ = writeFragmented(r.Context(), w, after)
	}
}

// handleTruncatedStream serves GET /{v}/stream/truncated. The response
// ends cleanly at the HTTP level (the chunked body is terminated), but the
// event stream never reaches its terminal marker.
func (s *Server) handleTruncatedStream(Variant) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		extendWriteDeadline(w, 0)
		if err := startEventStream(w); err != nil {
			return
		}
		_ = writeFragmented(r.Context(), w, truncatedPayload())
	}
}
