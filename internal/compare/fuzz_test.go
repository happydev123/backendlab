package compare

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"

	"github.com/happydev123/backendlab/internal/jsonutil"
)

var swapped = map[string]string{
	CodeFieldMissing:   CodeFieldAdded,
	CodeFieldAdded:     CodeFieldMissing,
	CodeElementMissing: CodeElementAdded,
	CodeElementAdded:   CodeElementMissing,
}

// checkSymmetric verifies that comparing in the other direction reports
// the same locations with mirrored codes and swapped previews.
func checkSymmetric(t *testing.T, a, b any) {
	t.Helper()
	ab := JSON(a, b, Options{})
	ba := JSON(b, a, Options{})
	if len(ab.Differences) != len(ba.Differences) || ab.Dropped != ba.Dropped {
		t.Fatalf("asymmetric counts: %d/%d vs %d/%d\n%s\n%s", len(ab.Differences), ab.Dropped,
			len(ba.Differences), ba.Dropped, summary(ab), summary(ba))
	}
	for i := range ab.Differences {
		x, y := ab.Differences[i], ba.Differences[i]
		want := x.Code
		if s, ok := swapped[want]; ok {
			want = s
		}
		if y.Code != want || x.Location != y.Location || x.Baseline != y.Candidate || x.Candidate != y.Baseline {
			t.Fatalf("difference %d not mirrored:\n %+v\n %+v", i, x, y)
		}
	}
	if self := JSON(a, a, Options{}); !self.Equal() {
		for _, d := range self.Differences {
			if d.Code != CodeTooDeep {
				t.Fatalf("value not equal to itself: %s", summary(self))
			}
		}
	}
}

func FuzzJSONCompare(f *testing.F) {
	seeds := [][2]string{
		{`{"a":1}`, `{"a":1.0}`},
		{`{"a":null}`, `{}`},
		{`{"a":[1,2,3]}`, `{"a":[1,2]}`},
		{`[{"id":1},{"id":2}]`, `[{"id":2},{"id":1}]`},
		{`{"x":{"y":{"z":"s"}}}`, `{"x":{"y":[]}}`},
		{`1e400`, `1E400`},
		{`"text"`, `true`},
		{`{"a~b":{"c/d":0.1}}`, `{"a~b":{"c/d":0.10}}`},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, x, y string) {
		a, errA := jsonutil.Decode([]byte(x))
		b, errB := jsonutil.Decode([]byte(y))
		// Bodies must never panic whatever the input.
		Bodies([]byte(x), []byte(y), Options{})
		if errA != nil || errB != nil {
			return
		}
		checkSymmetric(t, a, b)
	})
}

// randomValue builds a small random JSON value.
func randomValue(r *rand.Rand, depth int) any {
	n := r.Intn(8)
	if depth > 4 {
		n = r.Intn(4)
	}
	switch n {
	case 0:
		return nil
	case 1:
		return r.Intn(2) == 0
	case 2:
		return json.Number(fmt.Sprintf("%d.%d", r.Intn(5), r.Intn(3)))
	case 3:
		return fmt.Sprintf("s%d", r.Intn(4))
	case 4, 5:
		arr := make([]any, r.Intn(4))
		for i := range arr {
			arr[i] = randomValue(r, depth+1)
		}
		return arr
	default:
		obj := map[string]any{}
		for i := r.Intn(4); i > 0; i-- {
			obj[fmt.Sprintf("k%d", r.Intn(5))] = randomValue(r, depth+1)
		}
		return obj
	}
}

// mutate returns a copy of v with random local changes.
func mutate(r *rand.Rand, v any) any {
	if r.Intn(6) == 0 {
		return randomValue(r, 3)
	}
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, child := range t {
			if r.Intn(8) == 0 {
				continue
			}
			out[k] = mutate(r, child)
		}
		if r.Intn(5) == 0 {
			out[fmt.Sprintf("k%d", r.Intn(7))] = randomValue(r, 3)
		}
		return out
	case []any:
		out := make([]any, 0, len(t)+1)
		for _, child := range t {
			if r.Intn(8) == 0 {
				continue
			}
			out = append(out, mutate(r, child))
		}
		return out
	}
	return v
}

func TestJSONRandomSymmetric(t *testing.T) {
	r := rand.New(rand.NewSource(20240601))
	for i := 0; i < 2000; i++ {
		a := randomValue(r, 0)
		b := mutate(r, a)
		checkSymmetric(t, a, b)
	}
}
