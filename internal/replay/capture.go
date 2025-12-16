package replay

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/suite"
)

// scope holds the variables captured on one target during one run. Each
// target has its own scope, so in compare mode a value captured from the
// baseline is only ever sent to the baseline.
type scope struct {
	vars map[string]string
	// failed maps a variable to the case whose capture of it failed.
	failed map[string]string
}

func newScope() *scope {
	return &scope{vars: map[string]string{}, failed: map[string]string{}}
}

// missing returns the referenced variables that have no value, sorted.
func (s *scope) missing(refs map[string][]string) []string {
	var out []string
	for name := range refs {
		if _, ok := s.vars[name]; !ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// explain describes why the named variables are unavailable.
func (s *scope) explain(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		if by, ok := s.failed[n]; ok {
			parts[i] = fmt.Sprintf("${%s} (capture in case %q failed)", n, by)
		} else {
			parts[i] = fmt.Sprintf("${%s} (not captured)", n)
		}
	}
	return "case not executed: required variables are unavailable: " + strings.Join(parts, ", ")
}

// markFailed records every capture of c as failed, for cases that
// produced no usable response.
func (s *scope) markFailed(c *suite.Case) {
	for _, cp := range c.Capture {
		s.failed[cp.Name] = c.ID
	}
}

// capture stores the values named by c.Capture from a decoded body. Each
// value must exist and be a string, number or boolean; anything else is a
// capture failure. It returns failure findings and the captured values
// that sit at redacted paths, which the caller registers as secrets so
// they are scrubbed wherever they are later echoed.
func (a asserter) capture(c *suite.Case, body any, isJSON bool, s *scope) ([]finding.Finding, []string) {
	var out []finding.Finding
	var sensitive []string
	for _, cp := range c.Capture {
		loc := pointerLocation(cp.Pointer)
		fail := func(msg string) {
			s.failed[cp.Name] = c.ID
			out = append(out, a.failure(CodeCaptureFailed, loc, fmt.Sprintf("capture %s: %s", cp.Name, msg), "string, number or boolean", ""))
		}
		if !isJSON {
			fail("response body is not JSON")
			continue
		}
		p, err := jsonptr.Parse(cp.Pointer)
		if err != nil {
			fail(a.text(err.Error()))
			continue
		}
		v, found, err := p.Get(body)
		switch {
		case err != nil:
			fail(a.text("pointer does not apply to the body: " + err.Error()))
			continue
		case !found:
			fail("value is absent")
			continue
		}
		str, ok := scalarString(v)
		if !ok {
			fail(fmt.Sprintf("value is %s", jsonutil.TypeName(v)))
			continue
		}
		s.vars[cp.Name] = str
		delete(s.failed, cp.Name)
		if a.red.PathRedacted(p) {
			sensitive = append(sensitive, str)
		}
	}
	return out, sensitive
}

// scalarString converts a JSON scalar to its substitution text. Numbers
// keep their original spelling.
func scalarString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case json.Number:
		return string(t), true
	case bool:
		return strconv.FormatBool(t), true
	}
	return "", false
}
