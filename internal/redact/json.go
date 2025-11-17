package redact

import (
	"github.com/happydev123/backendlab/internal/jsonptr"
)

// JSON returns a deep copy of a decoded JSON value with sensitive keys and
// configured paths replaced by Marker and registered secrets scrubbed from
// strings. Values nested deeper than the walk bound are replaced by Marker
// rather than copied unchecked.
func (r *Redactor) JSON(v any) any {
	return r.walk(v, jsonptr.Pointer{}, 0)
}

func (r *Redactor) walk(v any, at jsonptr.Pointer, depth int) any {
	if depth > maxDepth {
		return Marker
	}
	if !at.IsRoot() && r.paths.Match(at) {
		return Marker
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			if r.SensitiveKey(k) {
				out[k] = Marker
				continue
			}
			out[k] = r.walk(child, at.Append(k), depth+1)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			out[i] = r.walk(child, at.AppendIndex(i), depth+1)
		}
		return out
	case string:
		return r.String(t)
	default:
		return v
	}
}

// PathRedacted reports whether the value at p would be replaced, either by
// a configured path or because its final key is sensitive. Comparison code
// uses this to avoid printing sensitive values in differences.
func (r *Redactor) PathRedacted(p jsonptr.Pointer) bool {
	if p.IsRoot() {
		return false
	}
	if r.paths.MatchPrefix(p) {
		return true
	}
	for _, seg := range p.Segments() {
		if r.SensitiveKey(seg) {
			return true
		}
	}
	return false
}
