package compare

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/jsonutil"
)

// maxDepth bounds recursion; it equals the longest pointer jsonptr can
// parse so every reported location can be parsed back.
const maxDepth = jsonptr.MaxSegments

// JSON compares two decoded JSON documents (as produced by
// jsonutil.Decode) under o. Object key order is ignored. Absent members
// and null members are different. Excluded pointers are skipped before
// anything is compared or reported.
func JSON(baseline, candidate any, o Options) Result {
	o = o.withDefaults()
	out := newDiffList(o.MaxDifferences)
	w := newWalker(&o, out)
	w.compare(jsonptr.Pointer{}, baseline, candidate, 0)
	return resultOf(out, w.within)
}

// Equal reports whether a and b are equal with JSON semantics: object key
// order is ignored, array order matters, numbers are equal when their
// decimal values are equal (1.0 equals 1.00), and absent differs from null.
func Equal(a, b any) bool {
	return JSON(a, b, Options{MaxDifferences: 1}).Equal()
}

// walker holds the state of one structural comparison.
type walker struct {
	o      *Options
	pv     previewer
	out    *diffList
	within int
}

func newWalker(o *Options, out *diffList) *walker {
	return &walker{o: o, pv: previewer{r: o.Redactor, max: o.MaxValuePreview}, out: out}
}

// location formats p for reports; the document root is "body".
func location(p jsonptr.Pointer) string {
	if p.IsRoot() {
		return LocationBody
	}
	return p.String()
}

// diff records a difference between two present values at p.
func (w *walker) diff(code string, p jsonptr.Pointer, msg string, b, c any) {
	w.out.add(Difference{
		Code:      code,
		Location:  location(p),
		Message:   msg,
		Baseline:  w.pv.value(p, b),
		Candidate: w.pv.value(p, c),
	})
}

func (w *walker) compare(p jsonptr.Pointer, b, c any, depth int) {
	if w.o.ignored(p) {
		return
	}
	if depth > maxDepth {
		w.out.add(Difference{
			Code:         CodeTooDeep,
			Location:     location(p),
			Message:      fmt.Sprintf("values nested deeper than %d levels were not compared", maxDepth),
			Inconclusive: true,
		})
		return
	}
	bt, ct := jsonutil.TypeName(b), jsonutil.TypeName(c)
	if bt != ct {
		w.diff(CodeTypeChanged, p, fmt.Sprintf("type changed from %s to %s", bt, ct), b, c)
		return
	}
	switch bv := b.(type) {
	case map[string]any:
		w.compareObjects(p, bv, c.(map[string]any), depth)
	case []any:
		w.compareArrays(p, bv, c.([]any), depth)
	case string:
		if bv != c.(string) {
			w.diff(CodeValueChanged, p, "string value changed", b, c)
		}
	case bool:
		if bv != c.(bool) {
			w.diff(CodeValueChanged, p, "boolean value changed", b, c)
		}
	case nil:
	default:
		if bt == "number" {
			w.compareNumber(p, b, c)
		} else if !reflect.DeepEqual(b, c) {
			w.diff(CodeValueChanged, p, "value changed", b, c)
		}
	}
}

func (w *walker) compareObjects(p jsonptr.Pointer, b, c map[string]any, depth int) {
	keys := make([]string, 0, len(b)+len(c))
	for k := range b {
		keys = append(keys, k)
	}
	for k := range c {
		if _, ok := b[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		cp := p.Append(k)
		if w.o.ignored(cp) {
			continue
		}
		bv, bok := b[k]
		cv, cok := c[k]
		switch {
		case bok && cok:
			w.compare(cp, bv, cv, depth+1)
		case bok && bv == nil:
			w.oneSided(CodeNullVsAbsent, cp, "member is null in baseline and absent in candidate", bv, true)
		case bok:
			w.oneSided(CodeFieldMissing, cp, "member is missing in candidate", bv, true)
		case cv == nil:
			w.oneSided(CodeNullVsAbsent, cp, "member is absent in baseline and null in candidate", cv, false)
		default:
			w.oneSided(CodeFieldAdded, cp, "member was added in candidate", cv, false)
		}
	}
}

// oneSided records a value that exists only in the baseline (inBaseline)
// or only in the candidate.
func (w *walker) oneSided(code string, p jsonptr.Pointer, msg string, v any, inBaseline bool) {
	d := Difference{Code: code, Location: location(p), Message: msg, Baseline: absent, Candidate: absent}
	if inBaseline {
		d.Baseline = w.pv.value(p, v)
	} else {
		d.Candidate = w.pv.value(p, v)
	}
	w.out.add(d)
}

func (w *walker) compareNumber(p jsonptr.Pointer, b, c any) {
	tol := w.o.toleranceAt(p)
	switch compareNumbers(b, c, tol) {
	case numbersEqual:
	case numbersWithinTolerance:
		w.within++
	case numbersUnparsable:
		w.out.add(Difference{
			Code:         CodeNumberUnparsable,
			Location:     location(p),
			Message:      "numbers differ in text and exceed the decimal comparison bounds",
			Baseline:     w.pv.value(p, b),
			Candidate:    w.pv.value(p, c),
			Inconclusive: true,
		})
	default:
		msg := fmt.Sprintf("number changed (tolerance: %s)", tol)
		// The size of a change would leak information about a redacted
		// value, so it is only shown for values that are not redacted.
		if !w.o.Redactor.PathRedacted(p) {
			bd, _ := toDecimal(b)
			cd, _ := toDecimal(c)
			msg = fmt.Sprintf("number changed by %s (tolerance: %s)", cd.Sub(bd).String(), tol)
		}
		w.diff(CodeValueChanged, p, msg, b, c)
	}
}
