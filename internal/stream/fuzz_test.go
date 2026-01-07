package stream

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/happydev123/backendlab/internal/limits"
)

// fuzzLimits are small so the fuzzer reaches every bound quickly.
var fuzzLimits = limits.Limits{MaxLineBytes: 64, MaxEventBytes: 128, MaxStreamBytes: 4096, MaxStreamEvents: 16}

// randomChunks splits data at positions chosen by a seeded generator.
func randomChunks(data []byte, seed int64) [][]byte {
	rng := rand.New(rand.NewSource(seed))
	var out [][]byte
	for len(data) > 0 {
		n := rng.Intn(len(data) + 1)
		out = append(out, data[:n])
		data = data[n:]
	}
	return out
}

// FuzzParser checks that parsing never panics and that the result does
// not depend on how the input is chunked.
func FuzzParser(f *testing.F) {
	seeds := []string{
		sampleStream,
		"data: x\n\n",
		"data: a\r\ndata: b\r\n\r\n",
		"\xEF\xBB\xBFevent: e\rdata\r\r",
		": comment\nid: 1\x00\nretry: 12a\n\n",
		"data: \xC3\n\n",
		"data: \xF0\x9F\x98\x80\n\n",
		"data: 0123456789012345678901234567890123456789012345678901234567890123456789\n\n",
		"data: [DONE]\n\ndata: late\n\n",
		"\r\r\r\n\n\r",
		"",
	}
	for i, s := range seeds {
		f.Add([]byte(s), int64(i))
	}
	f.Fuzz(func(t *testing.T, data []byte, seed int64) {
		whole := summarize(fuzzLimits, [][]byte{data})
		if got := summarize(fuzzLimits, bytewise(data)); !reflect.DeepEqual(got, whole) {
			t.Fatalf("byte-by-byte differs:\n got %+v\nwant %+v", got, whole)
		}
		if got := summarize(fuzzLimits, randomChunks(data, seed)); !reflect.DeepEqual(got, whole) {
			t.Fatalf("random chunks (seed %d) differ:\n got %+v\nwant %+v", seed, got, whole)
		}
		for i, ev := range whole.Events {
			if ev.Index != i || ev.Type == "" {
				t.Fatalf("event %d malformed: %+v", i, ev)
			}
		}
		if len(whole.Events) > fuzzLimits.MaxStreamEvents {
			t.Fatalf("%d events exceed the bound", len(whole.Events))
		}
	})
}
