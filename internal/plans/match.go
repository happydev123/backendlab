package plans

import (
	"fmt"
	"sort"
)

// Match confidence levels.
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// Match pairs a node of the before plan with a node of the after plan. The
// pairing is a heuristic: EXPLAIN output carries no node identity, so a
// match means "these nodes play the same role", not "these are the same
// executor node". Reason explains why the pair was made.
type Match struct {
	BeforePath string `json:"before_path"`
	AfterPath  string `json:"after_path"`
	Before     *Node  `json:"-"`
	After      *Node  `json:"-"`
	Confidence string `json:"confidence"`
	Reason     string `json:"reason"`
}

// unmatched is a node without a counterpart. For a subtree root, size is
// the number of nodes in the subtree. A wrapper is a single inserted or
// removed node whose child was matched.
type unmatched struct {
	node    *Node
	size    int
	wrapper bool
}

// divergence records a matched pair whose children could not be aligned:
// both sides kept children without counterparts.
type divergence struct {
	before, after *Node
	removed       []*Node
	added         []*Node
}

// alignment is the result of matching two plan trees.
type alignment struct {
	matches   []Match
	removed   []unmatched
	added     []unmatched
	divergent []divergence
}

// aligner holds per-comparison caches.
type aligner struct {
	sig map[*Node]string
	out alignment
}

// align matches the trees of before and after top-down.
func align(before, after *Plan) alignment {
	a := &aligner{sig: map[*Node]string{}}
	rb, ra := before.Root, after.Root
	switch {
	case a.signature(rb) == a.signature(ra):
		a.pair(rb, ra, ConfidenceHigh, "root nodes with the same signature ("+a.describe(rb)+")")
	case rb.Family() == ra.Family():
		a.pair(rb, ra, ConfidenceMedium, fmt.Sprintf("root nodes of the same family %s; signatures differ (%s vs %s)", rb.Family(), a.describe(rb), a.describe(ra)))
	case a.wrapsMatch(ra, rb):
		a.wrapper(ra, &a.out.added)
		a.pair(rb, wrapped(ra), ConfidenceMedium, "matched below the "+ra.Operation()+" added at the root")
	case a.wrapsMatch(rb, ra):
		a.wrapper(rb, &a.out.removed)
		a.pair(wrapped(rb), ra, ConfidenceMedium, "matched below the "+rb.Operation()+" removed from the root")
	default:
		a.out.removed = append(a.out.removed, unmatched{node: rb, size: subtreeSize(rb)})
		a.out.added = append(a.out.added, unmatched{node: ra, size: subtreeSize(ra)})
		a.out.divergent = append(a.out.divergent, divergence{removed: []*Node{rb}, added: []*Node{ra}})
	}
	return a.out
}

// candidate is a child pairing chosen while aligning one level.
type candidate struct {
	b, a       *Node
	order      int
	confidence string
	reason     string
}

// pair records a match and aligns the children of the two nodes.
func (a *aligner) pair(b, f *Node, confidence, reason string) {
	a.out.matches = append(a.out.matches, Match{
		BeforePath: b.Path, AfterPath: f.Path, Before: b, After: f,
		Confidence: confidence, Reason: reason,
	})
	a.children(b, f)
}

