package redact

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Header returns value, or Marker when name is sensitive. Non-sensitive
// values still have registered secrets scrubbed.
func (r *Redactor) Header(name, value string) string {
	if r.SensitiveHeader(name) {
		return Marker
	}
	return r.String(value)
}

// Headers returns a redacted copy of h. The input is not modified.
func (r *Redactor) Headers(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for name, vals := range h {
		cp := make([]string, len(vals))
		for i, v := range vals {
			cp[i] = r.Header(name, v)
		}
		out[name] = cp
	}
	return out
}

// HeaderMap returns a redacted, flattened copy of h with canonical names
// and multiple values joined by ", ". Useful for deterministic reports.
func (r *Redactor) HeaderMap(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		vals := make([]string, len(h[name]))
		for i, v := range h[name] {
			vals[i] = r.Header(name, v)
		}
		out[http.CanonicalHeaderKey(name)] = strings.Join(vals, ", ")
	}
	return out
}

// URL formats u without user info and with sensitive query parameters
// replaced by Marker.
func (r *Redactor) URL(u *url.URL) string {
	if u == nil {
		return ""
	}
	cp := *u
	cp.User = nil
	if cp.RawQuery != "" {
		q := cp.Query()
		changed := false
		for k := range q {
			if r.queryKeys[strings.ToLower(k)] {
				q[k] = []string{Marker}
				changed = true
			}
		}
		if changed {
			cp.RawQuery = q.Encode()
		}
	}
	return r.String(cp.String())
}

// scrubURLs redacts user info and sensitive query parameters in any
// absolute http(s) URLs that appear in free text (such as Go's
// *url.Error messages).
func (r *Redactor) scrubURLs(s string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	fields := strings.Fields(s)
	for _, f := range fields {
		trimmed := strings.Trim(f, `"'(),;`)
		if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
			continue
		}
		u, err := url.Parse(trimmed)
		if err != nil {
			continue
		}
		if clean := r.URL(u); clean != trimmed {
			s = strings.ReplaceAll(s, trimmed, clean)
		}
	}
	return s
}
