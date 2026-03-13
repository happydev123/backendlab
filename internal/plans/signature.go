package plans

import (
	"sort"
	"strings"
)

// signature is the matching key: the node family plus the object it works
// on. Scans use their relation, CTE, function or alias; other nodes use the
// sorted set of relations scanned anywhere below them.
func (a *aligner) signature(n *Node) string {
	if s, ok := a.sig[n]; ok {
		return s
	}
	s := string(n.Family()) + ":" + objectKey(n)
	if objectKey(n) == "" {
		s = string(n.Family()) + ":{" + strings.Join(subtreeRelations(n), ",") + "}"
	}
	a.sig[n] = s
	return s
}

// describe renders the object part of a signature for match reasons.
func (a *aligner) describe(n *Node) string {
	if k := objectKey(n); k != "" {
		return string(n.Family()) + " " + k
	}
	rels := subtreeRelations(n)
	if len(rels) == 0 {
		return string(n.Family()) + " without relations"
	}
	return string(n.Family()) + " over " + strings.Join(rels, ", ")
}

func objectKey(n *Node) string {
	switch {
	case n.Relation() != "":
		return "relation " + n.Relation()
	case n.CTEName != "":
		return "CTE " + n.CTEName
	case n.FunctionName != "":
		return "function " + n.FunctionName
	case n.Family() == FamilyScan && n.Alias != "":
		return "alias " + n.Alias
	}
	return ""
}

// subtreeRelations returns the distinct relations scanned in n's subtree.
func subtreeRelations(n *Node) []string {
	seen := map[string]bool{}
	walk(n, func(c *Node) bool {
		if r := c.Relation(); r != "" {
			seen[r] = true
		}
		return true
	})
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func subtreeSize(n *Node) int {
	count := 0
	walk(n, func(*Node) bool { count++; return true })
	return count
}

// wrapped returns the single main child of a node that the planner commonly
// inserts above another node (Hash, Sort, Materialize, Memoize, Gather),
// or nil. Init plans and subplans attached to the wrapper do not count as
// its main child.
func wrapped(n *Node) *Node {
	switch n.Family() {
	case FamilyHash, FamilySort, FamilyMaterialize, FamilyGather:
	default:
		return nil
	}
	var main *Node
	for _, c := range n.Plans {
		if c.ParentRelationship == "InitPlan" || c.ParentRelationship == "SubPlan" {
			continue
		}
		if main != nil {
			return nil
		}
		main = c
	}
	return main
}

// wrapsMatch reports whether w is a wrapper whose main child has the same
// signature as other.
func (a *aligner) wrapsMatch(w, other *Node) bool {
	c := wrapped(w)
	return c != nil && c.Family() != w.Family() && a.signature(c) == a.signature(other)
}
