package stream

import (
	"reflect"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonutil"
)

// feedAdapter decodes each payload like the checker does and passes it
// to a chat-delta adapter.
func feedAdapter(maxText int, payloads ...string) *chatDelta {
	c := newChatDelta(maxText)
	for i, p := range payloads {
		v, err := jsonutil.Decode([]byte(p))
		c.observe(i, v, err == nil)
	}
	return c
}

func TestChatDeltaShapes(t *testing.T) {
	tests := []struct {
		name     string
		payloads []string
		text     string
		deltas   int
		life     int
		unrec    int
		reasons  []string
	}{
		{
			name: "choices",
			payloads: []string{
				`{"choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
				`{"choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":null}]}`,
				`{"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}`,
				`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			},
			text: "Hello", deltas: 4, reasons: []string{"stop"},
		},
		{
			name:     "finish reason without delta",
			payloads: []string{`{"choices":[{"finish_reason":"length"}]}`},
			life:     1, reasons: []string{"length"},
		},
		{
			name:     "plain delta string",
			payloads: []string{`{"delta":"a"}`, `{"type":"output_text.delta","delta":"b"}`},
			text:     "ab", deltas: 2,
		},
		{
			name: "typed delta objects",
			payloads: []string{
				`{"type":"message_start","message":{}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" there"}}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
				`{"type":"message_stop"}`,
			},
			text: "Hi there", deltas: 3, life: 2, reasons: []string{"end_turn"},
		},
		{
			name:     "unknown shapes",
			payloads: []string{`not json`, `[1,2]`, `{"foo":1}`, `{"choices":[]}`, `{"choices":["x"]}`, `{"type":"x.delta","delta":{"other":1}}`},
			unrec:    6,
		},
		{
			name:     "duplicate reasons kept once",
			payloads: []string{`{"choices":[{"delta":{},"finish_reason":"stop"}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`},
			deltas:   2, reasons: []string{"stop"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := feedAdapter(1000, tt.payloads...).result()
			if r.Text != tt.text || r.Deltas != tt.deltas || r.Lifecycle != tt.life || r.Unrecognized != tt.unrec {
				t.Fatalf("result = %+v", r)
			}
			if len(tt.reasons) == 0 {
				tt.reasons = []string{}
			}
			if !reflect.DeepEqual(r.FinishReasons, tt.reasons) && !(len(r.FinishReasons) == 0 && len(tt.reasons) == 0) {
				t.Fatalf("reasons = %v, want %v", r.FinishReasons, tt.reasons)
			}
		})
	}
}

func TestChatDeltaTruncatesOnRuneBoundary(t *testing.T) {
	r := feedAdapter(5, `{"delta":"abcd"}`, `{"delta":"éé"}`).result()
	if r.Text != "abcd" || !r.Truncated || r.TextBytes != 8 {
		t.Fatalf("result = %+v", r)
	}
}

func TestChatDeltaFindingsNeverFail(t *testing.T) {
	c := feedAdapter(3, `{"foo":1}`, `{"delta":"long text"}`)
	s := newSink("", nil, 80, 10)
	c.report(s, 2)
	codes := map[string]bool{}
	for _, f := range s.items() {
		if f.Failing() {
			t.Fatalf("adapter finding fails the run: %+v", f)
		}
		codes[f.Code] = true
	}
	if !codes[CodeAdapterUnrecognized] || !codes[CodeAdapterTruncated] || codes[CodeAdapterNoDeltas] {
		t.Fatalf("codes = %v", codes)
	}
	s = newSink("", nil, 80, 10)
	feedAdapter(10, `{"foo":1}`).report(s, 1)
	items := s.items()
	if len(items) != 2 || items[0].Kind != finding.KindInconclusive || items[0].Location != "" {
		t.Fatalf("items = %+v", items)
	}
}

func TestKnownAdapter(t *testing.T) {
	if !knownAdapter("chat-delta") || knownAdapter("openai") {
		t.Fatal("adapter registry")
	}
}
