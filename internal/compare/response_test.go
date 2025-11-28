package compare

import (
	"net/http"
	"testing"

	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/suite"
)

func TestResponses(t *testing.T) {
	base := Response{
		Status: 200,
		Header: http.Header{
			"Content-Type":  {"application/json"},
			"X-Version":     {"1"},
			"X-Removed":     {"yes"},
			"Set-Cookie":    {"session=abc", "theme=dark"},
			"X-Not-Listed":  {"a"},
			"Cache-Control": {"no-store"},
		},
		Body: []byte(`{"a":1}`),
	}
	cand := Response{
		Status: 500,
		Header: http.Header{
			"Content-Type":  {"application/json"},
			"X-Version":     {"2"},
			"X-Added":       {"new"},
			"Set-Cookie":    {"session=def", "theme=dark"},
			"X-Not-Listed":  {"b"},
			"Cache-Control": {"no-store"},
		},
		Body: []byte(`{"a":2}`),
	}
	spec := suite.CompareSpec{Headers: []string{"x-version", "X-Removed", "x-added", "set-cookie", "content-type", "x-absent"}}
	r := Responses(base, cand, specOptions(t, spec))
	want := "compare.status_changed@status compare.header_changed@header:Set-Cookie compare.header_added@header:X-Added " +
		"compare.header_missing@header:X-Removed compare.header_changed@header:X-Version compare.value_changed@/a"
	if got := summary(r); got != want {
		t.Fatalf("got\n %s\nwant\n %s", got, want)
	}
	for _, d := range r.Differences {
		if d.Location == "header:Set-Cookie" && (d.Baseline != redact.Marker || d.Candidate != redact.Marker) {
			t.Fatalf("sensitive header shown: %+v", d)
		}
		if d.Location == "header:X-Removed" && d.Candidate != absent {
			t.Fatalf("absent header preview: %+v", d)
		}
	}
}

func TestResponsesSkip(t *testing.T) {
	base := Response{Status: 200, Body: []byte("a")}
	cand := Response{Status: 404, Body: []byte("b"), SkipBody: true}
	r := Responses(base, cand, specOptions(t, suite.CompareSpec{SkipStatus: true}))
	if !r.Equal() {
		t.Fatalf("expected no differences: %s", summary(r))
	}
}

func TestResponsesSharedBound(t *testing.T) {
	base := Response{Status: 200, Header: http.Header{"A": {"1"}, "B": {"1"}}, Body: []byte(`{"x":1,"y":1}`)}
	cand := Response{Status: 201, Header: http.Header{"A": {"2"}, "B": {"2"}}, Body: []byte(`{"x":2,"y":2}`)}
	o := specOptions(t, suite.CompareSpec{Headers: []string{"A", "B"}})
	o.MaxDifferences = 2
	r := Responses(base, cand, o)
	if len(r.Differences) != 2 || r.Dropped != 3 {
		t.Fatalf("kept %d dropped %d", len(r.Differences), r.Dropped)
	}
}
