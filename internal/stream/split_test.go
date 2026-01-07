package stream

import (
	"errors"
	"reflect"
	"testing"

	"github.com/happydev123/backendlab/internal/limits"
)

// sampleStream exercises every framing rule in a short buffer: a BOM,
// LF, CR and CRLF endings, comments, a bare field name, id and retry
// handling (valid and ignored values), multi-line data, an empty data
// event, multibyte characters and an unterminated final event.
const sampleStream = "\xEF\xBB\xBF: open\r\n" +
	"retry: 1500\n" +
	"event: start\r" +
	"data: {\"n\":1}\r\n" +
	"\r\n" +
	"id: 42\n" +
	"data: héllo\n" +
	"data:  wörld 😀\r" +
	"\r" +
	"data\n" +
	"unknown: x\n" +
	"\n" +
	"id: bad\x00id\r\n" +
	"retry: soon\r\n" +
	"event: done\r\n" +
	"data: [DONE]\r\n" +
	"\n" +
	"data: tail ü"

// parseSummary captures everything a parse produces so split and
// whole-buffer parses can be compared.
type parseSummary struct {
	Events   []Event
	ErrCode  string
	Comments int
	Unknown  int
	Notices  []Notice
	Pending  bool
	Discard  int
	Bytes    int64
	LastID   string
}

func summarize(lim limits.Limits, chunks [][]byte) parseSummary {
	events, err, p := parseChunks(lim, chunks...)
	s := parseSummary{Events: events, Comments: p.Comments(), Unknown: p.UnknownFields(),
		Notices: p.Notices(), Bytes: p.Bytes(), LastID: p.LastEventID()}
	var pe *ParseError
	if errors.As(err, &pe) {
		s.ErrCode = pe.Code
		s.Bytes = 0 // bytes accepted before a stop depend on chunking
	} else {
		s.Pending, s.Discard = p.Pending()
	}
	return s
}

func bytewise(data []byte) [][]byte {
	out := make([][]byte, len(data))
	for i := range data {
		out[i] = data[i : i+1]
	}
	return out
}

func TestSampleStreamWhole(t *testing.T) {
	got := summarize(limits.Limits{}, [][]byte{[]byte(sampleStream)})
	want := []Event{
		{Index: 0, Type: "start", Data: `{"n":1}`, Retry: intPtr(1500)},
		{Index: 1, Type: "message", Data: "héllo\n wörld 😀", ID: "42"},
		{Index: 2, Type: "message", Data: "", ID: "42"},
		{Index: 3, Type: "done", Data: "[DONE]", ID: "42"},
	}
	if !reflect.DeepEqual(got.Events, want) {
		t.Fatalf("events = %+v\nwant     %+v", got.Events, want)
	}
	if got.Comments != 1 || got.Unknown != 1 || len(got.Notices) != 2 || !got.Pending || got.ErrCode != "" {
		t.Fatalf("summary = %+v", got)
	}
}

func TestEveryChunkSplitMatchesWhole(t *testing.T) {
	data := []byte(sampleStream)
	want := summarize(limits.Limits{}, [][]byte{data})
	if got := summarize(limits.Limits{}, bytewise(data)); !reflect.DeepEqual(got, want) {
		t.Fatalf("byte-by-byte differs:\n got %+v\nwant %+v", got, want)
	}
	for i := 0; i <= len(data); i++ {
		got := summarize(limits.Limits{}, [][]byte{data[:i], data[i:]})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("split at %d differs:\n got %+v\nwant %+v", i, got, want)
		}
	}
	// Every pair of split points (three chunks, including empty ones).
	for i := 0; i <= len(data); i++ {
		for j := i; j <= len(data); j++ {
			got := summarize(limits.Limits{}, [][]byte{data[:i], data[i:j], data[j:]})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("split at %d,%d differs:\n got %+v\nwant %+v", i, j, got, want)
			}
		}
	}
}

func TestCRAtChunkEndIsOneLineEnding(t *testing.T) {
	// If the CR and LF of each pair were treated as two endings, the
	// empty line between them would dispatch "a" and "b" separately.
	chunks := [][]byte{[]byte("data: a\r"), []byte("\ndata: b\r"), []byte("\n\r"), []byte("\n")}
	got := summarize(limits.Limits{}, chunks)
	want := []Event{{Index: 0, Type: "message", Data: "a\nb"}}
	if !reflect.DeepEqual(got.Events, want) {
		t.Fatalf("events = %+v", got.Events)
	}
}

func TestSplitLimitsAreChunkIndependent(t *testing.T) {
	data := []byte("data: 0123\n\ndata: 0123456789\n\n")
	lim := limits.Limits{MaxLineBytes: 12}
	want := summarize(lim, [][]byte{data})
	if want.ErrCode != CodeLineTooLong || len(want.Events) != 1 {
		t.Fatalf("whole = %+v", want)
	}
	for i := 0; i <= len(data); i++ {
		if got := summarize(lim, [][]byte{data[:i], data[i:]}); !reflect.DeepEqual(got, want) {
			t.Fatalf("split at %d: %+v", i, got)
		}
	}
	// Invalid UTF-8 is detected the same way wherever the chunks split.
	bad := []byte("data: ok\n\ndata: \xE2\x82\n\n")
	wantBad := summarize(limits.Limits{}, [][]byte{bad})
	if wantBad.ErrCode != CodeInvalidUTF8 {
		t.Fatalf("bad = %+v", wantBad)
	}
	if got := summarize(limits.Limits{}, bytewise(bad)); !reflect.DeepEqual(got, wantBad) {
		t.Fatalf("bytewise bad = %+v", got)
	}
}
