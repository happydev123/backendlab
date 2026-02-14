package validate

import (
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
)

func TestCompileLayout(t *testing.T) {
	tests := []struct {
		in, layout, err string
	}{
		{in: "", layout: "2006-01-02"},
		{in: "date", layout: "2006-01-02"},
		{in: "rfc3339", layout: "2006-01-02T15:04:05Z07:00"},
		{in: "02.01.2006", layout: "02.01.2006"},
		{in: "Jan 2, 2006", layout: "Jan 2, 2006"},
		{in: "YYYY-MM-DD", err: "no Go reference-time elements"},
		{in: "2006-01", err: "must include year, month and day"},
		{in: "01/02", err: "must include year, month and day"},
		{in: "15:04", err: "must include year, month and day"},
		{in: strings.Repeat("2006", 20), err: "longer than"},
	}
	for _, tt := range tests {
		l, err := compileLayout(tt.in)
		if tt.err != "" {
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("%q: err = %v, want %q", tt.in, err, tt.err)
			}
			continue
		}
		if err != nil || l.layout != tt.layout {
			t.Errorf("%q: got %q, %v", tt.in, l.layout, err)
		}
	}
}

func TestDateRule(t *testing.T) {
	tests := []struct {
		layout, doc, want string
	}{
		{"date", `{"issue_date":"2024-03-09"}`, ""},
		{"date", `{"issue_date":"2024-02-30"}`, "validate.date@/issue_date"},
		{"date", `{"issue_date":"09.03.2024"}`, "validate.date@/issue_date"},
		{"date", `{"issue_date":"2024-03-09T10:00:00Z"}`, "validate.date@/issue_date"},
		{"date", `{"issue_date":20240309}`, "validate.date@/issue_date"},
		{"date", `{"issue_date":null}`, ""},
		{"date", `{}`, ""},
		{"02.01.2006", `{"issue_date":"09.03.2024"}`, ""},
		{"rfc3339", `{"issue_date":"2024-03-09T10:00:00+01:00"}`, ""},
		{"rfc3339", `{"issue_date":"2024-03-09"}`, "validate.date@/issue_date"},
	}
	for _, tt := range tests {
		r, err := compileDate(newBase("dates", typeDate), ruleSpec{Fields: []string{"/issue_date"}, Layout: tt.layout})
		if err != nil {
			t.Fatal(err)
		}
		if got := codes(runRule(t, r, mustDecode(t, tt.doc))); got != tt.want {
			t.Errorf("%s %s: got %q, want %q", tt.layout, tt.doc, got, tt.want)
		}
	}
	r, _ := compileDate(newBase("dates", typeDate), ruleSpec{Fields: []string{"/payments/*/date"}})
	fs := runRule(t, r, mustDecode(t, `{"payments":[{"date":"2024-01-01"},{"date":"2024-13-01"}]}`))
	if codes(fs) != "validate.date@/payments/1/date" || fs[0].Expected != "date in layout date (2006-01-02)" {
		t.Fatalf("wildcard: %+v", fs)
	}
	if _, err := compileDate(newBase("dates", typeDate), ruleSpec{Fields: []string{"/d"}, Layout: "DD/MM/YYYY"}); err == nil {
		t.Fatal("invalid layout accepted")
	}
}

func TestDateOrderRule(t *testing.T) {
	tests := []struct {
		name       string
		allowEqual bool
		layout     string
		doc, want  string
		kind       finding.Kind
	}{
		{name: "ordered", doc: `{"issue_date":"2024-03-01","due_date":"2024-03-31"}`},
		{name: "equal rejected", doc: `{"issue_date":"2024-03-01","due_date":"2024-03-01"}`,
			want: "validate.date_order@/due_date", kind: finding.KindAssertionFailure},
		{name: "equal allowed", allowEqual: true, doc: `{"issue_date":"2024-03-01","due_date":"2024-03-01"}`},
		{name: "reversed", allowEqual: true, doc: `{"issue_date":"2024-03-10","due_date":"2024-03-01"}`,
			want: "validate.date_order@/due_date", kind: finding.KindAssertionFailure},
		{name: "missing", doc: `{"issue_date":"2024-03-10"}`,
			want: "validate.input_missing@/due_date", kind: finding.KindInconclusive},
		{name: "both missing", doc: `{"issue_date":null}`,
			want: "validate.input_missing@/issue_date", kind: finding.KindInconclusive},
		{name: "unparseable", doc: `{"issue_date":"10/03/2024","due_date":"2024-03-01"}`,
			want: "validate.date_order@/issue_date", kind: finding.KindAssertionFailure},
		{name: "instants across zones", layout: "rfc3339",
			doc: `{"issue_date":"2024-03-01T10:00:00+02:00","due_date":"2024-03-01T09:00:00Z"}`},
		{name: "instants reversed", layout: "rfc3339",
			doc:  `{"issue_date":"2024-03-01T10:00:00Z","due_date":"2024-03-01T11:00:00+02:00"}`,
			want: "validate.date_order@/due_date", kind: finding.KindAssertionFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := compileDateOrder(newBase("order", typeDateOrder), ruleSpec{
				Before: "/issue_date", After: "/due_date", AllowEqual: tt.allowEqual, Layout: tt.layout,
			})
			if err != nil {
				t.Fatal(err)
			}
			fs := runRule(t, r, mustDecode(t, tt.doc))
			if got := codes(fs); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if len(fs) > 0 && fs[0].Kind != tt.kind {
				t.Fatalf("kind = %s", fs[0].Kind)
			}
		})
	}
	r, _ := compileDateOrder(newBase("order", typeDateOrder), ruleSpec{Before: "/issue_date", After: "/due_date", AllowEqual: true})
	fs := runRule(t, r, mustDecode(t, `{"issue_date":"2024-03-10","due_date":"2024-03-01"}`))
	if fs[0].Expected != "/issue_date <= /due_date (layout date (2006-01-02))" ||
		fs[0].Observed != `/issue_date "2024-03-10", /due_date "2024-03-01"` ||
		!strings.Contains(fs[0].Message, "/issue_date is after /due_date") {
		t.Fatalf("finding = %+v", fs[0])
	}
	for _, spec := range []ruleSpec{
		{Before: "/a", After: "/a"},
		{Before: "/a"},
		{Before: "/a", After: "/b", Layout: "YYYY"},
	} {
		if _, err := compileDateOrder(newBase("x", typeDateOrder), spec); err == nil {
			t.Errorf("%+v accepted", spec)
		}
	}
}
