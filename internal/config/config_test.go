package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) EnvLookup {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestParseCLI(t *testing.T) {
	c, err := ParseCLI([]byte(`
version: 1
output:
  format: markdown
targets:
  baseline:
    base_url: http://127.0.0.1:8090/baseline
  candidate:
    base_url: https://candidate.example.com/v2
    headers:
      Authorization: {env: CANDIDATE_TOKEN, prefix: "Bearer "}
      X-Client: backendlab
limits:
  max_cases: 50
  request_timeout: 3s
redact:
  json_paths: [/customer/tax_id]
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Limits.MaxCases != 50 || c.Limits.RequestTimeout != 3*time.Second {
		t.Fatalf("limits = %+v", c.Limits)
	}
	if c.Limits.MaxResponseBytes == 0 {
		t.Fatal("defaults not merged")
	}
	rt, err := c.Targets["candidate"].Resolve("candidate", env(map[string]string{"CANDIDATE_TOKEN": "s3cr3t-token"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Headers.Get("Authorization") != "Bearer s3cr3t-token" || rt.Headers.Get("X-Client") != "backendlab" {
		t.Fatalf("headers = %v", rt.Headers)
	}
	if len(rt.Secrets) != 1 || rt.Secrets[0] != "s3cr3t-token" {
		t.Fatalf("secrets = %v", rt.Secrets)
	}
	if rt.Origin() != "https://candidate.example.com" {
		t.Fatalf("origin = %s", rt.Origin())
	}
	if _, err := c.Targets["candidate"].Resolve("candidate", env(nil), nil); err == nil || !strings.Contains(err.Error(), "not set") {
		t.Fatalf("missing env err = %v", err)
	}
	_, err = c.Targets["candidate"].Resolve("candidate", env(map[string]string{"CANDIDATE_TOKEN": "x"}), map[string]bool{"OTHER": true})
	if !errors.Is(err, ErrCredentialNotAllowed) {
		t.Fatalf("allowlist err = %v", err)
	}
}

func TestCLIErrors(t *testing.T) {
	tests := map[string]string{
		"unknown key":       "version: 1\nbogus: true\n",
		"bad version":       "version: 3\n",
		"bad format":        "version: 1\noutput: {format: html}\n",
		"literal secret":    "version: 1\ntargets:\n  a:\n    base_url: http://x\n    headers:\n      Authorization: Bearer abc\n",
		"userinfo":          "version: 1\ntargets:\n  a:\n    base_url: http://u:p@x\n",
		"bad scheme":        "version: 1\ntargets:\n  a:\n    base_url: ftp://x\n",
		"query in base":     "version: 1\ntargets:\n  a:\n    base_url: http://x/?a=1\n",
		"bad target name":   "version: 1\ntargets:\n  Prod Env:\n    base_url: http://x\n",
		"header both":       "version: 1\ntargets:\n  a:\n    base_url: http://x\n    headers:\n      X-K: {env: K, value: v}\n",
		"header unknown":    "version: 1\ntargets:\n  a:\n    base_url: http://x\n    headers:\n      X-K: {from: K}\n",
		"bad env name":      "version: 1\ntargets:\n  a:\n    base_url: http://x\n    headers:\n      X-K: {env: 'lower'}\n",
		"limit over hard":   "version: 1\nlimits: {max_cases: 999999}\n",
		"negative limit":    "version: 1\nlimits: {max_concurrency: -1}\n",
		"bad redact path":   "version: 1\nredact: {json_paths: [nope]}\n",
		"timeout inversion": "version: 1\nlimits: {request_timeout: 5m, max_request_timeout: 1m}\n",
	}
	for name, doc := range tests {
		if _, err := ParseCLI([]byte(doc)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoadCLIImplicitMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "backendlab.yaml")
	c, err := LoadCLI(missing, false)
	if err != nil || c.Version != 1 {
		t.Fatalf("implicit missing config: %v", err)
	}
	if _, err := LoadCLI(missing, true); err == nil {
		t.Fatal("explicit missing config should fail")
	}
	big := filepath.Join(t.TempDir(), "big.yaml")
	if err := os.WriteFile(big, make([]byte, maxConfigBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCLI(big, true); err == nil {
		t.Fatal("oversized config should fail")
	}
}
