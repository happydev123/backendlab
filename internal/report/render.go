package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Format selects a renderer.
type Format string

const (
	FormatText     Format = "text"
	FormatJSON     Format = "json"
	FormatMarkdown Format = "markdown"
)

// ParseFormat validates a --format value. "md" is accepted for markdown.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "text":
		return FormatText, nil
	case "json":
		return FormatJSON, nil
	case "markdown", "md":
		return FormatMarkdown, nil
	}
	return "", fmt.Errorf("unsupported format %q (want text, json or markdown)", s)
}

// Options adjusts rendering.
type Options struct {
	// Color enables ANSI color in text output. Callers disable it for
	// --no-color, NO_COLOR, or when stdout is not a terminal.
	Color bool
}

// Render writes r in the requested format. The report should be
// finalized first.
func Render(w io.Writer, r *Report, f Format, opts Options) error {
	switch f {
	case FormatJSON:
		return RenderJSON(w, r)
	case FormatMarkdown:
		return RenderMarkdown(w, r)
	case FormatText, "":
		return RenderText(w, r, opts)
	}
	return fmt.Errorf("unsupported format %q", f)
}

// RenderJSON writes the report as indented JSON followed by a newline.
// HTML escaping is disabled so values stay readable; the dashboard applies
// its own context-aware escaping.
func RenderJSON(w io.Writer, r *Report) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// Decode parses a JSON report and checks its schema version.
func Decode(data []byte) (*Report, error) {
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse report: %w", err)
	}
	if r.Schema != SchemaVersion {
		return nil, fmt.Errorf("unsupported report schema %q (want %q)", r.Schema, SchemaVersion)
	}
	return &r, nil
}
