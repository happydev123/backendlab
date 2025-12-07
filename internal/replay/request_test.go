package replay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/suite"
	"github.com/happydev123/backendlab/internal/testutil"
)

const requestSuite = `
version: 1
name: requests
defaults:
  headers:
    Accept: application/json
    X-Api-Version: "2"
cases:
  - id: get-item
    method: GET
    path: /items/{id}/notes/{note}
    path_params:
      id: "${item}"
      note: "n 1"
    query:
      z: "${item}"
      a: "x&y=1"
    headers:
      X-Request-Tag: "tag-${item}"
      accept: text/plain
  - id: create
    method: POST
    path: /items
    body:
      json:
        name: "${item}"
        html: "<b>"
        amount: 10.10
  - id: text
    method: PUT
    path: /raw/caf%C3%A9/ü
    body:
      text: "hello ${not_expanded}"
      content_type: text/csv
  - id: dot
    method: GET
    path: /items/{id}
    path_params:
      id: "${dot}"
`

// requestSuiteWith replaces the last case's path parameter template so the
// suite validates without a capturing case.
func requestSuiteWith(t *testing.T, dotValue string) *suite.Suite {
	t.Helper()
	text := strings.Replace(requestSuite, `"${dot}"`, `"`+dotValue+`"`, 1)
	text = strings.Replace(text, "version: 1\n", "version: 1\n", 1)
	// ${item} is referenced without a capture; give the first case a
	// capture so validation accepts the references in later cases.
	text = strings.Replace(text, "cases:\n", "cases:\n  - id: seed\n    method: GET\n    path: /seed\n    capture:\n      - name: item\n        pointer: /item\n", 1)
	s := parseSuite(t, text)
	s.Cases = s.Cases[1:]
	return s
}

func TestBuildURL(t *testing.T) {
	s := requestSuiteWith(t, "..")
	base := resolveTarget(t, "base", config.Target{BaseURL: "http://example.test/api/v1/"}, nil).BaseURL
	vars := map[string]string{"item": "a/b c"}

	u, err := buildURL(base, &s.Cases[0], vars)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := u.String(), "http://example.test/api/v1/items/a%2Fb%20c/notes/n%201?a=x%26y%3D1&z=a%2Fb+c"; got != want {
		t.Fatalf("url\n got  %s\n want %s", got, want)
	}
	u, err = buildURL(base, &s.Cases[2], vars)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := u.EscapedPath(), "/api/v1/raw/caf%C3%A9/%C3%BC"; got != want {
		t.Fatalf("literal path escaped as %s, want %s", got, want)
	}
	if _, err := buildURL(base, &s.Cases[3], vars); err == nil || !strings.Contains(err.Error(), "dot segment") {
		t.Fatalf("dot segment accepted: %v", err)
	}
	if _, err := buildURL(base, &s.Cases[0], map[string]string{}); !errors.Is(err, suite.ErrUndefined) {
		t.Fatalf("undefined variable: %v", err)
	}
}

func TestBuildHeaders(t *testing.T) {
	s := requestSuiteWith(t, "x")
	target := resolveTarget(t, "base", config.Target{
		BaseURL: "http://example.test",
		Headers: map[string]config.HeaderValue{
			"Authorization": {Env: "TOKEN", Prefix: "Bearer "},
			"X-Api-Version": {Value: "target"},
		},
	}, map[string]string{"TOKEN": "tok-123456"})
	h, err := buildHeaders(target, s, &s.Cases[0], map[string]string{"item": "7"})
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]string{
		"Authorization": "Bearer tok-123456",
		"X-Api-Version": "target", // suite cannot override target headers
		"Accept":        "text/plain",
		"X-Request-Tag": "tag-7",
		"User-Agent":    userAgent(),
	}
	for name, want := range checks {
		if got := h.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if _, err := buildHeaders(target, s, &s.Cases[0], map[string]string{"item": "x\r\nX-Evil: 1"}); err == nil {
		t.Fatal("CR/LF in expanded header accepted")
	}
}

func TestBuildBody(t *testing.T) {
	s := requestSuiteWith(t, "x")
	lim := limits.Default()
	data, ct, err := buildBody(&s.Cases[1], map[string]string{"item": `q"uote`}, lim)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"amount":10.10,"html":"<b>","name":"q\"uote"}`; string(data) != want || ct != "application/json" {
		t.Fatalf("json body %s (%s), want %s", data, ct, want)
	}
	data, ct, err = buildBody(&s.Cases[2], nil, lim)
	if err != nil || string(data) != "hello ${not_expanded}" || ct != "text/csv" {
		t.Fatalf("text body %q %q %v", data, ct, err)
	}
	lim.MaxRequestBytes = 10
	if _, _, err := buildBody(&s.Cases[1], map[string]string{"item": "x"}, lim); err == nil {
		t.Fatal("oversized body accepted")
	}
	if data, ct, err := buildBody(&s.Cases[0], nil, lim); data != nil || ct != "" || err != nil {
		t.Fatal("case without body produced one")
	}
}

func TestBuildRequestOnTheWire(t *testing.T) {
	t.Setenv(testutil.EnvAsyncClose, "1")
	type seen struct {
		rawPath, query, ua, ct, body string
	}
	got := make(chan seen, 1)
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- seen{r.URL.EscapedPath(), r.URL.RawQuery, r.UserAgent(), r.Header.Get("Content-Type"), string(b)}
	}))
	s := requestSuiteWith(t, "x")
	target := resolveTarget(t, "base", config.Target{BaseURL: srv.URL + "/v1"}, nil)
	client := &http.Client{Transport: testutil.Transport()}

	send := func(c *suite.Case, vars map[string]string) seen {
		t.Helper()
		req, err := buildRequest(context.Background(), target, s, c, vars, limits.Default())
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return <-got
	}

	g := send(&s.Cases[0], map[string]string{"item": "a/b c"})
	if g.rawPath != "/v1/items/a%2Fb%20c/notes/n%201" || g.query != "a=x%26y%3D1&z=a%2Fb+c" || g.ua != userAgent() {
		t.Fatalf("server saw %+v", g)
	}
	g = send(&s.Cases[1], map[string]string{"item": "n"})
	if g.ct != "application/json" || g.body != `{"amount":10.10,"html":"<b>","name":"n"}` {
		t.Fatalf("server saw %+v", g)
	}
}
