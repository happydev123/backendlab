package plans

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/happydev123/backendlab/internal/limits"
)

// fuzzSeeds returns the fixture plans plus small hand-picked edge cases.
func fuzzSeeds(t testing.TB) [][]byte {
	seeds := [][]byte{
		[]byte(`[]`),
		[]byte(`{}`),
		[]byte(`{"Plan": {"Node Type": "Result"}}`),
		[]byte(`[{"Plan": {"Node Type": "Sort", "Plans": [{"Node Type": "Seq Scan", "Plan Rows": 1e40}]}}]`),
		[]byte(`[{"Plan": {"Node Type": "Hash", "Hash Batches": 0.5, "Actual Loops": 0}}]`),
		[]byte(`{"Plan": {"Node Type": "Seq Scan", "Plans": [null]}}`),
		[]byte(`"\u0000"`),
	}
	files, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		seeds = append(seeds, data)
	}
	return seeds
}

// exercise parses data and, when it parses, runs the read-only consumers
// so that they are covered for arbitrary accepted plans too.
func exercise(t testing.TB, data []byte) {
	p, err := Parse(data, "fuzz.json", limits.Default())
	if err != nil {
		if p != nil {
			t.Fatal("Parse returned a plan together with an error")
		}
		return
	}
	if p.Root == nil {
		t.Fatal("accepted plan without root")
	}
	_ = Summarize(p)
	c := Compare(p, p, Options{})
	_ = c.Report(nil)
}

func FuzzParse(f *testing.F) {
	for _, s := range fuzzSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		exercise(t, data)
	})
}

// TestParseMutations applies seeded random byte mutations to the fixtures
// so the no-panic property is checked on every plain `go test` run, not
// only under -fuzz.
func TestParseMutations(t *testing.T) {
	rng := rand.New(rand.NewSource(20261024))
	seeds := fuzzSeeds(t)
	alphabet := []byte(`{}[]":,0123456789.-e truefalsnul\`)
	for i := 0; i < 3000; i++ {
		src := seeds[rng.Intn(len(seeds))]
		data := append([]byte(nil), src...)
		for m := rng.Intn(4) + 1; m > 0 && len(data) > 0; m-- {
			pos := rng.Intn(len(data))
			switch rng.Intn(3) {
			case 0:
				data[pos] = alphabet[rng.Intn(len(alphabet))]
			case 1:
				data = append(data[:pos], data[pos+1:]...)
			default:
				data = append(data[:pos], append([]byte{alphabet[rng.Intn(len(alphabet))]}, data[pos:]...)...)
			}
		}
		exercise(t, data)
	}
}
