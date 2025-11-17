// Package redact removes credentials and configured sensitive values from
// headers, JSON documents, URLs and error text before anything is printed,
// persisted or written into a report.
//
// Redaction is a best-effort safeguard driven by configuration. It cannot
// guarantee that an arbitrary payload contains no sensitive data: a secret
// stored under an unexpected key, or embedded inside free text, passes
// through unless it is registered with WithSecrets.
package redact

import (
	"fmt"
	"sort"
	"strings"

	"github.com/happydev123/backendlab/internal/jsonptr"
)

// Marker replaces every redacted value. Its fixed form keeps reports
// diffable and makes redaction visible to readers.
const Marker = "[REDACTED]"

// minSecretLen is the shortest registered secret that is scrubbed from free
// text. Shorter values would cause false positives across ordinary words.
const minSecretLen = 4

// maxDepth bounds recursion when walking JSON documents.
const maxDepth = 64

// DefaultHeaders are always treated as sensitive.
var DefaultHeaders = []string{
	"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie",
	"X-Api-Key", "Api-Key", "X-Auth-Token", "X-Csrf-Token",
}

// DefaultKeys are JSON object keys whose values are always redacted,
// compared case-insensitively.
var DefaultKeys = []string{
	"password", "passwd", "secret", "client_secret", "token", "access_token",
	"refresh_token", "id_token", "api_key", "apikey", "authorization",
}

// DefaultQueryKeys are URL query parameters redacted in URLs and errors.
var DefaultQueryKeys = []string{
	"api_key", "apikey", "key", "token", "access_token", "signature", "sig", "password",
}

// Policy configures a Redactor. Headers and Keys extend the defaults;
// JSONPaths are pointer patterns ("/customer/tax_id", "/items/*/iban").
type Policy struct {
	Headers   []string `yaml:"headers" json:"headers,omitempty"`
	Keys      []string `yaml:"keys" json:"keys,omitempty"`
	JSONPaths []string `yaml:"json_paths" json:"json_paths,omitempty"`
}

// Redactor applies a Policy. It is immutable after construction and safe
// for concurrent use.
type Redactor struct {
	headers   map[string]bool
	keys      map[string]bool
	queryKeys map[string]bool
	paths     jsonptr.PatternSet
	secrets   []string
}

// New builds a Redactor from p, validating its JSON path patterns.
func New(p Policy) (*Redactor, error) {
	r := &Redactor{
		headers:   map[string]bool{},
		keys:      map[string]bool{},
		queryKeys: map[string]bool{},
	}
	for _, h := range append(append([]string(nil), DefaultHeaders...), p.Headers...) {
		h = strings.TrimSpace(h)
		if h == "" {
			return nil, fmt.Errorf("redact: empty header name")
		}
		r.headers[strings.ToLower(h)] = true
	}
	for _, k := range append(append([]string(nil), DefaultKeys...), p.Keys...) {
		k = strings.TrimSpace(k)
		if k == "" {
			return nil, fmt.Errorf("redact: empty key name")
		}
		r.keys[strings.ToLower(k)] = true
	}
	for _, k := range DefaultQueryKeys {
		r.queryKeys[k] = true
	}
	paths, err := jsonptr.ParsePatterns(p.JSONPaths)
	if err != nil {
		return nil, fmt.Errorf("redact: %w", err)
	}
	r.paths = paths
	return r, nil
}

// Default returns a Redactor with only the built-in rules.
func Default() *Redactor {
	r, err := New(Policy{})
	if err != nil {
		panic(err) // built-in policy is static and valid
	}
	return r
}

// WithSecrets returns a copy of r that additionally scrubs the given
// literal values (for example resolved credentials) from all strings.
// Values shorter than four bytes are ignored.
func (r *Redactor) WithSecrets(values ...string) *Redactor {
	cp := *r
	cp.secrets = append([]string(nil), r.secrets...)
	for _, v := range values {
		if len(v) >= minSecretLen {
			cp.secrets = append(cp.secrets, v)
		}
	}
	// Replace longer secrets first so a secret containing another is
	// removed whole.
	sort.Slice(cp.secrets, func(i, j int) bool { return len(cp.secrets[i]) > len(cp.secrets[j]) })
	return &cp
}

// SensitiveHeader reports whether the header name is redacted.
func (r *Redactor) SensitiveHeader(name string) bool {
	return r.headers[strings.ToLower(name)]
}

// SensitiveKey reports whether a JSON object key is redacted.
func (r *Redactor) SensitiveKey(key string) bool {
	return r.keys[strings.ToLower(key)]
}

// String scrubs registered secret values from s.
func (r *Redactor) String(s string) string {
	for _, sec := range r.secrets {
		if strings.Contains(s, sec) {
			s = strings.ReplaceAll(s, sec, Marker)
		}
	}
	return s
}

// Error renders err with registered secrets and sensitive URL parts
// removed. A nil error yields "".
func (r *Redactor) Error(err error) string {
	if err == nil {
		return ""
	}
	return r.scrubURLs(r.String(err.Error()))
}
