package replay

import (
	"testing"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/suite"
)

// parseSuite parses YAML suite text with default limits.
func parseSuite(t testing.TB, text string) *suite.Suite {
	t.Helper()
	s, err := suite.Parse([]byte(text), limits.Default())
	if err != nil {
		t.Fatalf("parse suite: %v", err)
	}
	return s
}

// loaded wraps a parsed suite as if loaded from dir.
func loaded(t testing.TB, text string) *suite.Loaded {
	t.Helper()
	return &suite.Loaded{
		Suite:  parseSuite(t, text),
		Source: suite.Source{Path: "suite.yaml", Dir: t.TempDir(), SHA256: suite.Digest([]byte(text)), Size: int64(len(text))},
	}
}

// resolveTarget resolves a target definition with env as the environment.
func resolveTarget(t testing.TB, name string, tgt config.Target, env map[string]string) *config.ResolvedTarget {
	t.Helper()
	rt, err := tgt.Resolve(name, func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}, nil)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	return rt
}
