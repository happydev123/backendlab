package compare

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/jsonutil"
)

// Bodies are compared structurally when both parse as JSON. Raw JSON text
// is never compared byte for byte to claim equality, because formatting,
// key order and number spelling do not change meaning. When neither body
// parses as JSON (including empty bodies) the bodies are compared as
// opaque bytes by size and SHA-256 digest. When only one side parses, the
// body format changed and nothing further is compared.

// bodyKind is how a body was interpreted.
type bodyKind int

const (
	bodyText bodyKind = iota
	bodyJSON
)

// parsedBody is a raw body and its interpretation.
type parsedBody struct {
	raw   []byte
	kind  bodyKind
	value any
}

func parseBody(raw []byte) parsedBody {
	if len(raw) > 0 {
		if v, err := jsonutil.Decode(raw); err == nil {
			return parsedBody{raw: raw, kind: bodyJSON, value: v}
		}
	}
	return parsedBody{raw: raw, kind: bodyText}
}

func (b parsedBody) describe() string {
	sum := sha256.Sum256(b.raw)
	kind := "text"
	if b.kind == bodyJSON {
		kind = "JSON"
	}
	if len(b.raw) == 0 {
		return "empty"
	}
	return fmt.Sprintf("%s, %d bytes, sha256 %s", kind, len(b.raw), hex.EncodeToString(sum[:])[:16])
}

// Bodies compares two raw response bodies under o.
func Bodies(baseline, candidate []byte, o Options) Result {
	o = o.withDefaults()
	out := newDiffList(o.MaxDifferences)
	w := newWalker(&o, out)
	w.compareBodies(baseline, candidate)
	return resultOf(out, w.within)
}

// compareBodies adds body differences to the walker's list.
func (w *walker) compareBodies(baseline, candidate []byte) {
	b, c := parseBody(baseline), parseBody(candidate)
	switch {
	case b.kind == bodyJSON && c.kind == bodyJSON:
		w.compare(jsonptr.Pointer{}, b.value, c.value, 0)
	case b.kind != c.kind:
		w.out.add(Difference{
			Code:      CodeBodyFormatChanged,
			Location:  LocationBody,
			Message:   "body format changed; one body is JSON and the other is not",
			Baseline:  b.describe(),
			Candidate: c.describe(),
		})
	default:
		if len(b.raw) == len(c.raw) && sha256.Sum256(b.raw) == sha256.Sum256(c.raw) {
			return
		}
		w.out.add(Difference{
			Code:      CodeBodyChanged,
			Location:  LocationBody,
			Message:   "non-JSON body changed (compared by size and SHA-256 digest)",
			Baseline:  b.describe(),
			Candidate: c.describe(),
		})
	}
}
