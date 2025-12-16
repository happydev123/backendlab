package replay

import (
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/jsonutil"
)

func TestCapture(t *testing.T) {
	s := parseSuite(t, `
version: 1
name: captures
cases:
  - id: login
    method: POST
    path: /login
    capture:
      - {name: user_id, pointer: /user/id}
      - {name: name, pointer: /user/name}
      - {name: admin, pointer: /user/admin}
      - {name: token, pointer: /auth/token}
      - {name: roles, pointer: /user/roles}
      - {name: missing, pointer: /user/missing}
`)
	c := &s.Cases[0]
	body, err := jsonutil.Decode([]byte(`{"user":{"id":1.50,"name":"a/b c","admin":false,"roles":["x"]},"auth":{"token":"tok-abcdef"}}`))
	if err != nil {
		t.Fatal(err)
	}
	sc := newScope()
	fs, sensitive := testAsserter().capture(c, body, true, sc)
	if got := codes(fs); got != "replay.capture_failed@/user/roles replay.capture_failed@/user/missing" {
		t.Fatalf("findings: %s", got)
	}
	want := map[string]string{"user_id": "1.50", "name": "a/b c", "admin": "false", "token": "tok-abcdef"}
	for k, v := range want {
		if sc.vars[k] != v {
			t.Errorf("%s = %q, want %q", k, sc.vars[k], v)
		}
	}
	if len(sensitive) != 1 || sensitive[0] != "tok-abcdef" {
		t.Fatalf("sensitive captures: %v", sensitive)
	}
	if sc.failed["roles"] != "login" || sc.failed["missing"] != "login" {
		t.Fatalf("failed map: %v", sc.failed)
	}

	refs := map[string][]string{"user_id": {"path_params.id"}, "roles": {"query.r"}, "other": {"headers.X"}}
	missing := sc.missing(refs)
	if strings.Join(missing, ",") != "other,roles" {
		t.Fatalf("missing = %v", missing)
	}
	msg := sc.explain(missing)
	if !strings.Contains(msg, `${roles} (capture in case "login" failed)`) || !strings.Contains(msg, "${other} (not captured)") {
		t.Fatalf("explain: %s", msg)
	}
}

func TestCaptureNotJSON(t *testing.T) {
	s := parseSuite(t, `
version: 1
name: captures
cases:
  - id: c
    method: GET
    path: /x
    capture:
      - {name: v, pointer: /v}
`)
	sc := newScope()
	fs, _ := testAsserter().capture(&s.Cases[0], nil, false, sc)
	if len(fs) != 1 || !strings.Contains(fs[0].Message, "not JSON") || sc.failed["v"] != "c" {
		t.Fatalf("got %+v", fs)
	}
	sc2 := newScope()
	sc2.markFailed(&s.Cases[0])
	if sc2.failed["v"] != "c" {
		t.Fatal("markFailed did not record the capture")
	}
}
