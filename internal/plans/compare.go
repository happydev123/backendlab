package plans

import (
	"fmt"
	"strings"

	"github.com/happydev123/backendlab/internal/finding"
)

// Limitation lines included in every comparison.
const (
	limitMatching = "Node matching is heuristic (family, relation or object, parent relationship, child order). EXPLAIN output carries no node identity, so a pair may not be the same executor node; each finding lists the match confidence and reason."
	limitIndexes  = "QueryLens does not recommend indexes or schema changes; findings only describe how the two plans differ."
	limitHTTP     = "A plan difference does not by itself explain an HTTP or application latency regression; confirm with measurements of the affected requests."
	limitTiming   = "Actual times come from single EXPLAIN ANALYZE runs and include instrumentation overhead; repeat the runs before drawing conclusions from time differences."
)

// Comparison is the result of comparing two plans.
type Comparison struct {
	Before  *Plan   `json:"-"`
	After   *Plan   `json:"-"`
	Matches []Match `json:"matches"`
	// Findings are sorted with finding.Sort and bounded by
	// Options.MaxFindings; Dropped counts the findings left out.
	Findings    []finding.Finding `json:"findings"`
	Dropped     int               `json:"dropped,omitempty"`
	Limitations []string          `json:"limitations"`
}

// Compare aligns the two plans and evaluates every rule. Nothing fails by
// default: differences are warnings and observations are info, unless
// Options.FailOn promotes a class of differences to errors. Compare is
// deterministic for the same inputs and options.
func Compare(before, after *Plan, opts Options) *Comparison {
	if before == nil || after == nil || before.Root == nil || after.Root == nil {
		return &Comparison{
			Before: before, After: after, Matches: []Match{}, Findings: []finding.Finding{},
			Limitations: []string{"Comparison needs two parsed plans; nothing was compared."},
		}
	}
	c := newComparer(before, after, opts)
	al := align(before, after)
	c.observePlan()

	byAfter := make(map[*Node]*Match, len(al.matches))
	for i := range al.matches {
		m := al.matches[i]
		byAfter[m.After] = &al.matches[i]
		c.checkScan(m)
		c.checkJoin(m)
		c.checkSpill(m)
		c.checkTemp(m)
		c.observeNode(m)
	}
	if after.Analyzed {
		after.Walk(func(n *Node) bool {
			c.checkEstimate(n, byAfter[n])
			return true
		})
	}
	c.structure(al)

	c.limit(limitMatching)
	if before.Analyzed || after.Analyzed {
		c.limit(limitTiming)
	}
	c.unknownKeys("before", before)
	c.unknownKeys("after", after)
	c.limit(limitIndexes)
	c.limit(limitHTTP)

	c.promote()
	finding.Sort(c.findings)
	cmp := &Comparison{Before: before, After: after, Matches: al.matches, Findings: c.findings, Limitations: c.limitations}
	if cmp.Matches == nil {
		cmp.Matches = []Match{}
	}
	if cmp.Findings == nil {
		cmp.Findings = []finding.Finding{}
	}
	if max := c.opts.MaxFindings; len(cmp.Findings) > max {
		cmp.Dropped = len(cmp.Findings) - max
		cmp.Findings = cmp.Findings[:max]
		cmp.Limitations = append(cmp.Limitations, fmt.Sprintf("%d findings beyond the limit of %d were omitted.", cmp.Dropped, max))
	}
	return cmp
}

// structure reports nodes without counterpart and divergent subtrees, and
// evaluates spill and temp rules on the added nodes.
func (c *comparer) structure(al alignment) {
	for _, u := range al.added {
		msg := fmt.Sprintf("%s has no counterpart in the before plan", label(u.node))
		switch {
		case u.wrapper:
			msg = fmt.Sprintf("%s was added above a matched node", label(u.node))
		case u.size > 1:
			msg = fmt.Sprintf("%s (with %d nodes below it) has no counterpart in the before plan", label(u.node), u.size-1)
		}
		c.add(afterFinding(CodeNodeAdded, finding.KindDifference, finding.SeverityWarning, u.node, msg))
		check := func(n *Node) bool { c.checkSpillAdded(n); c.checkTempAdded(n); return true }
		if u.wrapper {
			check(u.node)
		} else {
			walk(u.node, check)
		}
	}
	for _, u := range al.removed {
		msg := fmt.Sprintf("%s has no counterpart in the after plan", label(u.node))
		switch {
		case u.wrapper:
			msg = fmt.Sprintf("%s was removed from above a matched node", label(u.node))
		case u.size > 1:
			msg = fmt.Sprintf("%s (with %d nodes below it) has no counterpart in the after plan", label(u.node), u.size-1)
		}
		c.add(beforeFinding(CodeNodeRemoved, finding.KindDifference, finding.SeverityWarning, u.node, msg))
	}
	for _, d := range al.divergent {
		c.divergence(d)
	}
	if n := len(al.divergent); n > 0 {
		c.limit(fmt.Sprintf("Plan shapes diverge at %d place(s); nodes below a divergence are reported as added or removed and were not compared.", n))
	}
}

// divergence reports one place where the subtrees could not be aligned.
func (c *comparer) divergence(d divergence) {
	if d.before == nil {
		f := newFinding(CodeStructureChanged, finding.KindDifference, finding.SeverityWarning, "/Plan",
			fmt.Sprintf("Root nodes differ (%s vs %s) and could not be matched; the plans were not compared node by node", label(d.removed[0]), label(d.added[0])))
		f.Expected, f.Observed = label(d.removed[0]), label(d.added[0])
		c.add(f)
		return
	}
	m := Match{BeforePath: d.before.Path, AfterPath: d.after.Path, Before: d.before, After: d.after, Confidence: "-", Reason: "divergent children"}
	f := matchFinding(CodeStructureChanged, finding.KindDifference, finding.SeverityWarning, m,
		fmt.Sprintf("Children of %s changed shape; matching stopped below this node", label(d.after)))
	delete(f.Details, "match_confidence")
	delete(f.Details, "match_reason")
	f.Expected = labels(d.removed)
	f.Observed = labels(d.added)
	c.add(f)
}

func labels(ns []*Node) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, label(n))
	}
	return strings.Join(parts, "; ")
}

// unknownKeys records unrecognized input keys as a limitation.
func (c *comparer) unknownKeys(side string, p *Plan) {
	if p.UnknownKeys == 0 {
		return
	}
	c.limit(fmt.Sprintf("The %s plan contains %d unrecognized keys, which were ignored: %s.", side, p.UnknownKeys, strings.Join(p.UnknownKeyNames, ", ")))
}

// promote raises differences in the configured fail-on classes to errors.
func (c *comparer) promote() {
	for i, f := range c.findings {
		if f.Kind != finding.KindDifference {
			continue
		}
		if class := failClass(f.Code); class != "" && c.opts.FailOn[class] {
			c.findings[i] = f.WithDetail("fail_on", class)
			c.findings[i].Severity = finding.SeverityError
		}
	}
}
