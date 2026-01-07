package stream

import (
	"errors"
	"reflect"
	"testing"

	"github.com/happydev123/backendlab/internal/limits"
)

func intPtr(v int) *int { return &v }

// parseChunks feeds chunks to a new parser and returns all events, the
// parse error (if any) and the parser for inspecting counters.
func parseChunks(lim limits.Limits, chunks ...[]byte) ([]Event, error, *Parser) {
	p := NewParser(lim)
	var all []Event
	for _, c := range chunks {
		evs, err := p.Feed(c)
		all = append(all, evs...)
		if err != nil {
			return all, err, p
		}
	}
	evs, err := p.Finish()
	all = append(all, evs...)
	return all, err, p
}

func parseString(s string) ([]Event, error, *Parser) {
	return parseChunks(limits.Limits{}, []byte(s))
}

func TestParserEvents(t *testing.T) {
	msg := func(i int, data string) Event { return Event{Index: i, Type: DefaultEventType, Data: data} }
	tests := []struct {
		name string
		in   string
		want []Event
	}{
		{"single", "data: hello\n\n", []Event{msg(0, "hello")}},
		{"multi-line data", "data: a\ndata: b\ndata: c\n\n", []Event{msg(0, "a\nb\nc")}},
		{"event type", "event: update\ndata: x\n\n", []Event{{Index: 0, Type: "update", Data: "x"}}},
		{"type resets after dispatch", "event: update\ndata: x\n\ndata: y\n\n",
			[]Event{{Index: 0, Type: "update", Data: "x"}, msg(1, "y")}},
		{"type resets without dispatch", "event: lone\n\ndata: y\n\n", []Event{msg(0, "y")}},
		{"comment ignored", ": keep-alive\ndata: x\n: another\n\n", []Event{msg(0, "x")}},
		{"no space after colon", "data:x\n\n", []Event{msg(0, "x")}},
		{"only one space removed", "data:  x\n\n", []Event{msg(0, " x")}},
		{"colon in value", "data: a:b: c\n\n", []Event{msg(0, "a:b: c")}},
		{"field without colon", "data\n\n", []Event{msg(0, "")}},
		{"two empty data lines", "data\ndata\n\n", []Event{msg(0, "\n")}},
		{"empty data value dispatches", "data:\n\n", []Event{msg(0, "")}},
		{"empty buffer not dispatched", "event: x\nid: 1\n\n", nil},
		{"blank lines only", "\n\n\r\n\r", nil},
		{"bom stripped", "\xEF\xBB\xBFdata: x\n\n", []Event{msg(0, "x")}},
		{"second bom makes unknown field", "\xEF\xBB\xBF\xEF\xBB\xBFdata: x\n\n", nil},
		{"cr endings", "data: a\rdata: b\r\r", []Event{msg(0, "a\nb")}},
		{"crlf endings", "data: a\r\ndata: b\r\n\r\n", []Event{msg(0, "a\nb")}},
		{"mixed endings", "data: a\r\n\rdata: b\n\r\ndata: c\r\n", []Event{msg(0, "a"), msg(1, "b")}},
		{"id persists", "id: 7\ndata: x\n\ndata: y\n\n",
			[]Event{{Index: 0, Type: "message", Data: "x", ID: "7"}, {Index: 1, Type: "message", Data: "y", ID: "7"}}},
		{"empty id resets", "id: 7\ndata: x\n\nid\ndata: y\n\n",
			[]Event{{Index: 0, Type: "message", Data: "x", ID: "7"}, msg(1, "y")}},
		{"id with nul ignored", "id: 1\ndata: a\n\nid: b\x00c\ndata: b\n\n",
			[]Event{{Index: 0, Type: "message", Data: "a", ID: "1"}, {Index: 1, Type: "message", Data: "b", ID: "1"}}},
		{"retry on event", "retry: 3000\ndata: x\n\ndata: y\n\n",
			[]Event{{Index: 0, Type: "message", Data: "x", Retry: intPtr(3000)}, msg(1, "y")}},
		{"bad retry ignored", "retry: 3s\ndata: x\n\n", []Event{msg(0, "x")}},
		{"unknown field ignored", "foo: bar\ndata: x\n\n", []Event{msg(0, "x")}},
		{"field names are case-sensitive", "Data: x\n\n", nil},
		{"incomplete event dropped", "data: a\n\ndata: b\n", []Event{msg(0, "a")}},
		{"unterminated line dropped", "data: a\n\ndata: b", []Event{msg(0, "a")}},
		{"unicode", "data: héllo 😀\n\n", []Event{msg(0, "héllo 😀")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err, _ := parseString(tt.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("events = %+v\nwant     %+v", got, tt.want)
			}
		})
	}
}

