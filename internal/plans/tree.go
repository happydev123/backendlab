package plans

import (
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/happydev123/backendlab/internal/jsonutil"
)

// Tree bounds. Real plans rarely exceed a few hundred nodes or a depth of
// a few dozen; these limits reject generated or corrupted input before it
// can exhaust memory or the stack.
const (
	// MaxNodes is the largest number of plan nodes accepted in one plan.
	MaxNodes = 10000
	// MaxNodeDepth is the deepest node accepted (the root has depth 0).
	MaxNodeDepth = 100
	// maxUnknownNames bounds the distinct unknown key names kept for
	// reporting; the count itself is exact.
	maxUnknownNames = 20
	// maxUnknownNameBytes bounds each reported unknown key name.
	maxUnknownNameBytes = 40
)

// ParseError describes input that is not an acceptable EXPLAIN JSON plan.
// Location is the JSON Pointer of the offending value when one applies.
type ParseError struct {
	Name     string
	Location string
	Err      error
}

func (e *ParseError) Error() string {
	prefix := "plan"
	if e.Name != "" {
		prefix = "plan " + e.Name
	}
	if e.Location != "" {
		return fmt.Sprintf("%s: at %s: %v", prefix, e.Location, e.Err)
	}
	return fmt.Sprintf("%s: %v", prefix, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// ErrTooManyNodes and ErrTooDeep report plans past the tree bounds.
var (
	ErrTooManyNodes = fmt.Errorf("plan has more than %d nodes", MaxNodes)
	ErrTooDeep      = fmt.Errorf("plan nesting is deeper than %d nodes", MaxNodeDepth)
)

// treeBuilder converts decoded JSON objects into Nodes and accumulates the
// plan-wide flags (analyzed, buffers, counts) while it walks.
type treeBuilder struct {
	plan    *Plan
	unknown map[string]bool
}

func newTreeBuilder(p *Plan) *treeBuilder {
	return &treeBuilder{plan: p, unknown: map[string]bool{}}
}

// noteUnknown counts an unrecognized key.
func (b *treeBuilder) noteUnknown(key string) {
	b.plan.UnknownKeys++
	b.unknown[key] = true
}

// finish stores the sorted, bounded list of unknown key names.
func (b *treeBuilder) finish() {
	names := make([]string, 0, len(b.unknown))
	for k := range b.unknown {
		names = append(names, jsonutil.Truncate(k, maxUnknownNameBytes))
	}
	sort.Strings(names)
	if len(names) > maxUnknownNames {
		names = names[:maxUnknownNames]
	}
	if len(names) > 0 {
		b.plan.UnknownKeyNames = names
	}
}

// node decodes the plan node at path. Keys are processed in sorted order
// so that the first reported error does not depend on map iteration.
func (b *treeBuilder) node(v any, path string, depth int) (*Node, error) {
	if depth > MaxNodeDepth {
		return nil, &ParseError{Location: path, Err: ErrTooDeep}
	}
	b.plan.NodeCount++
	if b.plan.NodeCount > MaxNodes {
		return nil, &ParseError{Location: path, Err: ErrTooManyNodes}
	}
	if depth > b.plan.MaxDepth {
		b.plan.MaxDepth = depth
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, &ParseError{Location: path, Err: typeError("plan node object", v)}
	}
	if _, ok := obj["Node Type"]; !ok {
		return nil, &ParseError{Location: path, Err: errors.New(`plan node has no "Node Type"`)}
	}
	n := &Node{Path: path, Depth: depth}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "Plans" {
			continue
		}
		known, err := n.setField(k, obj[k])
		if err != nil {
			return nil, &ParseError{Location: path, Err: fmt.Errorf("field %q %w", k, err)}
		}
		if !known && !ignoredNodeKey(k) {
			b.noteUnknown(k)
		}
	}
	if n.NodeType == "" {
		return nil, &ParseError{Location: path, Err: errors.New(`"Node Type" is empty`)}
	}
	b.noteFlags(n)
	if raw, ok := obj["Plans"]; ok {
		children, ok := raw.([]any)
		if !ok {
			return nil, &ParseError{Location: path + "/Plans", Err: typeError("array", raw)}
		}
		n.Plans = make([]*Node, 0, len(children))
		for i, c := range children {
			child, err := b.node(c, path+"/Plans/"+strconv.Itoa(i), depth+1)
			if err != nil {
				return nil, err
			}
			n.Plans = append(n.Plans, child)
		}
	}
	return n, nil
}

// noteFlags updates the plan-wide presence flags from one node.
func (b *treeBuilder) noteFlags(n *Node) {
	if n.ActualLoops != nil {
		b.plan.Analyzed = true
	}
	if n.ActualTotalTime != nil {
		b.plan.Timing = true
	}
	if n.Buffers.Known() {
		b.plan.Buffers = true
	}
}
