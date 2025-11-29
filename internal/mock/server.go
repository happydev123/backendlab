// Package mock implements the demonstration service used by the examples
// and by integration tests. One process serves two variants of the same
// API under the path prefixes /baseline and /candidate, so a comparison run
// can point its two targets at one local process.
//
// The candidate variant carries deliberate, documented differences (see
// invoices.go) so that every comparison and validation feature has
// something to find. The service also contains development fault controls:
// slow responses, streams with long pauses or missing terminal markers, a
// side-effect counter and a reset endpoint. These controls exist only in
// this package. The service binds to loopback by default and must never be
// exposed on a shared network.
package mock

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// Variant selects which version of the API a request is served by.
type Variant string

// The two variants served by one process.
const (
	Baseline  Variant = "baseline"
	Candidate Variant = "candidate"
)

// Variants returns both variants in a fixed order.
func Variants() []Variant { return []Variant{Baseline, Candidate} }

// Defaults used by the mockservice command.
const (
	DefaultAddr              = "127.0.0.1:8090"
	DefaultInvoiceTotalDelta = "0.05"
	DefaultMaxDelay          = 10 * time.Second
	// MaxMaxDelay bounds the configurable delay cap so a mistyped flag
	// cannot hold connections open for hours.
	MaxMaxDelay = 10 * time.Minute
)

// Config holds the service settings.
type Config struct {
	// InvoiceTotalDelta is added to the candidate invoice total, which
	// breaks the subtotal + tax = total relation. It must have at most two
	// decimal places so totals stay exact cent amounts. Zero disables the
	// drift.
	InvoiceTotalDelta decimal.Decimal
	// MaxDelay caps every requested delay or stream pause. Zero means
	// DefaultMaxDelay.
	MaxDelay time.Duration
	// Now returns the current time for volatile timestamps. Nil means
	// time.Now.
	Now func() time.Time
}

// DefaultConfig returns the settings used when no flags are given.
func DefaultConfig() Config {
	return Config{
		InvoiceTotalDelta: decimal.RequireFromString(DefaultInvoiceTotalDelta),
		MaxDelay:          DefaultMaxDelay,
	}
}

// Validate checks settings that come from user input.
func (c Config) Validate() error {
	var errs []error
	if c.MaxDelay < 0 {
		errs = append(errs, errors.New("max delay must not be negative"))
	}
	if c.MaxDelay > MaxMaxDelay {
		errs = append(errs, fmt.Errorf("max delay must not exceed %s", MaxMaxDelay))
	}
	if !c.InvoiceTotalDelta.Equal(c.InvoiceTotalDelta.Round(2)) {
		errs = append(errs, fmt.Errorf("invoice total delta %s has more than two decimal places", c.InvoiceTotalDelta))
	}
	limit := decimal.NewFromInt(1000000)
	if c.InvoiceTotalDelta.Abs().GreaterThan(limit) {
		errs = append(errs, fmt.Errorf("invoice total delta must be within +/-%s", limit))
	}
	return errors.Join(errs...)
}

// Server holds the state of one mock service instance. All mutable state
// lives here; the package has no globals.
type Server struct {
	delta    decimal.Decimal
	maxDelay time.Duration
	now      func() time.Time

	mu     sync.Mutex
	counts map[Variant]int64
}

// New returns a server for cfg. Zero MaxDelay and nil Now are replaced by
// their defaults; callers handling user input should call cfg.Validate
// first, because New does not reject invalid values.
func New(cfg Config) *Server {
	s := &Server{
		delta:    cfg.InvoiceTotalDelta.Round(2),
		maxDelay: cfg.MaxDelay,
		now:      cfg.Now,
		counts:   map[Variant]int64{},
	}
	if s.maxDelay <= 0 {
		s.maxDelay = DefaultMaxDelay
	}
	if s.maxDelay > MaxMaxDelay {
		s.maxDelay = MaxMaxDelay
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// MaxDelay returns the effective delay cap.
func (s *Server) MaxDelay() time.Duration { return s.maxDelay }

// timestamp formats the current time for generated_at fields.
func (s *Server) timestamp() string {
	return s.now().UTC().Format(time.RFC3339Nano)
}
