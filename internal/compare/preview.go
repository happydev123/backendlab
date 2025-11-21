package compare

import (
	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/redact"
)

// previewer renders bounded, redacted value previews.
type previewer struct {
	r   *redact.Redactor
	max int
}

// value previews the JSON value found at p. Values at redacted paths, or
// under a sensitive key, are shown as the redaction marker; strings inside
// other values have registered secrets scrubbed.
func (pv previewer) value(p jsonptr.Pointer, v any) string {
	if pv.r.PathRedacted(p) {
		return redact.Marker
	}
	return jsonutil.Preview(pv.r.JSON(v), pv.max)
}

// header previews a header value, hiding sensitive headers entirely.
func (pv previewer) header(name, value string, present bool) string {
	if !present {
		return absent
	}
	return jsonutil.Truncate(pv.r.Header(name, value), pv.max)
}

// text previews free text with secrets scrubbed.
func (pv previewer) text(s string) string {
	return jsonutil.Truncate(pv.r.String(s), pv.max)
}
