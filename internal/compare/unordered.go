package compare

import (
	"fmt"

	"github.com/happydev123/backendlab/internal/jsonutil"
)

// Keyed matching pairs elements of two arrays by the value of one member.
// It is only used for arrays the suite marks as unordered, and it refuses
// to guess: if any element is not an object, lacks the key, has a key that
// is not a string, number or boolean, or shares its key with another
// element of the same array, the arrays are reported as unmatchable.

// keyedPair links a baseline element index to a candidate element index.
type keyedPair struct{ b, c int }

// keyedMatch is the result of matching two arrays by key.
type keyedMatch struct {
	// pairs are in baseline order.
	pairs []keyedPair
	// missing lists baseline indexes with no candidate element.
	missing []int
	// added lists candidate indexes with no baseline element.
	added []int
}

// matchByKey pairs the elements of base and cand by member key. A non-empty
// problem string explains why the arrays cannot be matched.
func matchByKey(base, cand []any, key string) (keyedMatch, string) {
	bIdx, problem := indexByKey(base, key, "baseline")
	if problem != "" {
		return keyedMatch{}, problem
	}
	cIdx, problem := indexByKey(cand, key, "candidate")
	if problem != "" {
		return keyedMatch{}, problem
	}
	var m keyedMatch
	for i, el := range base {
		id, _ := keyIdentity(el.(map[string]any)[key])
		if j, ok := cIdx[id]; ok {
			m.pairs = append(m.pairs, keyedPair{b: i, c: j})
		} else {
			m.missing = append(m.missing, i)
		}
	}
	for j, el := range cand {
		id, _ := keyIdentity(el.(map[string]any)[key])
		if _, ok := bIdx[id]; !ok {
			m.added = append(m.added, j)
		}
	}
	return m, ""
}

// indexByKey maps each element's key identity to its index.
func indexByKey(arr []any, key, side string) (map[string]int, string) {
	idx := make(map[string]int, len(arr))
	for i, el := range arr {
		obj, ok := el.(map[string]any)
		if !ok {
			return nil, fmt.Sprintf("%s element %d is %s, not an object", side, i, jsonutil.TypeName(el))
		}
		kv, ok := obj[key]
		if !ok {
			return nil, fmt.Sprintf("%s element %d has no member %q", side, i, key)
		}
		id, ok := keyIdentity(kv)
		if !ok {
			return nil, fmt.Sprintf("%s element %d has a %s %q; keys must be strings, numbers or booleans", side, i, jsonutil.TypeName(kv), key)
		}
		if prev, dup := idx[id]; dup {
			return nil, fmt.Sprintf("%s elements %d and %d share the same %q", side, prev, i, key)
		}
		idx[id] = i
	}
	return idx, ""
}

// keyIdentity returns a type-tagged identity for a scalar key. Numbers
// use their normalized decimal form, so 7 and 7.0 identify the same
// element.
func keyIdentity(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return "s:" + t, true
	case bool:
		if t {
			return "b:true", true
		}
		return "b:false", true
	default:
		if d, ok := toDecimal(v); ok {
			return "n:" + d.String(), true
		}
		if txt := numberText(v); txt != "" {
			return "t:" + txt, true
		}
	}
	return "", false
}
