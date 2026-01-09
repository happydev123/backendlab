package stream

import (
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/redact"
)

func TestSinkRedactsBeforeTruncating(t *testing.T) {
	red := redact.Default().WithSecrets("sk-very-secret-token")
	s := newSink("c1", red, 12, 10)
	// The secret straddles the truncation point: redacting after
	// truncation would leave "sk-ve" visible.
	got := s.data("abcdefg sk-very-secret-token tail")
	if strings.Contains(got, "sk-") {
		t.Fatalf("secret prefix leaked: %q", got)
	}
	if got != "abcdefg [RED..." {
		t.Fatalf("preview = %q", got)
	}
}

func TestSinkFillsModuleCaseAndBounds(t *testing.T) {
	s := newSink("case-a", nil, 40, 2)
	for _, loc := range []string{"event:2", "event:1", "event:0"} {
		s.issue(CodeInvalidJSONPayload, finding.KindAssertionFailure, finding.SeverityError, loc, "bad")
	}
	items := s.items()
	if len(items) != 2 || s.dropped() != 1 || s.count(CodeInvalidJSONPayload) != 3 {
		t.Fatalf("items=%d dropped=%d count=%d", len(items), s.dropped(), s.count(CodeInvalidJSONPayload))
	}
	for _, f := range items {
		if f.Module != finding.ModuleStream || f.CaseID != "case-a" {
			t.Fatalf("finding = %+v", f)
		}
		if err := f.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if items[0].Location != "event:1" || items[1].Location != "event:2" {
		t.Fatalf("not sorted: %s, %s", items[0].Location, items[1].Location)
	}
}

func TestLocations(t *testing.T) {
	if eventLocation(7) != "event:7" || lineLocation(3) != "line:3" {
		t.Fatal("location format changed")
	}
}
