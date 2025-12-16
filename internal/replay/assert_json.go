package replay

import (
	"fmt"
	"regexp"

	"github.com/happydev123/backendlab/internal/compare"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/suite"
)

// checkJSON evaluates expect.json assertions against a decoded body.
// Equality uses JSON semantics: object key order is ignored and numbers
// are equal when their decimal values are equal.
func (a asserter) checkJSON(asserts []suite.Assertion, body any) []finding.Finding {
	var out []finding.Finding
	for _, as := range asserts {
		if f := a.checkAssertion(as, body); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

func (a asserter) checkAssertion(as suite.Assertion, body any) *finding.Finding {
	loc := pointerLocation(as.Pointer)
	p, err := jsonptr.Parse(as.Pointer)
	if err != nil {
		f := a.failure(CodeJSONPointer, loc, a.text(err.Error()), "", "")
		return &f
	}
	v, found, err := p.Get(body)
	if err != nil {
		f := a.failure(CodeJSONPointer, loc, a.text("pointer does not apply to the body: "+err.Error()), "", "")
		return &f
	}
	observed := absentValue
	if found {
		observed = a.value(p, v)
	}
	fail := func(code, msg, expected string) *finding.Finding {
		f := a.failure(code, loc, msg, expected, observed)
		return &f
	}
	switch {
	case as.Exists != nil:
		if *as.Exists && !found {
			return fail(CodeJSONExists, "value is absent", "present")
		}
		if !*as.Exists && found {
			return fail(CodeJSONExists, "value is present", absentValue)
		}
	case as.Equals != nil:
		if !found {
			return fail(CodeJSONNotEqual, "value is absent", a.value(p, as.Equals.V))
		}
		if !compare.Equal(as.Equals.V, v) {
			return fail(CodeJSONNotEqual, "value differs", a.value(p, as.Equals.V))
		}
	case as.Type != "":
		if !found {
			return fail(CodeJSONType, "value is absent", as.Type)
		}
		if got := jsonutil.TypeName(v); got != as.Type {
			return fail(CodeJSONType, fmt.Sprintf("value is %s, not %s", got, as.Type), as.Type)
		}
	case as.Matches != "":
		re, err := regexp.Compile(as.Matches)
		if err != nil {
			return fail(CodeJSONNoMatch, "invalid regular expression", a.text(as.Matches))
		}
		if !found {
			return fail(CodeJSONNoMatch, "value is absent", a.text(as.Matches))
		}
		s, ok := v.(string)
		if !ok {
			return fail(CodeJSONNoMatch, fmt.Sprintf("value is %s; matches applies to strings", jsonutil.TypeName(v)), a.text(as.Matches))
		}
		if !re.MatchString(s) {
			return fail(CodeJSONNoMatch, "string does not match the regular expression", a.text(as.Matches))
		}
	}
	return nil
}

// bodyNotJSON reports that JSON checks could not run because the body is
// not a JSON document. The body itself is not quoted.
func (a asserter) bodyNotJSON(size int, reason string) finding.Finding {
	return a.failure(CodeBodyNotJSON, "body", "response body is not JSON, so "+reason+" could not be evaluated",
		"JSON document", fmt.Sprintf("%d bytes of non-JSON data", size))
}

// pointerLocation formats a pointer for findings; the root is "body".
func pointerLocation(ptr string) string {
	if ptr == "" {
		return "body"
	}
	return ptr
}
