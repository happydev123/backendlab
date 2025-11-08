package report

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/happydev123/backendlab/internal/finding"
)

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiDim    = "\x1b[2m"
)

// RenderText writes a human-readable report. Every value taken from input
// or observed responses passes through TerminalSafe.
func RenderText(w io.Writer, r *Report, opts Options) error {
	bw := bufio.NewWriter(w)
	paint := func(color, s string) string {
		if !opts.Color {
			return s
		}
		return color + s + ansiReset
	}

	fmt.Fprintf(bw, "%s %s (%s %s)\n", r.Tool.Name, TerminalSafe(r.Command), TerminalSafe(r.Tool.Name), TerminalSafe(r.Tool.Version))
	fmt.Fprintf(bw, "Status: %s\n", paint(statusColor(r.Status), strings.ToUpper(string(r.Status))))
	if r.Error != "" {
		fmt.Fprintf(bw, "Error: %s\n", TerminalSafe(r.Error))
	}
	for _, in := range r.Inputs {
		digest := ""
		if in.SHA256 != "" {
			digest = fmt.Sprintf(" (sha256 %s)", shortDigest(in.SHA256))
		}
		fmt.Fprintf(bw, "Input: %s %s%s\n", TerminalSafe(in.Kind), TerminalSafe(in.Name), digest)
	}
	if len(r.Targets) > 0 {
		fmt.Fprintf(bw, "Targets: %s\n", TerminalSafe(strings.Join(r.Targets, ", ")))
	}
	if r.Timing != nil {
		fmt.Fprintf(bw, "Duration: %s\n", formatMS(r.Timing.DurationMS))
	}

	if len(r.Cases) > 0 {
		fmt.Fprintf(bw, "\nCases: %d total, %s\n", r.Summary.Cases, caseCounts(r.Summary.CasesByStatus))
		width := 0
		for _, c := range r.Cases {
			if n := len(TerminalSafe(c.ID)); n > width {
				width = n
			}
		}
		for _, c := range r.Cases {
			label := fmt.Sprintf("%-5s", caseLabel(c.Status))
			line := fmt.Sprintf("  %s %-*s  %s", paint(caseColor(c.Status), label), width, TerminalSafe(c.ID), observationLine(c.Observations))
			fmt.Fprintln(bw, strings.TrimRight(line, " "))
		}
	}

	fmt.Fprintf(bw, "\nFindings: %d (%d failing)\n", r.Summary.Findings.Total, r.Summary.Findings.Failing)
	if len(r.Findings) == 0 {
		fmt.Fprintln(bw, "  none")
	}
	for _, f := range r.Findings {
		writeTextFinding(bw, f, paint)
	}
	if r.Dropped > 0 {
		fmt.Fprintf(bw, "  ... %d further findings omitted (limit reached)\n", r.Dropped)
	}

	if len(r.Limitations) > 0 {
		fmt.Fprintln(bw, "\nLimitations:")
		for _, l := range r.Limitations {
			fmt.Fprintf(bw, "  - %s\n", TerminalSafe(l))
		}
	}
	return bw.Flush()
}

func writeTextFinding(w io.Writer, f finding.Finding, paint func(string, string) string) {
	sev := paint(severityColor(f.Severity), "["+string(f.Severity)+"]")
	head := fmt.Sprintf("  %s %s %s", sev, f.Kind, TerminalSafe(f.Code))
	if f.CaseID != "" {
		head += " case=" + TerminalSafe(f.CaseID)
	}
	if f.Location != "" {
		head += " at " + TerminalSafe(f.Location)
	}
	fmt.Fprintln(w, head)
	fmt.Fprintf(w, "      %s\n", TerminalSafe(f.Message))
	if f.Expected != "" {
		fmt.Fprintf(w, "      expected: %s\n", TerminalSafe(f.Expected))
	}
	if f.Observed != "" {
		fmt.Fprintf(w, "      observed: %s\n", TerminalSafe(f.Observed))
	}
	if len(f.Details) > 0 {
		keys := make([]string, 0, len(f.Details))
		for k := range f.Details {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "      %s: %s\n", TerminalSafe(k), TerminalSafe(f.Details[k]))
		}
	}
}

func observationLine(obs []Observation) string {
	parts := make([]string, 0, len(obs))
	for _, o := range obs {
		p := TerminalSafe(o.Target)
		if o.Error != "" {
			p += " error: " + TerminalSafe(o.Error)
			parts = append(parts, p)
			continue
		}
		if o.StatusCode != 0 {
			p += fmt.Sprintf(" %d", o.StatusCode)
		}
		if o.DurationMS != nil {
			p += " " + formatMS(*o.DurationMS)
		}
		if o.Events > 0 {
			p += fmt.Sprintf(" %d events", o.Events)
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " | ")
}

var caseOrder = []CaseStatus{CasePassed, CaseFailed, CaseError, CaseInconclusive, CaseSkipped, CaseCancelled}

func caseCounts(m map[CaseStatus]int) string {
	parts := []string{}
	for _, s := range caseOrder {
		if m[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", m[s], s))
		}
	}
	if len(parts) == 0 {
		return "none run"
	}
	return strings.Join(parts, ", ")
}

func caseLabel(s CaseStatus) string {
	switch s {
	case CasePassed:
		return "PASS"
	case CaseFailed:
		return "FAIL"
	case CaseError:
		return "ERROR"
	case CaseInconclusive:
		return "INCON"
	case CaseSkipped:
		return "SKIP"
	case CaseCancelled:
		return "CANCL"
	}
	return strings.ToUpper(string(s))
}

func statusColor(s Status) string {
	switch s {
	case StatusPassed:
		return ansiGreen
	case StatusFailed, StatusError:
		return ansiRed
	}
	return ansiYellow
}

func caseColor(s CaseStatus) string {
	switch s {
	case CasePassed:
		return ansiGreen
	case CaseFailed, CaseError:
		return ansiRed
	case CaseInconclusive:
		return ansiYellow
	}
	return ansiDim
}

func severityColor(s finding.Severity) string {
	switch s {
	case finding.SeverityError:
		return ansiRed
	case finding.SeverityWarning:
		return ansiYellow
	}
	return ansiDim
}

func formatMS(ms float64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.2fs", ms/1000)
	}
	return fmt.Sprintf("%.1fms", ms)
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}
