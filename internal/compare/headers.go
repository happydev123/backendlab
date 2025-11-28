package compare

import (
	"fmt"
	"net/http"
	"strings"
)

// compareHeaders compares the headers named in o.Headers. Multiple values
// of one header are joined with ", " in received order. Sensitive headers
// (per the redactor) are compared by their real values but shown as the
// redaction marker.
func (w *walker) compareHeaders(b, c http.Header) {
	for _, name := range w.o.Headers {
		bv, bok := headerValue(b, name)
		cv, cok := headerValue(c, name)
		loc := headerPrefix + name
		switch {
		case !bok && !cok:
			continue
		case bok && !cok:
			w.out.add(Difference{
				Code: CodeHeaderMissing, Location: loc,
				Message:  fmt.Sprintf("header %s is missing in candidate", name),
				Baseline: w.pv.header(name, bv, true), Candidate: absent,
			})
		case !bok && cok:
			w.out.add(Difference{
				Code: CodeHeaderAdded, Location: loc,
				Message:  fmt.Sprintf("header %s was added in candidate", name),
				Baseline: absent, Candidate: w.pv.header(name, cv, true),
			})
		case bv != cv:
			w.out.add(Difference{
				Code: CodeHeaderChanged, Location: loc,
				Message:  fmt.Sprintf("header %s changed", name),
				Baseline: w.pv.header(name, bv, true), Candidate: w.pv.header(name, cv, true),
			})
		}
	}
}

// headerValue returns the joined values of name and whether it is present.
func headerValue(h http.Header, name string) (string, bool) {
	vals := h.Values(name)
	if len(vals) == 0 {
		return "", false
	}
	return strings.Join(vals, ", "), true
}

// compareStatus compares status codes unless disabled.
func (w *walker) compareStatus(b, c int) {
	if w.o.SkipStatus || b == c {
		return
	}
	w.out.add(Difference{
		Code:      CodeStatusChanged,
		Location:  LocationStatus,
		Message:   fmt.Sprintf("status code changed from %d to %d", b, c),
		Baseline:  fmt.Sprint(b),
		Candidate: fmt.Sprint(c),
	})
}
