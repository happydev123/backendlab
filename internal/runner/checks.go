package runner

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/replay"
	"github.com/happydev123/backendlab/internal/schema"
	"github.com/happydev123/backendlab/internal/stream"
	"github.com/happydev123/backendlab/internal/suite"
	"github.com/happydev123/backendlab/internal/validate"
)

// checkers holds per-case validators and stream expectations compiled
// before execution. It is read-only once built, so concurrent cases can
// share it.
type checkers struct {
	lim        limits.Limits
	red        *redact.Redactor
	validators map[string]*validate.Validator
	streams    map[string]stream.Expectations
}

func prepare(l *suite.Loaded, lim limits.Limits, red *redact.Redactor, confine bool) (*checkers, error) {
	ch := &checkers{
		lim:        lim,
		red:        red,
		validators: map[string]*validate.Validator{},
		streams:    map[string]stream.Expectations{},
	}
	dir := l.Source.Dir
	// Validators are shared between cases that name the same files.
	byFiles := map[string]*validate.Validator{}
	for i := range l.Suite.Cases {
		c := &l.Suite.Cases[i]
		if c.Expect.Schema != "" || c.Expect.Rules != "" {
			key := c.Expect.Schema + "\x00" + c.Expect.Rules
			v, ok := byFiles[key]
			if !ok {
				var err error
				v, err = buildValidator(dir, c.Expect.Schema, c.Expect.Rules, lim, red, confine)
				if err != nil {
					return nil, fmt.Errorf("case %q: %w", c.ID, err)
				}
				byFiles[key] = v
			}
			ch.validators[c.ID] = v
		}
		if c.Stream != nil {
			if c.Stream.Schema != "" {
				if _, err := suite.ResolveFile(dir, c.Stream.Schema, confine); err != nil {
					return nil, fmt.Errorf("case %q: stream.schema: %w", c.ID, err)
				}
			}
			exp, err := stream.ExpectationsFromSpec(c.Stream, dir)
			if err != nil {
				return nil, fmt.Errorf("case %q: %w", c.ID, err)
			}
			ch.streams[c.ID] = exp
		}
	}
	return ch, nil
}

func buildValidator(dir, schemaRef, rulesRef string, lim limits.Limits, red *redact.Redactor, confine bool) (*validate.Validator, error) {
	var sch *schema.Schema
	if schemaRef != "" {
		path, err := suite.ResolveFile(dir, schemaRef, confine)
		if err != nil {
			return nil, fmt.Errorf("expect.schema: %w", err)
		}
		if sch, err = schema.CompileFile(path); err != nil {
			return nil, fmt.Errorf("expect.schema: %w", err)
		}
	}
	var rules *validate.Rules
	if rulesRef != "" {
		path, err := suite.ResolveFile(dir, rulesRef, confine)
		if err != nil {
			return nil, fmt.Errorf("expect.rules: %w", err)
		}
		if rules, err = validate.LoadRules(path); err != nil {
			return nil, fmt.Errorf("expect.rules: %w", err)
		}
	}
	return validate.New(rules, sch, validate.Options{
		Redactor:        red,
		MaxFindings:     lim.MaxFindings,
		MaxValuePreview: lim.MaxValuePreview,
		ConfineFiles:    confine,
	})
}

// checkBody validates one response body. The case ID is used as the
// document ID so findings line up with the case in reports.
func (ch *checkers) checkBody(c *suite.Case, body any) []finding.Finding {
	v, ok := ch.validators[c.ID]
	if !ok {
		return nil
	}
	return v.Document(c.ID, body)
}

// readStream consumes a live SSE body with the case's expectations.
func (ch *checkers) readStream(ctx context.Context, c *suite.Case, body io.Reader, start time.Time, now func() time.Time) replay.StreamOutcome {
	exp := ch.streams[c.ID]
	res := stream.ReadLive(ctx, body, exp, stream.Options{
		Limits:   ch.lim,
		CaseID:   c.ID,
		Now:      now,
		Start:    start,
		Redactor: ch.red,
	})
	return replay.StreamOutcome{
		Events:      res.EventCount,
		FirstEvent:  res.FirstEvent,
		Bytes:       res.Bytes,
		Findings:    res.Findings,
		Termination: string(res.Termination),
	}
}
