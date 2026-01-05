package stream

import (
	"bytes"
	"unicode/utf8"
)

// bom is the UTF-8 byte order mark. One leading BOM is stripped.
var bom = []byte{0xEF, 0xBB, 0xBF}

// lineSplitter turns arbitrary chunks into complete lines. Chunk
// boundaries may fall anywhere: inside a line, between the CR and LF of a
// CRLF pair, inside the BOM, or inside a multibyte UTF-8 sequence. UTF-8 is
// validated per completed line, so split sequences are joined first.
type lineSplitter struct {
	maxLine int
	// buf holds the unterminated tail of the previous chunks.
	buf []byte
	// afterCR is set when the previous chunk ended with CR, so a leading
	// LF in the next chunk completes the same CRLF line ending.
	afterCR bool
	// head buffers the first bytes while it is undecided whether they
	// form a BOM; bomDone is set once that is decided.
	head    []byte
	bomDone bool
	// line counts completed lines (1-based numbering for messages).
	line int
}

// split feeds chunk and calls emit for every completed line, without its
// terminator. The slice passed to emit is only valid during the call.
func (s *lineSplitter) split(chunk []byte, emit func(line []byte) *ParseError) *ParseError {
	if !s.bomDone {
		s.head = append(s.head, chunk...)
		if len(s.head) < len(bom) && bytes.HasPrefix(bom, s.head) {
			return nil // still a possible BOM prefix
		}
		s.bomDone = true
		chunk = bytes.TrimPrefix(s.head, bom)
		s.head = nil
	}
	if s.afterCR && len(chunk) > 0 {
		s.afterCR = false
		if chunk[0] == '\n' {
			chunk = chunk[1:]
		}
	}
	for len(chunk) > 0 {
		i := bytes.IndexAny(chunk, "\r\n")
		if i < 0 {
			if err := s.grow(len(chunk)); err != nil {
				return err
			}
			s.buf = append(s.buf, chunk...)
			return nil
		}
		if err := s.grow(i); err != nil {
			return err
		}
		line := chunk[:i]
		if len(s.buf) > 0 {
			s.buf = append(s.buf, line...)
			line = s.buf
		}
		s.line++
		if !utf8.Valid(line) {
			return &ParseError{Termination: TermInvalidData, Code: CodeInvalidUTF8, Line: s.line,
				Message: "line is not valid UTF-8; parsing stopped"}
		}
		if err := emit(line); err != nil {
			return err
		}
		s.buf = s.buf[:0]
		if chunk[i] == '\r' {
			if i+1 == len(chunk) {
				s.afterCR = true
			} else if chunk[i+1] == '\n' {
				i++
			}
		}
		chunk = chunk[i+1:]
	}
	return nil
}

// grow checks that adding n bytes to the pending line stays within
// maxLine, before anything is buffered.
func (s *lineSplitter) grow(n int) *ParseError {
	if len(s.buf)+n > s.maxLine {
		return limitError(CodeLineTooLong, s.line+1, "line exceeds max_line_bytes (%d)", s.maxLine)
	}
	return nil
}

// pending returns the number of unterminated bytes held at EOF. Bytes of an
// undecided BOM prefix count as pending data.
func (s *lineSplitter) pending() int {
	return len(s.buf) + len(s.head)
}
