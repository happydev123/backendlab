// Package jsonutil holds the JSON decoding conventions used across
// BackendLab: numbers stay json.Number so that comparisons and money checks
// never round through float64, input size is bounded, and trailing data is
// rejected.
package jsonutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrTooLarge reports input over the configured size bound.
var ErrTooLarge = errors.New("input exceeds size limit")

// Decode parses data into a generic value using json.Number for numbers.
func Decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, errors.New("parse JSON: unexpected data after top-level value")
	}
	return v, nil
}

// DecodeReader reads at most limit bytes from r and decodes them. It fails
// with ErrTooLarge rather than silently truncating.
func DecodeReader(r io.Reader, limit int64) (any, error) {
	data, err := ReadAllLimit(r, limit)
	if err != nil {
		return nil, err
	}
	return Decode(data)
}

// DecodeStrict decodes data into dst, rejecting unknown fields and
// trailing data.
func DecodeStrict(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return errors.New("unexpected data after top-level value")
	}
	return nil
}

// ReadAllLimit reads r fully but fails with ErrTooLarge when more than
// limit bytes are available.
func ReadAllLimit(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("invalid read limit %d", limit)
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w (%d bytes)", ErrTooLarge, limit)
	}
	return data, nil
}

// TypeName returns the JSON type name of a decoded value: object, array,
// string, number, boolean or null.
func TypeName(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case json.Number, float64, int, int64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// Preview renders v as compact JSON truncated to max bytes (on a UTF-8
// boundary) with a trailing marker when shortened.
func Preview(v any, max int) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<%s>", TypeName(v))
	}
	return Truncate(string(data), max)
}

// Truncate shortens s to at most max bytes without splitting a UTF-8
// sequence, appending "..." when it cut anything.
func Truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "..."
}
