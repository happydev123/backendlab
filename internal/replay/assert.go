package replay

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/suite"
)

// Assertion failure codes.
const (
	CodeStatusUnexpected    = "replay.status_unexpected"
	CodeHeaderMismatch      = "replay.header_mismatch"
	CodeMaxDurationExceeded = "replay.max_duration_exceeded"
	CodeBodyNotJSON         = "replay.body_not_json"
	CodeJSONNotEqual        = "replay.json_not_equal"
	CodeJSONExists          = "replay.json_exists"
	CodeJSONType            = "replay.json_type"
	CodeJSONNoMatch         = "replay.json_no_match"
	CodeJSONPointer         = "replay.json_pointer"
	CodeCaptureFailed       = "replay.capture_failed"
)

const absentValue = "(absent)"

// asserter evaluates one case's expectations against one target's
// response and produces sanitized findings.
type asserter struct {
	caseID string
	target string
	red    *redact.Redactor
	max    int
}

// failure builds an assertion failure finding.
func (a asserter) failure(code, location, msg, expected, observed string) finding.Finding {
	f := newFinding(finding.KindAssertionFailure, finding.SeverityError, code, a.caseID, a.target, location, msg)
	f.Expected = expected
	f.Observed = observed
	return f
}

// value previews a JSON value at p, hiding redacted paths.
func (a asserter) value(p jsonptr.Pointer, v any) string {
	if a.red.PathRedacted(p) {
		return redact.Marker
	}
	return jsonutil.Preview(a.red.JSON(v), a.max)
}

// text bounds and scrubs free text.
func (a asserter) text(s string) string { return jsonutil.Truncate(a.red.String(s), a.max) }

// checkStatus fails when the status code is not in the expected list. An
// empty list accepts every status.
func (a asserter) checkStatus(exp suite.StatusList, code int) []finding.Finding {
	if exp.Contains(code) {
		return nil
	}
	want := make([]string, len(exp))
	for i, c := range exp {
		want[i] = strconv.Itoa(c)
	}
	return []finding.Finding{a.failure(CodeStatusUnexpected, "status",
		fmt.Sprintf("status %d is not in the expected list", code),
		strings.Join(want, " or "), strconv.Itoa(code))}
}

// checkHeaders compares expected header values exactly. Names are matched
// case-insensitively; multiple received values are joined with ", ".
func (a asserter) checkHeaders(exp map[string]string, h http.Header) []finding.Finding {
	expected := make(map[string]string, len(exp))
	names := make([]string, 0, len(exp))
	for name, v := range exp {
		canon := http.CanonicalHeaderKey(name)
		if _, dup := expected[canon]; !dup {
			names = append(names, canon)
		}
		expected[canon] = v
	}
	sort.Strings(names)
	var out []finding.Finding
	for _, canon := range names {
		want := expected[canon]
		vals := h.Values(canon)
		if len(vals) == 0 {
			out = append(out, a.failure(CodeHeaderMismatch, "header:"+canon,
				fmt.Sprintf("header %s is missing", canon), a.header(canon, want), absentValue))
			continue
		}
		got := strings.Join(vals, ", ")
		if got != want {
			out = append(out, a.failure(CodeHeaderMismatch, "header:"+canon,
				fmt.Sprintf("header %s has an unexpected value", canon), a.header(canon, want), a.header(canon, got)))
		}
	}
	return out
}

func (a asserter) header(name, value string) string {
	return jsonutil.Truncate(a.red.Header(name, value), a.max)
}

// checkDuration fails when the request took longer than an explicit
// max_duration. This is the only timing threshold replay applies.
func (a asserter) checkDuration(max suite.Duration, total time.Duration) []finding.Finding {
	if max <= 0 || total <= max.Std() {
		return nil
	}
	return []finding.Finding{a.failure(CodeMaxDurationExceeded, "duration",
		fmt.Sprintf("request took %s, more than max_duration %s (single observation)", roundMS(total), max.Std()),
		"<= "+max.Std().String(), roundMS(total).String())}
}

func roundMS(d time.Duration) time.Duration { return d.Round(time.Millisecond) }
