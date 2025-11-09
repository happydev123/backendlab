package limits

import (
	"testing"
	"time"
)

func TestDefaultsAreValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := Hard().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMergeKeepsExplicitValues(t *testing.T) {
	l := Limits{MaxCases: 7, RunTimeout: time.Minute}.Merge()
	if l.MaxCases != 7 || l.RunTimeout != time.Minute {
		t.Fatalf("explicit values lost: %+v", l)
	}
	if l.MaxResponseBytes != Default().MaxResponseBytes {
		t.Fatalf("default not applied: %d", l.MaxResponseBytes)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := map[string]func(*Limits){
		"negative":         func(l *Limits) { l.MaxConcurrency = -1 },
		"over hard":        func(l *Limits) { l.MaxResponseBytes = Hard().MaxResponseBytes + 1 },
		"timeout inverted": func(l *Limits) { l.RequestTimeout = 5 * time.Minute; l.MaxRequestTime = time.Minute },
		"line over event":  func(l *Limits) { l.MaxLineBytes = 2 << 20; l.MaxEventBytes = 1 << 20 },
	}
	for name, mutate := range tests {
		l := Default()
		mutate(&l)
		if err := l.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
