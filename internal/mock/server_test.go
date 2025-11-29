package mock

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"defaults", DefaultConfig(), ""},
		{"zero", Config{}, ""},
		{"negative delta", Config{InvoiceTotalDelta: decimal.RequireFromString("-1.25")}, ""},
		{"three decimals", Config{InvoiceTotalDelta: decimal.RequireFromString("0.005")}, "two decimal places"},
		{"trailing zero is fine", Config{InvoiceTotalDelta: decimal.RequireFromString("0.050")}, ""},
		{"huge delta", Config{InvoiceTotalDelta: decimal.RequireFromString("2000000")}, "within"},
		{"negative max delay", Config{MaxDelay: -time.Second}, "negative"},
		{"max delay too large", Config{MaxDelay: time.Hour}, "exceed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	s := New(Config{})
	if s.MaxDelay() != DefaultMaxDelay {
		t.Errorf("MaxDelay() = %s, want %s", s.MaxDelay(), DefaultMaxDelay)
	}
	if s.now == nil {
		t.Error("now is nil")
	}
	if got := New(Config{MaxDelay: 24 * time.Hour}).MaxDelay(); got != MaxMaxDelay {
		t.Errorf("oversized MaxDelay clamped to %s, want %s", got, MaxMaxDelay)
	}
}

func TestVariants(t *testing.T) {
	v := Variants()
	if len(v) != 2 || v[0] != Baseline || v[1] != Candidate {
		t.Fatalf("Variants() = %v", v)
	}
}

// fixedClock returns a Now function that always reports t.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// testServer returns a server with a fixed clock and a short delay cap.
func testServer() *Server {
	cfg := DefaultConfig()
	cfg.Now = fixedClock(time.Date(2026, 3, 14, 9, 26, 53, 589793000, time.UTC))
	cfg.MaxDelay = 20 * time.Millisecond
	return New(cfg)
}
