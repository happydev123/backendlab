package finding

import (
	"sort"
	"strings"
)

var severityRank = map[Severity]int{SeverityError: 0, SeverityWarning: 1, SeverityInfo: 2}

// Sort orders findings deterministically: by case ID, module, location,
// code, severity and finally message. Producers may emit findings in any
// order (for example from concurrent case execution); reports always sort
// before rendering.
func Sort(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool { return Less(fs[i], fs[j]) })
}

// Less is the ordering used by Sort.
func Less(a, b Finding) bool {
	if c := strings.Compare(a.CaseID, b.CaseID); c != 0 {
		return c < 0
	}
	if c := strings.Compare(string(a.Module), string(b.Module)); c != 0 {
		return c < 0
	}
	if c := compareLocation(a.Location, b.Location); c != 0 {
		return c < 0
	}
	if c := strings.Compare(a.Code, b.Code); c != 0 {
		return c < 0
	}
	if ra, rb := severityRank[a.Severity], severityRank[b.Severity]; ra != rb {
		return ra < rb
	}
	return a.Message < b.Message
}

// compareLocation compares locations segment by segment so that numeric
// array indexes sort numerically ("/items/2" before "/items/10").
func compareLocation(a, b string) int {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		ap, an, aok := splitIndex(as[i])
		bp, bn, bok := splitIndex(bs[i])
		// A "name:N" segment is numeric only against the same prefix; plain
		// indexes sort before names, as array elements before object keys.
		if ap != "" || bp != "" {
			if !(aok && bok && ap == bp) {
				aok, bok = false, false
			}
		}
		switch {
		case aok && bok:
			if an < bn {
				return -1
			}
			return 1
		case aok:
			return -1
		case bok:
			return 1
		}
		return strings.Compare(as[i], bs[i])
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}

// splitIndex splits a location segment into an optional "name:" prefix and
// a numeric index, so "event:10", "before:3" and "12" compare numerically
// against segments with the same prefix.
func splitIndex(s string) (prefix string, n int, ok bool) {
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		prefix, s = s[:i+1], s[i+1:]
	}
	n, ok = index(s)
	return prefix, n, ok
}

func index(s string) (int, bool) {
	if s == "" || len(s) > 9 {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}
