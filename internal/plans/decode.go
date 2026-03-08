package plans

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/report"
)

// maxJSONDepth bounds array/object nesting before decoding. A plan node at
// depth d sits at JSON depth 3+2d, and node contents add a few levels, so
// any plan within MaxNodeDepth fits; the tree decoder then reports depth
// in plan terms.
const maxJSONDepth = 2*MaxNodeDepth + 16

// InputKind is the report.Input kind used for plan files.
const InputKind = "plan"

// ParseFile reads and parses the plan at path. The file may not exceed
// lim.MaxPlanBytes. The returned Input records the path and the SHA-256 of
// the raw bytes for the report.
func ParseFile(path string, lim limits.Limits) (*Plan, report.Input, error) {
	lim = lim.Merge()
	in := report.Input{Kind: InputKind, Name: path}
	f, err := os.Open(path)
	if err != nil {
		return nil, in, fmt.Errorf("open plan: %w", err)
	}
	defer f.Close()
	data, err := jsonutil.ReadAllLimit(f, lim.MaxPlanBytes)
	if err != nil {
		if errors.Is(err, jsonutil.ErrTooLarge) {
			return nil, in, &ParseError{Name: path, Err: fmt.Errorf("%w: file exceeds max_plan_bytes (%d)", jsonutil.ErrTooLarge, lim.MaxPlanBytes)}
		}
		return nil, in, fmt.Errorf("read plan %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	in.SHA256 = hex.EncodeToString(sum[:])
	p, err := Parse(data, path, lim)
	if err != nil {
		return nil, in, err
	}
	return p, in, nil
}

// Parse decodes EXPLAIN (FORMAT JSON) output: either the array PostgreSQL
// prints, holding exactly one statement object with a "Plan" key, or that
// statement object on its own. Other formats, several statements, oversized
// input and plans past the tree bounds are rejected with a *ParseError.
func Parse(data []byte, name string, lim limits.Limits) (*Plan, error) {
	lim = lim.Merge()
	fail := func(loc string, err error) (*Plan, error) {
		return nil, &ParseError{Name: name, Location: loc, Err: err}
	}
	if int64(len(data)) > lim.MaxPlanBytes {
		return fail("", fmt.Errorf("%w: %d bytes exceeds max_plan_bytes (%d)", jsonutil.ErrTooLarge, len(data), lim.MaxPlanBytes))
	}
	data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	if len(data) == 0 {
		return fail("", errors.New("input is empty"))
	}
	if err := sniffFormat(data); err != nil {
		return fail("", err)
	}
	if err := checkJSONDepth(data, maxJSONDepth); err != nil {
		return fail("", err)
	}
	v, err := decodeSingle(data)
	if err != nil {
		return fail("", err)
	}
	stmt, loc, err := statementObject(v)
	if err != nil {
		return fail(loc, err)
	}
	p := &Plan{Name: name}
	b := newTreeBuilder(p)
	if err := decodeStatement(b, stmt); err != nil {
		var pe *ParseError
		if errors.As(err, &pe) {
			pe.Name = name
			return nil, pe
		}
		return fail("", err)
	}
	b.finish()
	return p, nil
}

// sniffFormat recognizes the other EXPLAIN output formats so the error can
// say what to change instead of reporting a JSON syntax error.
func sniffFormat(data []byte) error {
	switch data[0] {
	case '[', '{':
		return nil
	case '<':
		return errors.New("input looks like EXPLAIN (FORMAT XML) output; only FORMAT JSON is supported")
	}
	first := string(data)
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	first = strings.TrimSpace(first)
	switch {
	case strings.HasPrefix(first, "QUERY PLAN"):
		return errors.New("input looks like psql table output; save the plan unaligned (psql -At) with EXPLAIN (FORMAT JSON)")
	case strings.HasPrefix(first, "- Plan:") || strings.HasPrefix(first, "Plan:"):
		return errors.New("input looks like EXPLAIN (FORMAT YAML) output; only FORMAT JSON is supported")
	case strings.Contains(first, "cost=") || strings.Contains(first, "actual time=") || strings.HasPrefix(first, "->"):
		return errors.New("input looks like EXPLAIN text output; only FORMAT JSON is supported")
	}
	return errors.New("input is not JSON; expected EXPLAIN (FORMAT JSON) output")
}

// checkJSONDepth scans the raw bytes and fails when arrays and objects nest
// deeper than max. It runs before decoding so deep input never reaches the
// recursive decoder.
func checkJSONDepth(data []byte, max int) error {
	depth := 0
	inString, escaped := false, false
	for _, c := range data {
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '[', '{':
			depth++
			if depth > max {
				return fmt.Errorf("JSON nesting is deeper than %d levels", max)
			}
		case ']', '}':
			depth--
		}
	}
	return nil
}

// decodeSingle decodes exactly one JSON value with json.Number numbers and
// distinguishes a second JSON document from other trailing data.
func decodeSingle(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	var extra json.RawMessage
	switch err := dec.Decode(&extra); {
	case err == io.EOF:
		return v, nil
	case err == nil:
		return nil, errors.New("input holds more than one JSON document; save one EXPLAIN statement per file")
	default:
		return nil, errors.New("parse JSON: unexpected data after the plan")
	}
}

// statementObject unwraps the top-level value to the statement object and
// returns the JSON Pointer prefix used in error locations.
func statementObject(v any) (map[string]any, string, error) {
	loc := ""
	switch t := v.(type) {
	case []any:
		switch {
		case len(t) == 0:
			return nil, "", errors.New("JSON array is empty; expected one EXPLAIN statement")
		case len(t) > 1:
			return nil, "", fmt.Errorf("input holds %d statements; compare one statement per file", len(t))
		}
		v, loc = t[0], "/0"
	case map[string]any:
	default:
		return nil, "", fmt.Errorf("expected a JSON array or object, got %s", jsonutil.TypeName(v))
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, loc, fmt.Errorf("expected a statement object, got %s", jsonutil.TypeName(v))
	}
	if _, ok := obj["Plan"]; !ok {
		return nil, loc, fmt.Errorf(`not an EXPLAIN JSON plan: no "Plan" key (found %s)`, keyList(obj))
	}
	return obj, loc, nil
}

// keyList renders up to five sorted, truncated keys for an error message.
func keyList(obj map[string]any) string {
	if len(obj) == 0 {
		return "no keys"
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	more := ""
	if len(keys) > 5 {
		more = fmt.Sprintf(" and %d more", len(keys)-5)
		keys = keys[:5]
	}
	for i, k := range keys {
		keys[i] = fmt.Sprintf("%q", jsonutil.Truncate(k, 40))
	}
	return "keys " + strings.Join(keys, ", ") + more
}
