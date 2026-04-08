package store

import (
	"strings"
	"testing"
)

func validNewRun() NewRun {
	return NewRun{
		SuiteName:     "orders",
		SuiteFile:     "team/orders.yaml",
		SuiteSnapshot: []byte("version: 1\n"),
		Mode:          ModeRun,
		Targets:       []string{"staging"},
		MaxAttempts:   2,
	}
}

func TestNewRunValidate(t *testing.T) {
	cases := map[string]struct {
		mutate func(*NewRun)
		errSub string
	}{
		"empty name":         {func(n *NewRun) { n.SuiteName = " " }, "suite name"},
		"control chars":      {func(n *NewRun) { n.SuiteName = "a\nb" }, "control"},
		"absolute file":      {func(n *NewRun) { n.SuiteFile = "/etc/passwd" }, "relative"},
		"windows absolute":   {func(n *NewRun) { n.SuiteFile = `C:\suites\a.yaml` }, "relative"},
		"parent escape":      {func(n *NewRun) { n.SuiteFile = "a/../../b.yaml" }, "escapes"},
		"empty snapshot":     {func(n *NewRun) { n.SuiteSnapshot = nil }, "snapshot"},
		"bad mode":           {func(n *NewRun) { n.Mode = "fuzz" }, "mode"},
		"no targets":         {func(n *NewRun) { n.Targets = nil }, "target"},
		"duplicate target":   {func(n *NewRun) { n.Targets = []string{"a", "a"} }, "twice"},
		"compare one target": {func(n *NewRun) { n.Mode = ModeCompare }, "exactly two"},
		"writes not asked":   {func(n *NewRun) { n.Mutating = true }, "writes"},
		"attempts too high":  {func(n *NewRun) { n.MaxAttempts = MaxAttemptsLimit + 1 }, "max attempts"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			n := validNewRun()
			tc.mutate(&n)
			_, err := n.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.errSub)
			}
		})
	}
}

func TestNewRunValidateNormalizes(t *testing.T) {
	n := validNewRun()
	n.SuiteFile = `team\./orders.yaml`
	n.MaxAttempts = 0
	got, err := n.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if got.SuiteFile != "team/orders.yaml" {
		t.Errorf("SuiteFile = %q", got.SuiteFile)
	}
	if got.MaxAttempts != 1 {
		t.Errorf("MaxAttempts = %d, want default 1", got.MaxAttempts)
	}

	m := validNewRun()
	m.Mutating, m.WritesRequested, m.MaxAttempts = true, true, 3
	got, err = m.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxAttempts != 1 {
		t.Errorf("mutating run MaxAttempts = %d, want 1", got.MaxAttempts)
	}
}

func TestStatusHelpers(t *testing.T) {
	for _, s := range Statuses {
		if _, err := ParseStatus(string(s)); err != nil {
			t.Errorf("ParseStatus(%s): %v", s, err)
		}
	}
	if _, err := ParseStatus("done"); err == nil {
		t.Error("unknown status accepted")
	}
	if StatusQueued.Terminal() || StatusRunning.Terminal() || !StatusInterrupted.Terminal() {
		t.Error("Terminal() is wrong")
	}
}

func TestPublicIDFormat(t *testing.T) {
	a, err := newPublicID()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newPublicID()
	if a == b || !strings.HasPrefix(a, "run_") || len(a) != 20 || strings.ToLower(a) != a {
		t.Errorf("ids %q %q", a, b)
	}
}
