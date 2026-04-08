package store

import "fmt"

// Status is the lifecycle state of a run. Allowed transitions are defined
// in package jobs (jobs.CanTransition) and enforced again by a database
// trigger (migration 0003).
type Status string

const (
	// StatusQueued: waiting for a worker to claim it.
	StatusQueued Status = "queued"
	// StatusRunning: claimed by a worker that holds a lease.
	StatusRunning Status = "running"
	// StatusSucceeded: the run completed and the report passed.
	StatusSucceeded Status = "succeeded"
	// StatusFailed: the run completed with failures, or could not execute.
	StatusFailed Status = "failed"
	// StatusCancelled: cancelled by an operator before or during execution.
	StatusCancelled Status = "cancelled"
	// StatusInterrupted: the worker stopped (crash, lost lease or shutdown)
	// and the run will not be retried automatically, either because it
	// contains mutating cases or because it ran out of attempts. An
	// operator decides whether to resubmit it.
	StatusInterrupted Status = "interrupted"
)

// Statuses lists every status in lifecycle order.
var Statuses = []Status{
	StatusQueued, StatusRunning, StatusSucceeded, StatusFailed, StatusCancelled, StatusInterrupted,
}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	for _, k := range Statuses {
		if s == k {
			return true
		}
	}
	return false
}

// Terminal reports whether s has no outgoing transitions.
func (s Status) Terminal() bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusCancelled, StatusInterrupted:
		return true
	}
	return false
}

// ParseStatus validates a status name, for example from a query parameter.
func ParseStatus(name string) (Status, error) {
	s := Status(name)
	if !s.Valid() {
		return "", fmt.Errorf("unknown run status %q", name)
	}
	return s, nil
}

// Mode is how a run executes its suite.
type Mode string

const (
	// ModeRun executes cases against one target and checks expectations.
	ModeRun Mode = "run"
	// ModeCompare executes cases against a baseline and a candidate target
	// and compares the responses.
	ModeCompare Mode = "compare"
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool { return m == ModeRun || m == ModeCompare }
