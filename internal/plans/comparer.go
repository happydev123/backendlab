package plans

import (
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonutil"
)

// maxLabelBytes bounds node labels copied into findings. Labels are built
// from relation, index, CTE and function names; condition text (filters,
// index conditions) is never copied because it can contain literal values.
const maxLabelBytes = 160

// beforePrefix marks locations that refer to the before plan. Locations of
// matched nodes use the after plan's path.
const beforePrefix = "before:"

// comparer carries the state of one comparison while the rules run.
type comparer struct {
	before, after *Plan
	opts          Options
	findings      []finding.Finding
	limitations   []string
}

func newComparer(before, after *Plan, opts Options) *comparer {
	return &comparer{before: before, after: after, opts: opts.normalized()}
}

// add records a finding.
func (c *comparer) add(f finding.Finding) { c.findings = append(c.findings, f) }

// limit records a limitation once.
func (c *comparer) limit(text string) {
	for _, l := range c.limitations {
		if l == text {
			return
		}
	}
	c.limitations = append(c.limitations, text)
}

// label returns the bounded readable label of n.
func label(n *Node) string { return jsonutil.Truncate(n.Label(), maxLabelBytes) }

// newFinding builds a plans finding with the given location.
func newFinding(code string, kind finding.Kind, sev finding.Severity, location, msg string) finding.Finding {
	return finding.Finding{
		Module: finding.ModulePlans, Code: code, Kind: kind, Severity: sev,
		Location: location, Message: msg,
	}
}

// matchFinding builds a finding about a matched pair. The location is the
// after node's path; details name both nodes and the match reason so a
// reader can judge how much to trust the pairing.
func matchFinding(code string, kind finding.Kind, sev finding.Severity, m Match, msg string) finding.Finding {
	f := newFinding(code, kind, sev, m.AfterPath, msg)
	f.Details = map[string]string{
		"before_path":      m.BeforePath,
		"before_node":      label(m.Before),
		"after_node":       label(m.After),
		"match_confidence": m.Confidence,
		"match_reason":     jsonutil.Truncate(m.Reason, 2*maxLabelBytes),
	}
	return f
}

// afterFinding builds a finding about a node of the after plan.
func afterFinding(code string, kind finding.Kind, sev finding.Severity, n *Node, msg string) finding.Finding {
	f := newFinding(code, kind, sev, n.Path, msg)
	f.Details = map[string]string{"after_node": label(n)}
	return f
}

// beforeFinding builds a finding about a node of the before plan.
func beforeFinding(code string, kind finding.Kind, sev finding.Severity, n *Node, msg string) finding.Finding {
	f := newFinding(code, kind, sev, beforePrefix+n.Path, msg)
	f.Details = map[string]string{"before_node": label(n)}
	return f
}
