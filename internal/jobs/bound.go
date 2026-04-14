package jobs

import (
	"encoding/json"
	"fmt"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/report"
)

// Bounds on persisted case results and error text that are not covered by
// limits.Limits.
const (
	maxCaseIDLen        = 200
	maxObservations     = 64
	maxErrorTextBytes   = 4096
	defaultMaxBodyBytes = 64 << 10
)

// Bounds controls how results are sanitized and bounded before they are
// written to PostgreSQL.
type Bounds struct {
	// MaxReportBytes bounds the stored JSON report (limits.max_report_bytes).
	MaxReportBytes int64
	// MaxCaseFindings bounds the findings stored per case; the rest are
	// counted in CaseRecord.FindingsDropped.
	MaxCaseFindings int
	// RetainBodies enables storing response bodies with case results
	// (retention.retain_raw_bodies). Off by default.
	RetainBodies bool
	// MaxBodyBytes bounds each retained body (retention.raw_body_max_bytes).
	MaxBodyBytes int64
	// Redactor scrubs registered secrets from error text and bodies. Nil
	// means redact.Default().
	Redactor *redact.Redactor
}

func (b Bounds) withDefaults() Bounds {
	d := limits.Default()
	if b.MaxReportBytes <= 0 {
		b.MaxReportBytes = d.MaxReportBytes
	}
	if b.MaxCaseFindings <= 0 {
		b.MaxCaseFindings = d.MaxFindings
	}
	if b.MaxBodyBytes <= 0 {
		b.MaxBodyBytes = defaultMaxBodyBytes
	}
	if b.Redactor == nil {
		b.Redactor = redact.Default()
	}
	return b
}

// CaseRecord is a validated, bounded case result ready to persist.
type CaseRecord struct {
	CaseID          string
	Index           int
	Status          report.CaseStatus
	Findings        []finding.Finding
	FindingsDropped int
	Observations    []report.Observation
	Bodies          map[string]string
}

// CaseRecord validates u and applies the bounds: findings are sorted and
// capped, observation errors and bodies are redacted, and bodies are kept
// only when retention is enabled, cut to MaxBodyBytes with an explicit
// marker.
func (b Bounds) CaseRecord(u CaseUpdate) (CaseRecord, error) {
	b = b.withDefaults()
	if u.CaseID == "" || len(u.CaseID) > maxCaseIDLen {
		return CaseRecord{}, fmt.Errorf("case id must be 1-%d bytes", maxCaseIDLen)
	}
	if u.Index < 0 {
		return CaseRecord{}, fmt.Errorf("case %s: negative index", u.CaseID)
	}
	status := report.CaseStatus(u.Status)
	switch status {
	case report.CasePassed, report.CaseFailed, report.CaseError, report.CaseSkipped,
		report.CaseInconclusive, report.CaseCancelled:
	default:
		return CaseRecord{}, fmt.Errorf("case %s: unknown status %q", u.CaseID, u.Status)
	}
	if len(u.Observations) > maxObservations {
		return CaseRecord{}, fmt.Errorf("case %s: %d observations exceed the bound of %d", u.CaseID, len(u.Observations), maxObservations)
	}
	rec := CaseRecord{CaseID: u.CaseID, Index: u.Index, Status: status}

	rec.Findings = append([]finding.Finding{}, u.Findings...)
	finding.Sort(rec.Findings)
	if len(rec.Findings) > b.MaxCaseFindings {
		rec.FindingsDropped = len(rec.Findings) - b.MaxCaseFindings
		rec.Findings = rec.Findings[:b.MaxCaseFindings]
	}
	rec.Observations = make([]report.Observation, len(u.Observations))
	for i, o := range u.Observations {
		o.Error = b.Redactor.String(o.Error)
		rec.Observations[i] = o
	}
	if b.RetainBodies && len(u.Body) > 0 {
		rec.Bodies = make(map[string]string, len(u.Body))
		for target, body := range u.Body {
			rec.Bodies[target] = truncateBody(b.Redactor.String(body), b.MaxBodyBytes)
		}
	}
	return rec, nil
}

func truncateBody(s string, max int64) string {
	if int64(len(s)) <= max {
		return s
	}
	cut := jsonutil.Truncate(s, int(max))
	return fmt.Sprintf("%s\n[body truncated: %d of %d bytes retained]", cut[:len(cut)-3], len(cut)-3, len(s))
}

// sanitizeError redacts and bounds error text stored with a run.
func (b Bounds) sanitizeError(s string) string {
	b = b.withDefaults()
	return jsonutil.Truncate(b.Redactor.String(s), maxErrorTextBytes)
}

// encodedReport is a report prepared for storage.
type encodedReport struct {
	Data      []byte // nil when even the reduced report does not fit
	Summary   []byte
	Truncated bool
	Note      string // explains truncation; empty otherwise
}

// encodeReport marshals rep for storage. A report larger than
// MaxReportBytes is replaced by a copy without cases and findings (the
// summary, status, error and limitations remain, and per-case results are
// still in case_results). If that is still too large no report is stored.
// Either way Note explains what happened; nothing is dropped silently.
func (b Bounds) encodeReport(rep *report.Report) (encodedReport, error) {
	b = b.withDefaults()
	summary, err := json.Marshal(rep.Summary)
	if err != nil {
		return encodedReport{}, fmt.Errorf("encode report summary: %w", err)
	}
	data, err := json.Marshal(rep)
	if err != nil {
		return encodedReport{}, fmt.Errorf("encode report: %w", err)
	}
	out := encodedReport{Data: data, Summary: summary}
	if int64(len(data)) <= b.MaxReportBytes {
		return out, nil
	}
	full := len(data)
	reduced := *rep
	reduced.Cases = nil
	reduced.Findings = []finding.Finding{}
	reduced.Dropped = rep.Dropped + len(rep.Findings)
	reduced.Limitations = append(append([]string(nil), rep.Limitations...),
		fmt.Sprintf("stored report reduced to a summary: the full report was %d bytes and the limit is %d bytes; per-case results are stored separately", full, b.MaxReportBytes))
	out.Truncated = true
	if data, err = json.Marshal(&reduced); err == nil && int64(len(data)) <= b.MaxReportBytes {
		out.Data = data
		out.Note = fmt.Sprintf("report exceeded max_report_bytes (%d > %d); stored a summary without cases and findings", full, b.MaxReportBytes)
		return out, nil
	}
	out.Data = nil
	out.Note = fmt.Sprintf("report exceeded max_report_bytes (%d > %d) even without cases and findings; no report stored", full, b.MaxReportBytes)
	return out, nil
}
