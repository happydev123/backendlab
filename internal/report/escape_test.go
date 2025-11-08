package report

import "testing"

func TestTerminalSafe(t *testing.T) {
	tests := map[string]string{
		"plain text":           "plain text",
		"a\x1b[2Jb":            `a\x1b[2Jb`,
		"line1\nline2":         `line1\nline2`,
		"tab\there":            `tab\there`,
		"bell\a":               `bell\x07`,
		"bad\xffutf8":          `bad\xffutf8`,
		"rtl\u202eoverride":    "rtl\\u202eoverride",
		"Grüße 日本":             "Grüße 日本",
		"c1\u009bcontrol":      `c1\x9bcontrol`,
		"del\x7f":              `del\x7f`,
		"carriage\rreturn":     `carriage\rreturn`,
		"isolate\u2066x\u2069": "isolate\\u2066x\\u2069",
	}
	for in, want := range tests {
		if got := TerminalSafe(in); got != want {
			t.Errorf("TerminalSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarkdownSafe(t *testing.T) {
	tests := map[string]string{
		"value":            "value",
		"[x](http://evil)": `\[x\]\(http://evil\)`,
		"<script>":         `\<script\>`,
		"a|b":              `a\|b`,
		"*bold* _it_":      `\*bold\* \_it\_`,
		"# heading":        `\# heading`,
		"line\nbreak":      `line\\nbreak`,
	}
	for in, want := range tests {
		if got := MarkdownSafe(in); got != want {
			t.Errorf("MarkdownSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarkdownCode(t *testing.T) {
	tests := map[string]string{
		"":      "` `",
		"abc":   "`abc`",
		"a`b":   "``a`b``",
		"`x`":   "`` `x` ``",
		"a|b":   "`a\\|b`",
		"a``b`": "``` a``b` ```",
	}
	for in, want := range tests {
		if got := MarkdownCode(in); got != want {
			t.Errorf("MarkdownCode(%q) = %q, want %q", in, got, want)
		}
	}
}
