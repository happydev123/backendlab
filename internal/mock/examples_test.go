package mock

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/suite"
)

const examplesDir = "../../examples/invoice-api"

var exampleSuites = []string{
	"compare-suite.yaml",
	"stream-suite.yaml",
	"validate-suite.yaml",
	"writes-suite.yaml",
	"slow-suite.yaml",
}

func TestExampleSuitesParse(t *testing.T) {
	for _, name := range exampleSuites {
		if _, err := suite.LoadFile(filepath.Join(examplesDir, name), limits.Default()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestExampleWritesSuiteIsMutating(t *testing.T) {
	l, err := suite.LoadFile(filepath.Join(examplesDir, "writes-suite.yaml"), limits.Default())
	if err != nil {
		t.Fatal(err)
	}
	if got := l.Suite.MutatingCases(); len(got) != 1 || got[0] != "increment" {
		t.Fatalf("mutating cases %v", got)
	}
	for _, name := range exampleSuites {
		if name == "writes-suite.yaml" {
			continue
		}
		l, err := suite.LoadFile(filepath.Join(examplesDir, name), limits.Default())
		if err != nil {
			t.Fatal(err)
		}
		if l.Suite.Mutating() {
			t.Errorf("%s should be read-only", name)
		}
	}
}

func TestExampleConfig(t *testing.T) {
	cfg, err := config.LoadCLI(filepath.Join(examplesDir, "backendlab.yaml"), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range Variants() {
		tgt, ok := cfg.Targets[string(v)]
		if !ok {
			t.Fatalf("target %s missing", v)
		}
		u, err := url.Parse(tgt.BaseURL)
		if err != nil {
			t.Fatal(err)
		}
		if u.Host != DefaultAddr || u.Path != "/"+string(v) {
			t.Errorf("target %s base_url %s does not point at the mock", v, tgt.BaseURL)
		}
		if len(tgt.Headers) != 0 {
			t.Errorf("target %s sets headers; the mock needs none", v)
		}
	}
}

// TestExampleCasesMatchRoutes sends every example case to the mock handler
// for both variants and checks the status the suite expects, so the
// examples cannot drift away from the mock's URL layout.
func TestExampleCasesMatchRoutes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxDelay = time.Millisecond
	h := New(cfg).Handler()
	captured := map[string]string{"first_invoice_id": "inv-1001"}

	for _, name := range exampleSuites {
		l, err := suite.LoadFile(filepath.Join(examplesDir, name), limits.Default())
		if err != nil {
			t.Fatal(err)
		}
		for i := range l.Suite.Cases {
			c := &l.Suite.Cases[i]
			path := c.Path
			for k, raw := range c.PathParams {
				val, err := suite.ExpandString(raw, captured)
				if err != nil {
					t.Fatalf("%s %s: %v", name, c.ID, err)
				}
				path = strings.ReplaceAll(path, "{"+k+"}", url.PathEscape(val))
			}
			q := url.Values{}
			for k, v := range c.Query {
				q.Set(k, v)
			}
			for _, v := range Variants() {
				target := "/" + string(v) + path
				if len(q) > 0 {
					target += "?" + q.Encode()
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(c.Method, target, nil))
				if rec.Code == http.StatusNotFound && !c.Expect.Status.Contains(http.StatusNotFound) {
					t.Errorf("%s %s (%s): %s %s is not routed", name, c.ID, v, c.Method, target)
					continue
				}
				if !c.Expect.Status.Contains(rec.Code) {
					t.Errorf("%s %s (%s): status %d, suite expects %v", name, c.ID, v, rec.Code, c.Expect.Status)
				}
			}
		}
	}
}
