package redact

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/jsonutil"
)

func TestHeaders(t *testing.T) {
	r, err := New(Policy{Headers: []string{"X-Tenant-Secret"}})
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer abc")
	h.Set("Cookie", "session=1")
	h.Set("X-Tenant-Secret", "s")
	h.Set("Content-Type", "application/json")
	out := r.Headers(h)
	for _, name := range []string{"Authorization", "Cookie", "X-Tenant-Secret"} {
		if out.Get(name) != Marker {
			t.Errorf("%s = %q, want redacted", name, out.Get(name))
		}
	}
	if out.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type changed: %q", out.Get("Content-Type"))
	}
	if h.Get("Authorization") != "Bearer abc" {
		t.Error("input header modified")
	}
	m := r.HeaderMap(h)
	if m["Authorization"] != Marker || m["Content-Type"] != "application/json" {
		t.Errorf("HeaderMap = %v", m)
	}
}

func TestJSONRedaction(t *testing.T) {
	r, err := New(Policy{JSONPaths: []string{"/customer/tax_id", "/items/*/iban"}, Keys: []string{"ssn"}})
	if err != nil {
		t.Fatal(err)
	}
	r = r.WithSecrets("tok_live_123456")
	doc, err := jsonutil.Decode([]byte(`{
		"customer": {"name": "Ada", "tax_id": "DE123", "ssn": "1"},
		"items": [{"iban": "X1", "amount": 5}, {"iban": "X2"}],
		"password": "p",
		"note": "token tok_live_123456 used",
		"Access_Token": "t"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	got := r.JSON(doc).(map[string]any)
	cust := got["customer"].(map[string]any)
	if cust["tax_id"] != Marker || cust["ssn"] != Marker || cust["name"] != "Ada" {
		t.Errorf("customer = %v", cust)
	}
	items := got["items"].([]any)
	if items[0].(map[string]any)["iban"] != Marker || items[1].(map[string]any)["iban"] != Marker {
		t.Errorf("items = %v", items)
	}
	if got["password"] != Marker || got["Access_Token"] != Marker {
		t.Errorf("sensitive keys not redacted: %v", got)
	}
	if got["note"] != "token "+Marker+" used" {
		t.Errorf("note = %v", got["note"])
	}
	// The original document is untouched.
	if doc.(map[string]any)["password"] != "p" {
		t.Error("input modified")
	}
	if !r.PathRedacted(jsonptr.MustParse("/customer/tax_id")) || !r.PathRedacted(jsonptr.MustParse("/x/password/y")) {
		t.Error("PathRedacted false negative")
	}
	if r.PathRedacted(jsonptr.MustParse("/customer/name")) {
		t.Error("PathRedacted false positive")
	}
}

func TestSecretsAndErrors(t *testing.T) {
	r := Default().WithSecrets("abc", "supersecret", "supersecret-extended")
	if got := r.String("x supersecret-extended y"); got != "x "+Marker+" y" {
		t.Errorf("longest secret should win, got %q", got)
	}
	if got := r.String("abc"); got != "abc" {
		t.Errorf("short values must not be scrubbed, got %q", got)
	}
	err := errors.New(`Get "https://user:pw@api.example.com/v1?api_key=k1&page=2": dial tcp: refused supersecret`)
	got := r.Error(err)
	for _, leak := range []string{"user:pw", "k1", "supersecret"} {
		if strings.Contains(got, leak) {
			t.Errorf("error text leaks %q: %s", leak, got)
		}
	}
	if !strings.Contains(got, "page=2") {
		t.Errorf("non-sensitive query lost: %s", got)
	}
	if r.Error(nil) != "" {
		t.Error("nil error should render empty")
	}
}

func TestURL(t *testing.T) {
	u, _ := url.Parse("https://u:p@h.example/x?token=abc&q=1")
	got := Default().URL(u)
	if strings.Contains(got, "u:p") || strings.Contains(got, "abc") {
		t.Errorf("URL leaks: %s", got)
	}
}

func TestPolicyValidation(t *testing.T) {
	if _, err := New(Policy{JSONPaths: []string{"no-slash"}}); err == nil {
		t.Error("expected invalid path error")
	}
	if _, err := New(Policy{Headers: []string{" "}}); err == nil {
		t.Error("expected empty header error")
	}
}

func TestDepthBound(t *testing.T) {
	var v any = "leaf"
	for i := 0; i < maxDepth+10; i++ {
		v = []any{v}
	}
	out := Default().JSON(v)
	depth := 0
	for {
		arr, ok := out.([]any)
		if !ok {
			break
		}
		out = arr[0]
		depth++
	}
	if out != Marker {
		t.Fatalf("expected deep value to be replaced, got %v at depth %d", out, depth)
	}
}
