package validate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonutil"
)

// Document is one document of a batch. ID is empty until Validator.Identify
// (or Run) assigns it; Index is the zero-based position in the input.
type Document struct {
	ID    string
	Index int
	Value any
}

// Result is the outcome of validating a batch.
type Result struct {
	// Documents are the input documents with IDs assigned.
	Documents []Document
	// Findings are sorted with finding.Sort and bounded by MaxFindings.
	Findings []finding.Finding
	// Dropped counts findings left out after the bound.
	Dropped int
}

// Identify returns a copy of docs with every ID set. A document that has
// no ID yet takes the value at the rules' document_id pointer when it is a
// non-empty string or a number (and not at a redacted path); otherwise its
// batch position "#<index>". IDs are made unique for reporting: a repeated
// ID gets "#<index>" appended ("INV-1002", then "INV-1002#3"), so
// duplicate documents stay distinguishable while the unique rule reports
// the duplication itself.
func (v *Validator) Identify(docs []Document) []Document {
	out := make([]Document, len(docs))
	used := make(map[string]bool, len(docs))
	for i, d := range docs {
		id := d.ID
		if id == "" {
			id = v.idFromDocument(d.Value)
		}
		if id == "" {
			id = fallbackID(d.Index)
		}
		out[i] = d
		out[i].ID = uniqueID(id, d.Index, used)
	}
	return out
}

func (v *Validator) idFromDocument(doc any) string {
	if v.rules == nil || v.rules.documentID == nil {
		return ""
	}
	ref := *v.rules.documentID
	if v.san.redacted(ref.ptr) {
		return ""
	}
	val, found := ref.lookup(doc)
	if !found {
		return ""
	}
	var s string
	switch t := val.(type) {
	case string:
		s = t
	case json.Number:
		s = string(t)
	default:
		return ""
	}
	return jsonutil.Truncate(v.san.red.String(s), MaxIDLength)
}

func fallbackID(index int) string { return "#" + strconv.Itoa(index) }

func uniqueID(id string, index int, used map[string]bool) string {
	cand := id
	if used[cand] {
		cand = fmt.Sprintf("%s#%d", id, index)
		for k := 2; used[cand]; k++ {
			cand = fmt.Sprintf("%s#%d-%d", id, index, k)
		}
	}
	used[cand] = true
	return cand
}

// Run validates every document (schemas and per-document rules) and then
// applies batch rules such as unique. When the findings exceed
// MaxFindings, every document with findings keeps at least its most severe
// one before the remaining slots are filled in document order, so case
// statuses stay accurate whenever the bound allows it.
func (v *Validator) Run(docs []Document) Result {
	ided := v.Identify(docs)
	per := make([][]finding.Finding, len(ided))
	dropped := 0
	for i, d := range ided {
		c := finding.NewCollector(v.maxFindings)
		v.checkDocument(d.ID, d.Value, c)
		per[i] = c.Items()
		dropped += c.Dropped()
	}
	if v.rules != nil && len(v.rules.batch) > 0 {
		ids := make([]string, len(ided))
		values := make([]any, len(ided))
		for i, d := range ided {
			ids[i], values[i] = d.ID, d.Value
		}
		bc := &batchCheck{ids: ids, docs: values, san: v.san, emit: func(i int, f finding.Finding) {
			f.CaseID = ids[i]
			per[i] = append(per[i], f)
		}}
		for _, r := range v.rules.batch {
			r.checkBatch(bc)
		}
	}
	findings, more := selectFindings(per, v.maxFindings)
	return Result{Documents: ided, Findings: findings, Dropped: dropped + more}
}

// Batch is Run returning only the findings. When findings were dropped it
// appends a validate.findings_truncated finding with the count.
func (v *Validator) Batch(docs []Document) []finding.Finding {
	res := v.Run(docs)
	out := res.Findings
	if res.Dropped > 0 {
		out = append(out, truncatedFinding("", res.Dropped, v.maxFindings))
	}
	return out
}

// selectFindings flattens per-document findings to at most limit, preferring
// one failing finding per document, and sorts the result. It also returns
// how many findings were left out.
func selectFindings(per [][]finding.Finding, limit int) ([]finding.Finding, int) {
	total := 0
	for _, fs := range per {
		total += len(fs)
	}
	out := make([]finding.Finding, 0, min(total, limit))
	if total <= limit {
		for _, fs := range per {
			out = append(out, fs...)
		}
		finding.Sort(out)
		return out, 0
	}
	taken := make([]int, len(per))
	for i, fs := range per {
		sort.SliceStable(fs, func(a, b int) bool {
			if fs[a].Failing() != fs[b].Failing() {
				return fs[a].Failing()
			}
			return finding.Less(fs[a], fs[b])
		})
		if len(fs) > 0 && len(out) < limit {
			out = append(out, fs[0])
			taken[i] = 1
		}
	}
	for i, fs := range per {
		for _, f := range fs[taken[i]:] {
			if len(out) >= limit {
				break
			}
			out = append(out, f)
			taken[i]++
		}
	}
	finding.Sort(out)
	return out, total - len(out)
}
