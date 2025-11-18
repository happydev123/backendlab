// Package config loads and validates BackendLab configuration: CLI
// settings, target definitions and server settings. Decoding is strict;
// unknown keys and invalid values are errors. Secrets are never written in
// configuration files, only referenced by environment variable name.
package config

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var targetNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
var envNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// sensitiveHeaders must be supplied from the environment, never literally.
var sensitiveHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "cookie": true,
	"x-api-key": true, "api-key": true, "x-auth-token": true,
}

// HeaderValue is a literal header value or a reference to an environment
// variable. In YAML it is either a string or {env: NAME, prefix: "Bearer "}.
type HeaderValue struct {
	Value  string `yaml:"value"`
	Env    string `yaml:"env"`
	Prefix string `yaml:"prefix"`
}

// UnmarshalYAML accepts the scalar and mapping forms.
func (h *HeaderValue) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		h.Value = n.Value
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: header value must be a string or a mapping", n.Line)
	}
	// Node.Decode does not inherit the outer decoder's KnownFields
	// setting, so unknown keys are checked here.
	for i := 0; i < len(n.Content); i += 2 {
		switch n.Content[i].Value {
		case "value", "env", "prefix":
		default:
			return fmt.Errorf("line %d: unknown header field %q", n.Content[i].Line, n.Content[i].Value)
		}
	}
	type raw HeaderValue
	var r raw
	if err := n.Decode(&r); err != nil {
		return err
	}
	*h = HeaderValue(r)
	return nil
}

// Target is a named environment such as "baseline" or "candidate".
type Target struct {
	BaseURL string                 `yaml:"base_url"`
	Headers map[string]HeaderValue `yaml:"headers"`
	// FollowRedirects enables following up to five redirects. Redirects are
	// not followed by default; credentials are dropped when a redirect
	// changes origin. Server mode additionally applies egress policy.
	FollowRedirects bool `yaml:"follow_redirects"`
	// CAFile adds PEM certificates to the trusted roots. TLS verification
	// cannot be disabled.
	CAFile string `yaml:"ca_file"`
}

// Validate checks the target definition.
func (t Target) Validate(name string) error {
	if !targetNamePattern.MatchString(name) {
		return fmt.Errorf("target name %q must match %s", name, targetNamePattern)
	}
	u, err := url.Parse(t.BaseURL)
	if err != nil || t.BaseURL == "" {
		return fmt.Errorf("target %s: base_url must be an absolute http or https URL", name)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("target %s: base_url scheme must be http or https", name)
	case u.Host == "":
		return fmt.Errorf("target %s: base_url has no host", name)
	case u.User != nil:
		return fmt.Errorf("target %s: base_url must not contain credentials; use headers with env", name)
	case u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("target %s: base_url must not have a query or fragment", name)
	}
	for _, hname := range sortedKeys(t.Headers) {
		hv := t.Headers[hname]
		if hv.Env != "" && hv.Value != "" {
			return fmt.Errorf("target %s: header %s sets both value and env", name, hname)
		}
		if hv.Env == "" && hv.Prefix != "" {
			return fmt.Errorf("target %s: header %s: prefix requires env", name, hname)
		}
		if hv.Env != "" && !envNamePattern.MatchString(hv.Env) {
			return fmt.Errorf("target %s: header %s: invalid environment variable name %q", name, hname, hv.Env)
		}
		if hv.Env == "" && sensitiveHeaders[strings.ToLower(hname)] {
			return fmt.Errorf("target %s: header %s is a credential and must use env", name, hname)
		}
		if strings.ContainsAny(hv.Value+hv.Prefix, "\r\n\x00") {
			return fmt.Errorf("target %s: header %s contains control characters", name, hname)
		}
	}
	return nil
}

// EnvLookup reads an environment variable. os.LookupEnv satisfies it.
type EnvLookup func(string) (string, bool)

// OSEnv is the process environment.
var OSEnv EnvLookup = os.LookupEnv

// ResolvedTarget is a target with credentials read from the environment.
// It exists only in memory; it must never be persisted or printed.
type ResolvedTarget struct {
	Name            string
	BaseURL         *url.URL
	Headers         http.Header
	FollowRedirects bool
	CAFile          string
	// Secrets lists resolved credential values for the redactor.
	Secrets []string
}

// ErrCredentialNotAllowed reports an env reference outside the allowlist.
var ErrCredentialNotAllowed = errors.New("credential reference is not allowed")

// Resolve reads env-backed headers. When allowed is non-nil only the named
// variables may be read (server mode); the CLI passes nil.
func (t Target) Resolve(name string, env EnvLookup, allowed map[string]bool) (*ResolvedTarget, error) {
	if err := t.Validate(name); err != nil {
		return nil, err
	}
	u, _ := url.Parse(t.BaseURL)
	rt := &ResolvedTarget{
		Name:            name,
		BaseURL:         u,
		Headers:         http.Header{},
		FollowRedirects: t.FollowRedirects,
		CAFile:          t.CAFile,
	}
	for _, hname := range sortedKeys(t.Headers) {
		hv := t.Headers[hname]
		if hv.Env == "" {
			rt.Headers.Set(hname, hv.Value)
			continue
		}
		if allowed != nil && !allowed[hv.Env] {
			return nil, fmt.Errorf("target %s: header %s: %w: %s", name, hname, ErrCredentialNotAllowed, hv.Env)
		}
		v, ok := env(hv.Env)
		if !ok || v == "" {
			return nil, fmt.Errorf("target %s: header %s: environment variable %s is not set", name, hname, hv.Env)
		}
		if strings.ContainsAny(v, "\r\n\x00") {
			return nil, fmt.Errorf("target %s: header %s: environment variable %s contains control characters", name, hname, hv.Env)
		}
		rt.Headers.Set(hname, hv.Prefix+v)
		rt.Secrets = append(rt.Secrets, v)
	}
	return rt, nil
}

// Origin returns scheme://host[:port] of the target, used to decide
// whether credentials may accompany a request.
func (rt *ResolvedTarget) Origin() string {
	return rt.BaseURL.Scheme + "://" + rt.BaseURL.Host
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
