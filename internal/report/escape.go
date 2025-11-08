package report

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// TerminalSafe makes s safe to print to a terminal on one line: control
// characters (including ESC, so no ANSI sequences survive), invalid UTF-8
// and Unicode bidirectional overrides are replaced by visible escapes.
func TerminalSafe(s string) string {
	if isPlain(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			fmt.Fprintf(&b, `\x%02x`, r)
		case isBidi(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

func isPlain(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c >= 0x7f {
			return false
		}
	}
	return true
}

func isBidi(r rune) bool {
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x200e || r == 0x200f
}

// markdownSpecial lists characters escaped in Markdown text so that
// observed values cannot create links, emphasis, tables or raw HTML.
const markdownSpecial = "\\`*_{}[]()<>#+-.!|~"

// MarkdownSafe escapes s for use inside Markdown text or a table cell.
// Control characters are escaped first, then Markdown punctuation is
// backslash-escaped.
func MarkdownSafe(s string) string {
	s = TerminalSafe(s)
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(markdownSpecial, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// MarkdownCode renders s as inline code, choosing a backtick fence longer
// than any run inside s.
func MarkdownCode(s string) string {
	s = TerminalSafe(s)
	if s == "" {
		return "` `"
	}
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	pad := ""
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		pad = " "
	}
	// Pipes are escaped so code spans stay inside table cells.
	s = strings.ReplaceAll(s, "|", `\|`)
	return fence + pad + s + pad + fence
}
