package httpapi

import (
	"time"

	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
)

// JSON views of runs. They leave out the suite snapshot, lease details and
// the stored report bytes (downloaded separately).

type suiteJSON struct {
	Name   string `json:"name"`
	File   string `json:"file"`
	SHA256 string `json:"sha256,omitempty"`
}

type runLinks struct {
	Self   string `json:"self"`
	Report string `json:"report"`
}

func linksFor(id string) runLinks {
	return runLinks{Self: "/api/v1/runs/" + id, Report: "/api/v1/runs/" + id + "/report"}
}

type runJSON struct {
	ID              string          `json:"id"`
	Suite           suiteJSON       `json:"suite"`
	Mode            store.Mode      `json:"mode"`
	Targets         []string        `json:"targets"`
	WritesRequested bool            `json:"writes_requested"`
	Mutating        bool            `json:"mutating"`
	Status          store.Status    `json:"status"`
	Attempt         int             `json:"attempt"`
	MaxAttempts     int             `json:"max_attempts"`
	CancelRequested bool            `json:"cancel_requested"`
	CreatedAt       time.Time       `json:"created_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	Error           string          `json:"error,omitempty"`
	Summary         *report.Summary `json:"summary,omitempty"`
	ReportTruncated bool            `json:"report_truncated"`
	ResubmittedFrom string          `json:"resubmitted_from,omitempty"`
	Links           runLinks        `json:"links"`
}

func runView(r *store.Run, resubmittedFrom string) runJSON {
	return runJSON{
		ID:              r.PublicID,
		Suite:           suiteJSON{Name: r.SuiteName, File: r.SuiteFile, SHA256: r.SuiteSHA256},
		Mode:            r.Mode,
		Targets:         nonNil(r.Targets),
		WritesRequested: r.WritesRequested,
		Mutating:        r.Mutating,
		Status:          r.Status,
		Attempt:         r.Attempt,
		MaxAttempts:     r.MaxAttempts,
		CancelRequested: r.CancelRequested(),
		CreatedAt:       r.CreatedAt.UTC(),
		StartedAt:       utc(r.StartedAt),
		FinishedAt:      utc(r.FinishedAt),
		Error:           r.Error,
		Summary:         r.Summary,
		ReportTruncated: r.ReportTruncated,
		ResubmittedFrom: resubmittedFrom,
		Links:           linksFor(r.PublicID),
	}
}

type runListItemJSON struct {
	ID              string          `json:"id"`
	Suite           suiteJSON       `json:"suite"`
	Mode            store.Mode      `json:"mode"`
	Targets         []string        `json:"targets"`
	Mutating        bool            `json:"mutating"`
	Status          store.Status    `json:"status"`
	Attempt         int             `json:"attempt"`
	MaxAttempts     int             `json:"max_attempts"`
	CancelRequested bool            `json:"cancel_requested"`
	CreatedAt       time.Time       `json:"created_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	Error           string          `json:"error,omitempty"`
	Summary         *report.Summary `json:"summary,omitempty"`
	ReportTruncated bool            `json:"report_truncated"`
	Links           runLinks        `json:"links"`
}

func listItemView(s store.RunSummary) runListItemJSON {
	return runListItemJSON{
		ID:              s.PublicID,
		Suite:           suiteJSON{Name: s.SuiteName, File: s.SuiteFile},
		Mode:            s.Mode,
		Targets:         nonNil(s.Targets),
		Mutating:        s.Mutating,
		Status:          s.Status,
		Attempt:         s.Attempt,
		MaxAttempts:     s.MaxAttempts,
		CancelRequested: s.CancelRequested,
		CreatedAt:       s.CreatedAt.UTC(),
		StartedAt:       utc(s.StartedAt),
		FinishedAt:      utc(s.FinishedAt),
		Error:           s.Error,
		Summary:         s.Summary,
		ReportTruncated: s.ReportTruncated,
		Links:           linksFor(s.PublicID),
	}
}

type runListJSON struct {
	Runs   []runListItemJSON `json:"runs"`
	Total  int64             `json:"total"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
}

type caseJSON struct {
	Index           int                  `json:"index"`
	ID              string               `json:"id"`
	Status          report.CaseStatus    `json:"status"`
	Findings        []finding.Finding    `json:"findings"`
	FindingsDropped int                  `json:"findings_dropped,omitempty"`
	Observations    []report.Observation `json:"observations"`
	// Bodies is present only when raw body retention is enabled; the
	// values are already redacted and bounded by the worker.
	Bodies     map[string]string `json:"bodies,omitempty"`
	RecordedAt time.Time         `json:"recorded_at"`
}

type runDetailJSON struct {
	runJSON
	Cases []caseJSON `json:"cases"`
}

func detailView(d *RunDetail) runDetailJSON {
	out := runDetailJSON{runJSON: runView(d.Run, d.ResubmittedFrom), Cases: make([]caseJSON, len(d.Cases))}
	for i, c := range d.Cases {
		out.Cases[i] = caseJSON{
			Index:           c.Index,
			ID:              c.CaseID,
			Status:          c.Status,
			Findings:        append([]finding.Finding{}, c.Findings...),
			FindingsDropped: c.FindingsDropped,
			Observations:    append([]report.Observation{}, c.Observations...),
			Bodies:          c.Bodies,
			RecordedAt:      c.RecordedAt.UTC(),
		}
	}
	return out
}

type catalogEntryJSON struct {
	File          string   `json:"file"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Cases         int      `json:"cases"`
	Mutating      bool     `json:"mutating"`
	MutatingCases []string `json:"mutating_cases,omitempty"`
	SHA256        string   `json:"sha256"`
}

type catalogProblemJSON struct {
	File  string `json:"file"`
	Error string `json:"error"`
}

type suitesJSON struct {
	Suites      []catalogEntryJSON   `json:"suites"`
	Problems    []catalogProblemJSON `json:"problems"`
	Targets     []string             `json:"targets"`
	AllowWrites bool                 `json:"allow_writes"`
}

func catalogView(c *catalog.Catalog, targets []string, allowWrites bool) suitesJSON {
	out := suitesJSON{Suites: []catalogEntryJSON{}, Problems: []catalogProblemJSON{}, Targets: targets, AllowWrites: allowWrites}
	for _, e := range c.List() {
		out.Suites = append(out.Suites, catalogEntryJSON{File: e.File, Name: e.Name, Description: e.Description,
			Cases: e.Cases, Mutating: e.Mutating, MutatingCases: e.MutatingCases, SHA256: e.SHA256})
	}
	for _, p := range c.Problems() {
		out.Problems = append(out.Problems, catalogProblemJSON(p))
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
