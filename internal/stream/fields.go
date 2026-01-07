package stream

import (
	"bytes"
	"strconv"
)

// lineKind classifies one line of an event stream.
type lineKind int

const (
	lineBlank   lineKind = iota // dispatches the pending event
	lineComment                 // starts with ':'
	lineField                   // "name:value" or a bare field name
)

// splitField parses a non-blank, non-comment line into a field name and
// value. A line without ':' is a field name with an empty value. One
// space directly after the first ':' is removed.
func splitField(line []byte) (name, value []byte) {
	i := bytes.IndexByte(line, ':')
	if i < 0 {
		return line, nil
	}
	name, value = line[:i], line[i+1:]
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	return name, value
}

func classify(line []byte) lineKind {
	switch {
	case len(line) == 0:
		return lineBlank
	case line[0] == ':':
		return lineComment
	default:
		return lineField
	}
}

// maxRetryDigits keeps parsed retry values inside int range on every
// platform. Longer digit strings are valid per the standard but are
// reported as invalid here rather than overflowing.
const maxRetryDigits = 9

// parseRetry returns the retry value when v consists only of ASCII digits
// (the standard's rule) and fits maxRetryDigits.
func parseRetry(v []byte) (int, bool) {
	if len(v) == 0 || len(v) > maxRetryDigits {
		return 0, false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(string(v))
	if err != nil {
		return 0, false
	}
	return n, true
}
