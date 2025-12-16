package replay

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/suite"
)

func testAsserter() asserter {
	return asserter{caseID: "c1", target: "base", red: redact.Default().WithSecrets("sekrit-value"), max: 40}
}

func codes(fs []finding.Finding) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = f.Code + "@" + f.Location
	}
	return strings.Join(parts, " ")
}

func TestCheckStatus(t *testing.T) {
	a := testAsserter()
	if fs := a.checkStatus(nil, 503); len(fs) != 0 {
		t.Fatal("empty list must accept any status")
	}
	if fs := a.checkStatus(suite.StatusList{200, 204}, 204); len(fs) != 0 {
		t.Fatal("listed status rejected")
	}
	fs := a.checkStatus(suite.StatusList{200, 204}, 500)
	if len(fs) != 1 || fs[0].Expected != "200 or 204" || fs[0].Observed != "500" || fs[0].Kind != finding.KindAssertionFailure {
		t.Fatalf("got %+v", fs)
	}
	if fs[0].CaseID != "c1" || fs[0].Details["target"] != "base" || fs[0].Module != finding.ModuleReplay {
		t.Fatalf("identity %+v", fs[0])
	}
}

func TestCheckHeaders(t *testing.T) {
	a := testAsserter()
	h := http.Header{"Content-Type": {"application/json"}, "Set-Cookie": {"a=1"}, "X-Multi": {"a", "b"}}
	exp := map[string]string{
		"content-type": "application/json",
		"X-Multi":      "a, b",
		"X-Missing":    "1",
		"set-cookie":   "a=2",
	}
	fs := a.checkHeaders(exp, h)
	if got := codes(fs); got != "replay.header_mismatch@header:Set-Cookie replay.header_mismatch@header:X-Missing" {
		t.Fatalf("got %s", got)
	}
	if fs[0].Expected != redact.Marker || fs[0].Observed != redact.Marker {
		t.Fatalf("sensitive header values shown: %+v", fs[0])
	}
	if fs[1].Observed != absentValue {
		t.Fatalf("missing header: %+v", fs[1])
	}
}

func TestCheckDuration(t *testing.T) {
	a := testAsserter()
	if fs := a.checkDuration(0, time.Hour); len(fs) != 0 {
		t.Fatal("no threshold must not fail")
	}
	if fs := a.checkDuration(suite.Duration(time.Second), time.Second); len(fs) != 0 {
		t.Fatal("equal to threshold must pass")
	}
	fs := a.checkDuration(suite.Duration(750*time.Millisecond), 812345*time.Microsecond)
	if len(fs) != 1 || fs[0].Code != CodeMaxDurationExceeded || fs[0].Observed != "812ms" || fs[0].Expected != "<= 750ms" {
		t.Fatalf("got %+v", fs)
	}
}

func TestCheckJSON(t *testing.T) {
	s := parseSuite(t, `
version: 1
name: assertions
cases:
  - id: c1
    method: GET
    path: /x
    expect:
      json:
        - {pointer: /total, equals: 10.50}
        - {pointer: /total, equals: 10.5}
        - {pointer: /items, equals: [{b: 2, a: 1}]}
        - {pointer: /missing, exists: false}
        - {pointer: /id, exists: true}
        - {pointer: /name, type: string}
        - {pointer: /name, matches: "^inv-[0-9]+$"}
        - {pointer: /total, equals: 11}
        - {pointer: /gone, exists: true}
        - {pointer: /id, exists: false}
        - {pointer: /name, type: number}
        - {pointer: /total, matches: "1"}
        - {pointer: /name, matches: "^x"}
        - {pointer: /items/one, equals: 1}
        - {pointer: /password, equals: "guess"}
        - {pointer: "", type: object}
`)
	body, err := jsonutil.Decode([]byte(`{"total":10.500,"items":[{"a":1,"b":2}],"id":7,"name":"inv-42","password":"hunter2"}`))
	if err != nil {
		t.Fatal(err)
	}
	fs := testAsserter().checkJSON(s.Cases[0].Expect.JSON, body)
	want := []string{
		"replay.json_not_equal@/total",
		"replay.json_exists@/gone",
		"replay.json_exists@/id",
		"replay.json_type@/name",
		"replay.json_no_match@/total",
		"replay.json_no_match@/name",
		"replay.json_pointer@/items/one",
		"replay.json_not_equal@/password",
	}
	if got := codes(fs); got != strings.Join(want, " ") {
		t.Fatalf("got\n %s\nwant\n %s", got, strings.Join(want, " "))
	}
	for _, f := range fs {
		if f.Location == "/password" && (f.Observed != redact.Marker || f.Expected != redact.Marker) {
			t.Fatalf("sensitive value shown: %+v", f)
		}
	}
}

func TestBodyNotJSON(t *testing.T) {
	f := testAsserter().bodyNotJSON(12, "expect.json")
	if f.Code != CodeBodyNotJSON || f.Kind != finding.KindAssertionFailure || !strings.Contains(f.Observed, "12 bytes") {
		t.Fatalf("got %+v", f)
	}
}