// children aligns the child lists of a matched pair in four passes:
// same signature and parent relationship; same signature only; one side
// wrapped in an inserted single-child node; and position among the
// remaining children of the same family and relationship. Children left
// over on both sides mark the pair as divergent and are not paired.
func (a *aligner) children(b, f *Node) {
	B, A := b.Plans, f.Plans
	usedB, usedA := make([]bool, len(B)), make([]bool, len(A))
	var picks []candidate
	take := func(i, j int, bn, an *Node, conf, reason string) {
		usedB[i], usedA[j] = true, true
		picks = append(picks, candidate{b: bn, a: an, order: i, confidence: conf, reason: reason})
	}
	// Index the after children by signature, and the after wrappers by the
	// signature of their main child, so wide nodes (large Append lists)
	// align in linear time.
	bySig, byWrapped := map[string][]int{}, map[string][]int{}
	for j, ac := range A {
		bySig[a.signature(ac)] = append(bySig[a.signature(ac)], j)
		if c := wrapped(ac); c != nil && c.Family() != ac.Family() {
			byWrapped[a.signature(c)] = append(byWrapped[a.signature(c)], j)
		}
	}
	for i, bc := range B {
		for _, j := range bySig[a.signature(bc)] {
			if ac := A[j]; !usedA[j] && bc.ParentRelationship == ac.ParentRelationship {
				take(i, j, bc, ac, ConfidenceHigh, fmt.Sprintf("same signature (%s) and parent relationship %s", a.describe(bc), orUnknown(bc.ParentRelationship)))
				break
			}
		}
	}
	for i, bc := range B {
		for _, j := range bySig[a.signature(bc)] {
			if ac := A[j]; !usedB[i] && !usedA[j] {
				take(i, j, bc, ac, ConfidenceMedium, fmt.Sprintf("same signature (%s); parent relationship changed %s -> %s", a.describe(bc), orUnknown(bc.ParentRelationship), orUnknown(ac.ParentRelationship)))
			}
		}
	}
	for i, bc := range B {
		for _, j := range byWrapped[a.signature(bc)] {
			if ac := A[j]; !usedB[i] && !usedA[j] {
				a.wrapper(ac, &a.out.added)
				take(i, j, bc, wrapped(ac), ConfidenceMedium, fmt.Sprintf("same signature (%s) below the added %s", a.describe(bc), ac.Operation()))
			}
		}
		c := wrapped(bc)
		if usedB[i] || c == nil || c.Family() == bc.Family() {
			continue
		}
		for _, j := range bySig[a.signature(c)] {
			if ac := A[j]; !usedB[i] && !usedA[j] {
				a.wrapper(bc, &a.out.removed)
				take(i, j, c, ac, ConfidenceMedium, fmt.Sprintf("same signature (%s) below the removed %s", a.describe(ac), bc.Operation()))
			}
		}
	}
	restB, restA := unusedIndexes(usedB), unusedIndexes(usedA)
	for k := 0; k < len(restB) && k < len(restA); k++ {
		bc, ac := B[restB[k]], A[restA[k]]
		if bc.Family() == ac.Family() && bc.ParentRelationship == ac.ParentRelationship {
			take(restB[k], restA[k], bc, ac, ConfidenceLow, fmt.Sprintf("positional fallback: child %d of the same family %s, signatures differ (%s vs %s)", restA[k], bc.Family(), a.describe(bc), a.describe(ac)))
		}
	}
	var gone, extra []*Node
	for i, used := range usedB {
		if !used {
			gone = append(gone, B[i])
			a.out.removed = append(a.out.removed, unmatched{node: B[i], size: subtreeSize(B[i])})
		}
	}
	for j, used := range usedA {
		if !used {
			extra = append(extra, A[j])
			a.out.added = append(a.out.added, unmatched{node: A[j], size: subtreeSize(A[j])})
		}
	}
	if len(gone) > 0 && len(extra) > 0 {
		a.out.divergent = append(a.out.divergent, divergence{before: b, after: f, removed: gone, added: extra})
	}
	sort.SliceStable(picks, func(x, y int) bool { return picks[x].order < picks[y].order })
	for _, p := range picks {
		a.pair(p.b, p.a, p.confidence, p.reason)
	}
}

func unusedIndexes(used []bool) []int {
	var out []int
	for i, u := range used {
		if !u {
			out = append(out, i)
		}
	}
	return out
}

// wrapper records w as an inserted or removed single node. Init plans and
// subplans attached to it have no counterpart either and are recorded as
// whole subtrees.
func (a *aligner) wrapper(w *Node, list *[]unmatched) {
	*list = append(*list, unmatched{node: w, size: 1, wrapper: true})
	main := wrapped(w)
	for _, c := range w.Plans {
		if c != main {
			*list = append(*list, unmatched{node: c, size: subtreeSize(c)})
		}
	}
}
