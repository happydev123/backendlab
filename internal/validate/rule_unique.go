package validate

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/happydev123/backendlab/internal/finding"
)

// maxListedDuplicates bounds how many other documents a duplicate finding
// names.
const maxListedDuplicates = 10

// batchRule is a rule evaluated across all documents of a batch.
type batchRule interface {
	checkBatch(b *batchCheck)
}

// batchCheck carries a batch through the batch rules. emit records a
// finding for the document at index i.
type batchCheck struct {
	ids  []string
	docs []any
	san  sanitizer
	emit func(i int, f finding.Finding)
}

// uniqueRule reports documents of a batch that share a value at a field,
// for example a duplicated invoice number. Every document of a duplicate
// group gets a finding naming the others, since the data cannot tell which
// one is wrong. Values compare by JSON type and value: strings exactly,
// numbers by decimal value (1.0 equals 1), so "1001" and 1001 differ.
// Absent and null values are not compared. Unique rules only run in batch
// mode (Validator.Batch and Validator.Run), not for single documents.
type uniqueRule struct {
	ruleBase
	field fieldRef
}

func compileUnique(b ruleBase, s ruleSpec) (*uniqueRule, error) {
	field, err := parseFieldRef("field", s.Field)
	if err != nil {
		return nil, err
	}
	return &uniqueRule{ruleBase: b, field: field}, nil
}

func (r *uniqueRule) checkBatch(bc *batchCheck) {
	expected := r.field.text + " unique within the batch"
	groups := map[string][]int{}
	var order []string
	for i, doc := range bc.docs {
		v, found := r.field.lookup(doc)
		if !found || v == nil {
			continue
		}
		key := uniqueKey(v)
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}
	for _, key := range order {
		members := groups[key]
		if len(members) < 2 {
			continue
		}
		for _, i := range members {
			others := make([]string, 0, len(members)-1)
			for _, j := range members {
				if j != i {
					others = append(others, bc.ids[j])
				}
			}
			v, _ := r.field.lookup(bc.docs[i])
			msg := fmt.Sprintf("%s: the value of %s also appears in %s", r.name(), r.field.text, listIDs(others))
			bc.emit(i, r.failure(r.field.ptr, expected, bc.san.value(r.field.ptr, v), msg))
		}
	}
}

// uniqueKey maps a value to a comparison key that includes its JSON type.
func uniqueKey(v any) string {
	switch t := v.(type) {
	case string:
		return "s:" + t
	case json.Number:
		if d, err := parseDecimalNumber(t); err == nil {
			n := d.Normalize()
			return "n:" + n.Coefficient().String() + "e" + strconv.Itoa(int(n.Exponent()))
		}
		return "n:" + string(t)
	case bool:
		return "b:" + strconv.FormatBool(t)
	default:
		data, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("x:%T", t)
		}
		return "j:" + string(data)
	}
}

// listIDs renders document IDs for a message, bounded.
func listIDs(ids []string) string {
	if len(ids) <= maxListedDuplicates {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(ids[:maxListedDuplicates], ", "), len(ids)-maxListedDuplicates)
}
