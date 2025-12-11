package replay

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/redact"
)

// errResponseTooLarge reports a response body over its size bound.
var errResponseTooLarge = errors.New("response body exceeds the size limit")

// drainLimit is how much of an unread body is discarded before closing,
// which lets the connection be reused for small remainders without
// reading large bodies to the end.
const drainLimit = 64 << 10

// readBody reads at most limit bytes and fails with errResponseTooLarge
// rather than truncating.
func readBody(body io.Reader, limit int64) ([]byte, error) {
	data, err := jsonutil.ReadAllLimit(body, limit)
	if errors.Is(err, jsonutil.ErrTooLarge) {
		return nil, errResponseTooLarge
	}
	return data, err
}

// closeBody discards a small remainder and closes the body.
func closeBody(body io.ReadCloser) {
	_, _ = io.CopyN(io.Discard, body, drainLimit)
	_ = body.Close()
}

// limitedReader passes through at most limit bytes and then fails with
// errResponseTooLarge, so stream consumers see an explicit error instead
// of a silent EOF.
type limitedReader struct {
	r         io.Reader
	remaining int64
}

func newLimitedReader(r io.Reader, limit int64) *limitedReader {
	return &limitedReader{r: r, remaining: limit}
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.remaining < 0 {
		return 0, errResponseTooLarge
	}
	if int64(len(p)) > l.remaining+1 {
		p = p[:l.remaining+1]
	}
	n, err := l.r.Read(p)
	if int64(n) > l.remaining {
		n = int(l.remaining)
		l.remaining = -1
		return n, errResponseTooLarge
	}
	l.remaining -= int64(n)
	return n, err
}

// retainedBody renders a body for CaseResult.Bodies: JSON bodies are
// redacted structurally and re-encoded, other bodies have secrets
// scrubbed; the result is truncated to max bytes.
func retainedBody(raw []byte, decoded any, isJSON bool, r *redact.Redactor, max int64) string {
	text := ""
	if isJSON {
		if data, err := json.Marshal(r.JSON(decoded)); err == nil {
			text = string(data)
		}
	} else {
		text = r.String(string(raw))
	}
	return jsonutil.Truncate(text, int(max))
}
