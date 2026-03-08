package plans

import (
	"fmt"
	"sort"
	"strconv"
)

// decodeStatement fills the plan from the statement object: the node tree
// under "Plan" plus the statement-level timing, trigger, JIT and settings
// blocks. Keys are processed in sorted order for stable errors.
func decodeStatement(b *treeBuilder, stmt map[string]any) error {
	keys := make([]string, 0, len(stmt))
	for k := range stmt {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	p := b.plan
	for _, k := range keys {
		v := stmt[k]
		loc := "/" + k
		var err error
		switch k {
		case "Plan":
			p.Root, err = b.node(v, "/Plan", 0)
			if err != nil {
				return err
			}
			continue
		case "Planning Time":
			err = setNumber(&p.PlanningTime, v)
		case "Execution Time":
			err = setNumber(&p.ExecutionTime, v)
		case "Triggers":
			p.Triggers, err = decodeTriggers(v, loc)
		case "JIT":
			p.JIT, err = decodeJIT(v)
		case "Settings":
			p.Settings, err = decodeSettings(v)
		case "Planning", "Query Identifier", "Query Text", "Serialization":
			// Planning buffers/memory, query id and serialization details
			// are recognized but not compared.
		default:
			b.noteUnknown(k)
		}
		if err != nil {
			if _, ok := err.(*ParseError); ok {
				return err
			}
			return &ParseError{Location: loc, Err: err}
		}
	}
	return nil
}

// decodeTriggers reads the "Triggers" array of an analyzed statement.
func decodeTriggers(v any, loc string) ([]Trigger, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, typeError("array", v)
	}
	if len(arr) > MaxNodes {
		return nil, fmt.Errorf("more than %d triggers", MaxNodes)
	}
	out := make([]Trigger, 0, len(arr))
	for i, e := range arr {
		at := loc + "/" + strconv.Itoa(i)
		obj, ok := e.(map[string]any)
		if !ok {
			return nil, &ParseError{Location: at, Err: typeError("object", e)}
		}
		var t Trigger
		for _, f := range []struct {
			key string
			set func(any) error
		}{
			{"Trigger Name", func(v any) error { return setString(&t.Name, v) }},
			{"Relation", func(v any) error { return setString(&t.Relation, v) }},
			{"Time", func(v any) error { return setNumber(&t.Time, v) }},
			{"Calls", func(v any) error { return setNumber(&t.Calls, v) }},
		} {
			if raw, ok := obj[f.key]; ok {
				if err := f.set(raw); err != nil {
					return nil, &ParseError{Location: at, Err: fmt.Errorf("field %q %w", f.key, err)}
				}
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// decodeJIT reads the function count and total time of the "JIT" block.
// PostgreSQL 16 nests per-phase objects inside "Timing"; only the overall
// "Total" is kept, which has the same shape in every version.
func decodeJIT(v any) (*JIT, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, typeError("object", v)
	}
	j := &JIT{}
	if raw, ok := obj["Functions"]; ok {
		if err := setNumber(&j.Functions, raw); err != nil {
			return nil, fmt.Errorf("field %q %w", "Functions", err)
		}
	}
	if raw, ok := obj["Timing"]; ok {
		timing, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("field %q %w", "Timing", typeError("object", raw))
		}
		if total, ok := timing["Total"]; ok {
			if err := setNumber(&j.TotalTime, total); err != nil {
				return nil, fmt.Errorf("field %q %w", "Timing/Total", err)
			}
		}
	}
	return j, nil
}

// decodeSettings reads EXPLAIN (SETTINGS): an object of setting names to
// string values.
func decodeSettings(v any) (map[string]string, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, typeError("object", v)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(obj))
	for _, k := range keys {
		raw := obj[k]
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("setting %q %w", k, typeError("string", raw))
		}
		out[k] = s
	}
	return out, nil
}
