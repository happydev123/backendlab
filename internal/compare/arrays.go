package compare

import (
	"fmt"
	"strconv"

	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/redact"
)

// compareArrays compares arrays by position, or by key when the array's
// pointer matches an unordered rule.
func (w *walker) compareArrays(p jsonptr.Pointer, b, c []any, depth int) {
	if key, ok := w.o.unorderedKey(p); ok {
		w.compareKeyed(p, b, c, key, depth)
		return
	}
	w.compareOrdered(p, b, c, depth)
}

// compareOrdered reports a length change and compares the common prefix
// element by element. Extra elements are covered by the length difference
// and not listed one by one.
func (w *walker) compareOrdered(p jsonptr.Pointer, b, c []any, depth int) {
	if len(b) != len(c) {
		w.out.add(Difference{
			Code:      CodeArrayLengthChanged,
			Location:  location(p),
			Message:   fmt.Sprintf("array length changed from %d to %d", len(b), len(c)),
			Baseline:  w.lengthPreview(p, len(b)),
			Candidate: w.lengthPreview(p, len(c)),
		})
	}
	n := min(len(b), len(c))
	for i := 0; i < n; i++ {
		w.compare(p.AppendIndex(i), b[i], c[i], depth+1)
	}
}

func (w *walker) lengthPreview(p jsonptr.Pointer, n int) string {
	if w.o.Redactor.PathRedacted(p) {
		return redact.Marker
	}
	return strconv.Itoa(n) + " elements"
}

// compareKeyed matches elements by key. Matched elements are compared
// recursively and reported at their baseline index; unmatched elements are
// reported at their index on the side where they exist.
func (w *walker) compareKeyed(p jsonptr.Pointer, b, c []any, key string, depth int) {
	m, problem := matchByKey(b, c, key)
	if problem != "" {
		// Arrays that are equal position by position are equal in any
		// order, so an unusable key does not matter for them.
		probe := newWalker(w.o, newDiffList(1))
		probe.compareOrdered(p, b, c, depth)
		if probe.out.empty() {
			w.within += probe.within
			return
		}
		w.out.add(Difference{
			Code:     CodeUnorderedUnmatchable,
			Location: location(p),
			Message: fmt.Sprintf("array is configured as unordered by %q but cannot be matched (%s); its elements were not compared",
				key, problem),
			Baseline:     w.lengthPreview(p, len(b)),
			Candidate:    w.lengthPreview(p, len(c)),
			Inconclusive: true,
		})
		return
	}
	for _, i := range m.missing {
		ep := p.AppendIndex(i)
		if w.o.ignored(ep) {
			continue
		}
		w.out.add(Difference{
			Code:      CodeElementMissing,
			Location:  location(ep),
			Message:   fmt.Sprintf("element with %s %s is missing in candidate", key, w.keyPreview(ep, b[i], key)),
			Baseline:  w.pv.value(ep, b[i]),
			Candidate: absent,
		})
	}
	for _, pair := range m.pairs {
		w.compare(p.AppendIndex(pair.b), b[pair.b], c[pair.c], depth+1)
	}
	for _, j := range m.added {
		ep := p.AppendIndex(j)
		if w.o.ignored(ep) {
			continue
		}
		w.out.add(Difference{
			Code:      CodeElementAdded,
			Location:  location(ep),
			Message:   fmt.Sprintf("element with %s %s was added in candidate", key, w.keyPreview(ep, c[j], key)),
			Baseline:  absent,
			Candidate: w.pv.value(ep, c[j]),
		})
	}
}

// keyPreview renders an element's key value for messages.
func (w *walker) keyPreview(ep jsonptr.Pointer, el any, key string) string {
	return w.pv.value(ep.Append(key), el.(map[string]any)[key])
}
