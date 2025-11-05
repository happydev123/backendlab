// Package jsonptr implements RFC 6901 JSON Pointers over values decoded by
// encoding/json (map[string]any, []any, json.Number, string, bool, nil),
// plus a small wildcard pattern form used for exclusions and redaction.
package jsonptr

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MaxSegments bounds pointer depth.
const MaxSegments = 64

// Pointer is a parsed JSON Pointer. The empty pointer refers to the whole
// document.
type Pointer struct {
	segments []string
}

// ErrSyntax reports a malformed pointer.
var ErrSyntax = errors.New("invalid JSON pointer")

// Parse parses s. It must be empty or start with "/".
func Parse(s string) (Pointer, error) {
	if s == "" {
		return Pointer{}, nil
	}
	if s[0] != '/' {
		return Pointer{}, fmt.Errorf("%w %q: must start with '/'", ErrSyntax, s)
	}
	raw := strings.Split(s[1:], "/")
	if len(raw) > MaxSegments {
		return Pointer{}, fmt.Errorf("%w %q: more than %d segments", ErrSyntax, s, MaxSegments)
	}
	segs := make([]string, len(raw))
	for i, r := range raw {
		seg, err := unescape(r)
		if err != nil {
			return Pointer{}, fmt.Errorf("%w %q: %v", ErrSyntax, s, err)
		}
		segs[i] = seg
	}
	return Pointer{segments: segs}, nil
}

// MustParse parses s and panics on error. Intended for constants in tests.
func MustParse(s string) Pointer {
	p, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return p
}

func unescape(s string) (string, error) {
	if !strings.Contains(s, "~") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '~' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			return "", errors.New("dangling '~'")
		}
		switch s[i+1] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", fmt.Errorf("invalid escape '~%c'", s[i+1])
		}
		i++
	}
	return b.String(), nil
}

// Escape escapes one reference token.
func Escape(token string) string {
	if !strings.ContainsAny(token, "~/") {
		return token
	}
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}

// Segments returns a copy of the unescaped reference tokens.
func (p Pointer) Segments() []string { return append([]string(nil), p.segments...) }

// Len returns the number of reference tokens.
func (p Pointer) Len() int { return len(p.segments) }

// IsRoot reports whether p refers to the whole document.
func (p Pointer) IsRoot() bool { return len(p.segments) == 0 }

// String formats p back into pointer syntax.
func (p Pointer) String() string {
	if len(p.segments) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range p.segments {
		b.WriteByte('/')
		b.WriteString(Escape(s))
	}
	return b.String()
}

// Append returns a new pointer with token appended.
func (p Pointer) Append(token string) Pointer {
	segs := make([]string, len(p.segments), len(p.segments)+1)
	copy(segs, p.segments)
	return Pointer{segments: append(segs, token)}
}

// AppendIndex returns a new pointer with an array index appended.
func (p Pointer) AppendIndex(i int) Pointer { return p.Append(strconv.Itoa(i)) }

// Get resolves p against doc. found is false when any segment does not
// exist; err is non-nil when a segment cannot apply to an array (a token
// that is not a valid index).
func (p Pointer) Get(doc any) (value any, found bool, err error) {
	cur := doc
	for i, seg := range p.segments {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[seg]
			if !ok {
				return nil, false, nil
			}
			cur = next
		case []any:
			idx, perr := arrayIndex(seg)
			if perr != nil {
				return nil, false, fmt.Errorf("segment %d of %q: %v", i, p.String(), perr)
			}
			if idx >= len(v) {
				return nil, false, nil
			}
			cur = v[idx]
		default:
			return nil, false, nil
		}
	}
	return cur, true, nil
}

func arrayIndex(seg string) (int, error) {
	if seg == "" || (len(seg) > 1 && seg[0] == '0') {
		return 0, fmt.Errorf("invalid array index %q", seg)
	}
	n, err := strconv.Atoi(seg)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid array index %q", seg)
	}
	return n, nil
}
