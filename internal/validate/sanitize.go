package validate

import (
	"strconv"
	"strings"

	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/redact"
)

// sanitizer turns document values into text that is safe to place in a
// finding: values at redacted paths become redact.Marker, nested sensitive
// keys and registered secrets are scrubbed, and everything is bounded to
// the preview length.
type sanitizer struct {
	red *redact.Redactor
	max int
}

// newSanitizer applies defaults: the built-in redaction rules and the
// default preview length.
func newSanitizer(red *redact.Redactor, max int) sanitizer {
	if red == nil {
		red = redact.Default()
	}
	if max <= 0 {
		max = limits.Default().MaxValuePreview
	}
	return sanitizer{red: red, max: max}
}

// redacted reports whether the value at p must not be shown.
func (s sanitizer) redacted(p jsonptr.Pointer) bool {
	return s.red.PathRedacted(p)
}

// value renders the decoded value found at p as compact JSON.
func (s sanitizer) value(p jsonptr.Pointer, v any) string {
	if s.redacted(p) {
		return redact.Marker
	}
	return jsonutil.Preview(s.red.JSON(v), s.max)
}

// raw renders the textual form of a scalar found at p (for example the
// decimal "103.30" as written), without JSON quoting.
func (s sanitizer) raw(p jsonptr.Pointer, text string) string {
	if s.redacted(p) {
		return redact.Marker
	}
	return s.text(text)
}

// text scrubs registered secrets from free text and bounds its length.
func (s sanitizer) text(str string) string {
	return jsonutil.Truncate(s.red.String(str), s.max)
}

// list renders up to max items of a list of strings for Expected text,
// noting how many were left out.
func (s sanitizer) list(items []string) string {
	var b strings.Builder
	for i, it := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		if b.Len()+len(it) > s.max {
			b.WriteString("... (")
			b.WriteString(strconv.Itoa(len(items) - i))
			b.WriteString(" more)")
			break
		}
		b.WriteString(it)
	}
	return s.red.String(b.String())
}

// aType names the JSON type of v with its article: "an object", "a string".
func aType(v any) string {
	name := jsonutil.TypeName(v)
	switch name {
	case "object", "array":
		return "an " + name
	}
	return "a " + name
}
