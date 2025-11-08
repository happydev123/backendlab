package report

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/happydev123/backendlab/internal/finding"
)

// RenderMarkdown writes the report as GitHub-flavored Markdown. Observed
// values are escaped so they cannot inject links, HTML or table cells.
func RenderMarkdown(w io.Writer, r *Report) error {
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "# BackendLab report: %s\n\n", MarkdownSafe(r.Command))
	fmt.Fprintf(bw, "- Status: **%s**\n", MarkdownSafe(string(r.Status)))
	fmt.Fprintf(bw, "- Tool: %s %s\n", MarkdownSafe(r.Tool.Name), MarkdownCode(r.Tool.Version))
	fmt.Fprintf(bw, "- Schema: %s\n", MarkdownCode(r.Schema))
	if r.Error != "" {
		fmt.Fprintf(bw, "- Error: %s\n", MarkdownSafe(r.Error))
	}
	for _, in := range r.Inputs {
		line := fmt.Sprintf("- Input (%s): %s", MarkdownSafe(in.Kind), MarkdownCode(in.Name))
		if in.SHA256 != "" {
			line += " sha256 " + MarkdownCode(shortDigest(in.SHA256))
		}
		fmt.Fprintln(bw, line)
	}
	if len(r.Targets) > 0 {
		fmt.Fprintf(bw, "- Targets: %s\n", MarkdownSafe(strings.Join(r.Targets, ", ")))
	}
	if r.Timing != nil {
		fmt.Fprintf(bw, "- Duration: %s\n", formatMS(r.Timing.DurationMS))
	}

	fmt.Fprintf(bw, "\n## Summary\n\n")
	fmt.Fprintf(bw, "| Measure | Count |\n|---|---|\n")
	if len(r.Cases) > 0 {
		fmt.Fprintf(bw, "| Cases | %d |\n", r.Summary.Cases)
		for _, s := range caseOrder {
			if n := r.Summary.CasesByStatus[s]; n > 0 {
				fmt.Fprintf(bw, "| Cases %s | %d |\n", s, n)
			}
		}
	}
	fmt.Fprintf(bw, "| Findings | %d |\n| Failing findings | %d |\n", r.Summary.Findings.Total, r.Summary.Findings.Failing)
	kinds := make([]string, 0, len(r.Summary.Findings.ByKind))
	for k, n := range r.Summary.Findings.ByKind {
		if n > 0 {
			kinds = append(kinds, string(k))
		}
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Fprintf(bw, "| %s | %d |\n", MarkdownSafe(k), r.Summary.Findings.ByKind[finding.Kind(k)])
	}

	if len(r.Cases) > 0 {
		fmt.Fprintf(bw, "\n## Cases\n\n| Case | Status | Observations |\n|---|---|---|\n")
		for _, c := range r.Cases {
			fmt.Fprintf(bw, "| %s | %s | %s |\n", MarkdownCode(c.ID), MarkdownSafe(string(c.Status)), MarkdownSafe(observationLine(c.Observations)))
		}
	}

	fmt.Fprintf(bw, "\n## Findings\n\n")
	if len(r.Findings) == 0 {
		fmt.Fprintln(bw, "No findings.")
	} else {
		fmt.Fprintf(bw, "| Severity | Kind | Code | Case | Location | Explanation |\n|---|---|---|---|---|---|\n")
		for _, f := range r.Findings {
			fmt.Fprintf(bw, "| %s | %s | %s | %s | %s | %s |\n",
				MarkdownSafe(string(f.Severity)), MarkdownSafe(string(f.Kind)), MarkdownCode(f.Code),
				optionalCode(f.CaseID), optionalCode(f.Location), markdownExplanation(f))
		}
	}
	if r.Dropped > 0 {
		fmt.Fprintf(bw, "\n%d further findings were omitted after the configured limit.\n", r.Dropped)
	}

	if len(r.Limitations) > 0 {
		fmt.Fprintf(bw, "\n## Limitations\n\n")
		for _, l := range r.Limitations {
			fmt.Fprintf(bw, "- %s\n", MarkdownSafe(l))
		}
	}
	return bw.Flush()
}

func optionalCode(s string) string {
	if s == "" {
		return ""
	}
	return MarkdownCode(s)
}

func markdownExplanation(f finding.Finding) string {
	parts := []string{MarkdownSafe(f.Message)}
	if f.Expected != "" {
		parts = append(parts, "expected "+MarkdownCode(f.Expected))
	}
	if f.Observed != "" {
		parts = append(parts, "observed "+MarkdownCode(f.Observed))
	}
	return strings.Join(parts, "; ")
}
