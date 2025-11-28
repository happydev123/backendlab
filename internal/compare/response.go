package compare

import "net/http"

// Response is what one target returned for a case.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
	// SkipBody excludes the body from comparison, for responses whose body
	// was consumed elsewhere (such as event streams).
	SkipBody bool
}

// Responses compares a baseline and a candidate response: the status code
// (unless o.SkipStatus), the headers listed in o.Headers, and the body.
// All differences share the o.MaxDifferences bound. The body is skipped
// when either response sets SkipBody.
func Responses(baseline, candidate Response, o Options) Result {
	o = o.withDefaults()
	out := newDiffList(o.MaxDifferences)
	w := newWalker(&o, out)
	w.compareStatus(baseline.Status, candidate.Status)
	w.compareHeaders(baseline.Header, candidate.Header)
	if !baseline.SkipBody && !candidate.SkipBody {
		w.compareBodies(baseline.Body, candidate.Body)
	}
	return resultOf(out, w.within)
}
