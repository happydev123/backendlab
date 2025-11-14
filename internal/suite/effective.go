package suite

import (
	"time"

	"github.com/happydev123/backendlab/internal/limits"
)

// EffectiveTimeout returns the case timeout, falling back to the suite
// default and then to the configured request timeout.
func (s *Suite) EffectiveTimeout(c *Case, lim limits.Limits) time.Duration {
	if c.Timeout > 0 {
		return c.Timeout.Std()
	}
	if s.Defaults.Timeout > 0 {
		return s.Defaults.Timeout.Std()
	}
	return lim.RequestTimeout
}

// EffectiveHeaders merges default headers with case headers; case values
// win. Header names are compared case-insensitively and returned in
// canonical form.
func (s *Suite) EffectiveHeaders(c *Case) map[string]string {
	out := make(map[string]string, len(s.Defaults.Headers)+len(c.Headers))
	for k, v := range s.Defaults.Headers {
		out[CanonicalHeader(k)] = v
	}
	for k, v := range c.Headers {
		out[CanonicalHeader(k)] = v
	}
	return out
}

// EffectiveCompare merges the suite's default comparison settings with the
// case's. List fields are concatenated (defaults first); a case tolerance
// replaces the default tolerance; SkipStatus is true if either sets it.
func (s *Suite) EffectiveCompare(c *Case) CompareSpec {
	d := s.Defaults.Compare
	out := CompareSpec{
		Ignore:         append(append([]string(nil), d.Ignore...), c.Compare.Ignore...),
		Headers:        append(append([]string(nil), d.Headers...), c.Compare.Headers...),
		PathTolerances: append(append([]PathTolerance(nil), c.Compare.PathTolerances...), d.PathTolerances...),
		Unordered:      append(append([]UnorderedArray(nil), c.Compare.Unordered...), d.Unordered...),
		Tolerance:      d.Tolerance,
		SkipStatus:     d.SkipStatus || c.Compare.SkipStatus,
	}
	if c.Compare.Tolerance != nil {
		out.Tolerance = c.Compare.Tolerance
	}
	return out
}
