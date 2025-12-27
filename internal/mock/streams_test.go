package mock

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type sseEvent struct {
	Type, Data string
}

// parseSSE is a minimal event-stream parser for test assertions. It
// returns dispatched events, the number of comment lines, and whether the
// input ended in the middle of an event.
func parseSSE(t *testing.T, raw []byte) (events []sseEvent, comments int, partial bool) {
	t.Helper()
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	// The element after the last newline is an unterminated line.
	partial = lines[len(lines)-1] != ""
	lines = lines[:len(lines)-1]
	var cur sseEvent
	var data []string
	pending := false
	for _, line := range lines {
		switch {
		case line == "":
			if pending {
				cur.Data = strings.Join(data, "\n")
				if cur.Type == "" {
					cur.Type = "message"
				}
				events = append(events, cur)
			}
			cur, data, pending = sseEvent{}, nil, false
		case strings.HasPrefix(line, ":"):
			comments++
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			pending = true
			switch field {
			case "event":
				cur.Type = value
			case "data":
				data = append(data, value)
			}
		}
	}
	return events, comments, partial || pending
}

func chatText(t *testing.T, events []sseEvent) string {
	t.Helper()
	var b strings.Builder
	for _, e := range events {
		if e.Type != "delta" {
			continue
		}
		var p struct{ Delta string }
		if err := json.Unmarshal([]byte(e.Data), &p); err != nil {
			t.Fatalf("delta payload %q: %v", e.Data, err)
		}
		b.WriteString(p.Delta)
	}
	return b.String()
}

func TestChatStream(t *testing.T) {
	s := testServer()
	var rawByVariant [][]byte
	for _, v := range Variants() {
		rec := newFlushRecorder()
		s.handleChatStream(v)(rec, httptest.NewRequest(http.MethodGet, "/"+string(v)+"/stream/chat", nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" {
			t.Fatalf("%s: status %d, Content-Type %q", v, rec.Code, rec.Header().Get("Content-Type"))
		}
		raw := rec.Body.Bytes()
		if !bytes.Equal(raw, chatPayload(v)) {
			t.Fatalf("%s: body differs from payload", v)
		}
		if len(rec.chunks) != len(fragments(raw)) {
			t.Errorf("%s: %d flushed chunks, want %d", v, len(rec.chunks), len(fragments(raw)))
		}
		k := classify(rec.chunks)
		if !k.crlf || !k.multibyte || !k.midLine {
			t.Errorf("%s: missing awkward boundaries %+v", v, k)
		}
		events, comments, partial := parseSSE(t, raw)
		if partial || comments != 1 {
			t.Errorf("%s: partial=%v comments=%d", v, partial, comments)
		}
		last := events[len(events)-1]
		if last.Type != terminalEvent || last.Data != terminalData {
			t.Errorf("%s: last event %+v", v, last)
		}
		if got := chatText(t, events); got != "Grüße aus 日本" {
			t.Errorf("%s: chat text %q", v, got)
		}
		rawByVariant = append(rawByVariant, raw)
	}
	if bytes.Equal(rawByVariant[0], rawByVariant[1]) {
		t.Error("baseline and candidate chat streams should differ in event split")
	}
}

func TestGapStream(t *testing.T) {
	s := testServer() // MaxDelay 20ms caps the requested gap
	rec := newFlushRecorder()
	start := time.Now()
	s.handleGapStream(Baseline)(rec, httptest.NewRequest(http.MethodGet, "/baseline/stream/gap?gap=1h", nil))
	if time.Since(start) > 5*time.Second {
		t.Fatal("gap was not capped")
	}
	events, _, partial := parseSSE(t, rec.Body.Bytes())
	if partial || len(events) != 3 {
		t.Fatalf("events %+v partial=%v", events, partial)
	}
	if events[2].Data != terminalData {
		t.Errorf("last event %+v", events[2])
	}
	if got := chatText(t, events); got != "waiting for the next token" {
		t.Errorf("text %q", got)
	}
}

func TestGapStreamHonorsCancellation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxDelay = time.Minute
	s := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/baseline/stream/gap?gap=30s", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleGapStream(Baseline)(rec, req)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after cancellation")
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(terminalData)) {
		t.Fatal("cancelled stream still wrote the terminal marker")
	}
}

func TestGapStreamRejectsBadGap(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer().handleGapStream(Candidate)(rec, httptest.NewRequest(http.MethodGet, "/candidate/stream/gap?gap=x", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestTruncatedStream(t *testing.T) {
	rec := newFlushRecorder()
	testServer().handleTruncatedStream(Candidate)(rec, httptest.NewRequest(http.MethodGet, "/candidate/stream/truncated", nil))
	raw := rec.Body.Bytes()
	if bytes.Contains(raw, []byte(terminalData)) {
		t.Fatal("truncated stream contains the terminal marker")
	}
	events, _, partial := parseSSE(t, raw)
	if !partial {
		t.Error("truncated stream should end inside an event")
	}
	if len(events) != 2 {
		t.Errorf("%d complete events, want 2", len(events))
	}
	if raw[len(raw)-1] == '\n' {
		t.Error("truncated stream ends with a newline")
	}
}