func TestParserCounters(t *testing.T) {
	_, err, p := parseString("\xEF\xBB\xBF: c1\nfoo\nid: a\x00\nretry: x\nretry: 25\n: c2\ndata: z\n\nevent: e\ndata: tail")
	if err != nil {
		t.Fatal(err)
	}
	if p.Comments() != 2 || p.UnknownFields() != 1 || p.Events() != 1 {
		t.Fatalf("comments=%d unknown=%d events=%d", p.Comments(), p.UnknownFields(), p.Events())
	}
	if r := p.Retry(); r == nil || *r != 25 {
		t.Fatalf("retry = %v", r)
	}
	codes := []string{}
	for _, n := range p.Notices() {
		codes = append(codes, n.Code)
	}
	if !reflect.DeepEqual(codes, []string{CodeIDContainsNUL, CodeInvalidRetry}) {
		t.Fatalf("notice codes = %v", codes)
	}
	if n := p.Notices()[0]; n.Line != 3 || n.Event != 0 || n.Value != "a\x00" {
		t.Fatalf("notice = %+v", n)
	}
	pending, n := p.Pending()
	if !pending || n != len("e")+len("data: tail") {
		t.Fatalf("pending = %v, %d", pending, n)
	}
	if p.Bytes() == 0 {
		t.Fatal("bytes not counted")
	}
}

func TestParserRetryRules(t *testing.T) {
	for in, want := range map[string]bool{
		"0": true, "123456789": true, "1234567890": false, "": false, "-1": false, " 1": false, "1.5": false, "١": false,
	} {
		_, ok := parseRetry([]byte(in))
		if ok != want {
			t.Errorf("parseRetry(%q) ok = %v, want %v", in, ok, want)
		}
	}
}

func TestParserNoticeBound(t *testing.T) {
	var in []byte
	for i := 0; i < maxNotices+5; i++ {
		in = append(in, "retry: x\n"...)
	}
	_, err, p := parseChunks(limits.Limits{}, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Notices()) != maxNotices || p.NoticesDropped() != 5 {
		t.Fatalf("notices = %d dropped = %d", len(p.Notices()), p.NoticesDropped())
	}
}

func TestParserLimits(t *testing.T) {
	tests := []struct {
		name       string
		lim        limits.Limits
		chunks     []string
		wantCode   string
		wantEvents int
	}{
		{"line", limits.Limits{MaxLineBytes: 8}, []string{"data: 12\n\ndata: 123\n\n"}, CodeLineTooLong, 1},
		{"line without terminator", limits.Limits{MaxLineBytes: 8}, []string{"data:", " 1234"}, CodeLineTooLong, 0},
		{"event", limits.Limits{MaxEventBytes: 20}, []string{"data: 0123456789\ndata: 0123456789\n\n"}, CodeEventTooLarge, 0},
		{"event counts comments", limits.Limits{MaxEventBytes: 10}, []string{": 0123456789\n"}, CodeEventTooLarge, 0},
		{"stream bytes", limits.Limits{MaxStreamBytes: 12}, []string{"data: a\n\n", "data: b\n\n"}, CodeStreamTooLarge, 1},
		{"stream events", limits.Limits{MaxStreamEvents: 2}, []string{"data: a\n\ndata: b\n\ndata: c\n\n"}, CodeTooManyEvents, 2},
		{"invalid utf8", limits.Limits{}, []string{"data: ok\n\ndata: \xC3\x28\n\n"}, CodeInvalidUTF8, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := make([][]byte, len(tt.chunks))
			for i, c := range tt.chunks {
				chunks[i] = []byte(c)
			}
			events, err, p := parseChunks(tt.lim, chunks...)
			var pe *ParseError
			if !errors.As(err, &pe) || pe.Code != tt.wantCode {
				t.Fatalf("err = %v, want code %s", err, tt.wantCode)
			}
			want := TermLimitExceeded
			if tt.wantCode == CodeInvalidUTF8 {
				want = TermInvalidData
			}
			if pe.Termination != want {
				t.Fatalf("termination = %s, want %s", pe.Termination, want)
			}
			if len(events) != tt.wantEvents {
				t.Fatalf("events = %d, want %d", len(events), tt.wantEvents)
			}
			// The parser stays stopped.
			if evs, again := p.Feed([]byte("data: more\n\n")); again != err || len(evs) != 0 {
				t.Fatalf("Feed after stop = %v, %v", evs, again)
			}
			if _, again := p.Finish(); again != err {
				t.Fatalf("Finish after stop = %v", again)
			}
		})
	}
}

func TestParserStreamBytesExact(t *testing.T) {
	in := "data: a\n\n"
	events, err, p := parseChunks(limits.Limits{MaxStreamBytes: int64(len(in))}, []byte(in))
	if err != nil || len(events) != 1 || p.Bytes() != int64(len(in)) {
		t.Fatalf("events=%d err=%v bytes=%d", len(events), err, p.Bytes())
	}
}
