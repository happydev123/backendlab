package mock

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"unicode/utf8"
)

// flushRecorder records the bytes written between flushes.
type flushRecorder struct {
	*httptest.ResponseRecorder
	pending bytes.Buffer
	chunks  [][]byte
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (f *flushRecorder) Write(p []byte) (int, error) {
	f.pending.Write(p)
	return f.ResponseRecorder.Write(p)
}

func (f *flushRecorder) Flush() {
	if f.pending.Len() > 0 {
		f.chunks = append(f.chunks, bytes.Clone(f.pending.Bytes()))
		f.pending.Reset()
	}
	f.ResponseRecorder.Flush()
}

type boundaryKinds struct {
	crlf, multibyte, midLine bool
}

// classify reports which awkward boundaries appear between pieces.
func classify(pieces [][]byte) boundaryKinds {
	var k boundaryKinds
	for i := 0; i+1 < len(pieces); i++ {
		end, next := pieces[i], pieces[i+1]
		last, first := end[len(end)-1], next[0]
		switch {
		case last == '\r' && first == '\n':
			k.crlf = true
		case !utf8.RuneStart(first):
			k.multibyte = true
		case last != '\n' && last != '\r':
			k.midLine = true
		}
	}
	return k
}

func TestFragments(t *testing.T) {
	payload := []byte("event: delta\r\ndata: {\"delta\":\"Grüße 日本\"}\r\n\r\n: note\ndata: tail")
	pieces := fragments(payload)
	if got := bytes.Join(pieces, nil); !bytes.Equal(got, payload) {
		t.Fatalf("fragments do not reassemble:\n got %q\nwant %q", got, payload)
	}
	for i, p := range pieces {
		if len(p) == 0 {
			t.Fatalf("fragment %d is empty", i)
		}
	}
	k := classify(pieces)
	if !k.crlf || !k.multibyte || !k.midLine {
		t.Fatalf("missing boundary kinds %+v in %q", k, pieces)
	}
	// "ü", "ß", "日" and "本" are each split once.
	split := 0
	for _, p := range pieces {
		if !utf8.Valid(p) {
			split++
		}
	}
	if split == 0 {
		t.Fatal("no fragment ends or starts inside a multibyte character")
	}
	// Deterministic.
	again := fragments(payload)
	if len(again) != len(pieces) {
		t.Fatal("fragments is not deterministic")
	}
}

func TestFragmentsEdgeCases(t *testing.T) {
	for _, in := range []string{"", "a", "\r\n", "\n\n", "é", "\xff\xfe", "data"} {
		pieces := fragments([]byte(in))
		if got := string(bytes.Join(pieces, nil)); got != in {
			t.Errorf("fragments(%q) reassembles to %q", in, got)
		}
		for _, p := range pieces {
			if len(p) == 0 {
				t.Errorf("fragments(%q) produced an empty piece", in)
			}
		}
	}
}

func TestWriteFragmentedFlushesEachPiece(t *testing.T) {
	payload := eventBlock("\r\n", "1", "delta", `{"delta":"Grüße"}`)
	rec := newFlushRecorder()
	if err := startEventStream(rec); err != nil {
		t.Fatal(err)
	}
	if err := writeFragmented(context.Background(), rec, payload); err != nil {
		t.Fatal(err)
	}
	want := fragments(payload)
	if len(rec.chunks) != len(want) {
		t.Fatalf("%d flushed chunks, want %d: %q", len(rec.chunks), len(want), rec.chunks)
	}
	for i := range want {
		if !bytes.Equal(rec.chunks[i], want[i]) {
			t.Errorf("chunk %d = %q, want %q", i, rec.chunks[i], want[i])
		}
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type %q", ct)
	}
}

func TestWriteFragmentedStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := newFlushRecorder()
	if err := writeFragmented(ctx, rec, []byte("data: x\n\n")); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("wrote %q after cancellation", rec.Body.String())
	}
}

// noFlushWriter does not implement http.Flusher.
type noFlushWriter struct{ http.ResponseWriter }

func TestWriteFragmentedRequiresFlusher(t *testing.T) {
	w := noFlushWriter{httptest.NewRecorder()}
	if err := writeFragmented(context.Background(), w, []byte("data: x\n\n")); err == nil {
		t.Fatal("expected an error when flushing is not supported")
	}
}

func TestEventBlock(t *testing.T) {
	got := string(eventBlock("\n", "", "done", "[DONE]"))
	if got != "event: done\ndata: [DONE]\n\n" {
		t.Fatalf("eventBlock = %q", got)
	}
	got = string(eventBlock("\r\n", "7", "", "x"))
	if got != "id: 7\r\ndata: x\r\n\r\n" {
		t.Fatalf("eventBlock = %q", got)
	}
}
