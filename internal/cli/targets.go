package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/happydev123/backendlab/internal/config"
)

// baseURLFlag collects repeated --base-url name=URL overrides. An override
// replaces the configured base URL of a target (keeping its headers) or
// defines a header-less target when none is configured.
type baseURLFlag map[string]string

func (b baseURLFlag) String() string {
	parts := make([]string, 0, len(b))
	for k, v := range b {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func (b baseURLFlag) Set(v string) error {
	name, url, ok := strings.Cut(v, "=")
	if !ok || name == "" || url == "" {
		return fmt.Errorf("want name=URL, got %q", v)
	}
	if _, dup := b[name]; dup {
		return fmt.Errorf("target %q given twice", name)
	}
	b[name] = url
	return nil
}

// resolveTarget returns the named target with credentials read from the
// environment. The CLI places no destination restrictions on targets:
// the local user chooses them.
func (a *App) resolveTarget(cfg *config.CLI, name string, overrides baseURLFlag) (*config.ResolvedTarget, error) {
	t, ok := cfg.Targets[name]
	if url, has := overrides[name]; has {
		t.BaseURL = url
		ok = true
	}
	if !ok {
		known := make([]string, 0, len(cfg.Targets))
		for k := range cfg.Targets {
			known = append(known, k)
		}
		sort.Strings(known)
		hint := "none configured"
		if len(known) > 0 {
			hint = "configured: " + strings.Join(known, ", ")
		}
		return nil, usagef("unknown target %q (%s); add it to the config file or pass --base-url %s=URL", name, hint, name)
	}
	env := a.Env
	if env == nil {
		env = func(string) (string, bool) { return "", false }
	}
	return t.Resolve(name, env, nil)
}
