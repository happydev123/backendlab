// Package jobs is JobDock, the PostgreSQL-backed execution engine for
// background suite runs submitted to "backendlab serve". It is not a
// general job platform: it executes one kind of job (a suite run) with an
// explicit state machine, lease-based claims with fencing tokens,
// heartbeats, operator cancellation and a conservative crash recovery
// policy. See docs/jobdock.md for the full description and limitations.
//
// The state machine is defined here (CanTransition) and enforced a second
// time in SQL: every statement that changes a run's status names the
// expected current status in its WHERE clause, and a database trigger
// rejects any transition outside the same table.
package jobs

import (
	"github.com/happydev123/backendlab/internal/store"
)

// transitions is the run state machine. running -> queued is listed but
// additionally requires CanRequeue.
var transitions = map[store.Status][]store.Status{
	store.StatusQueued: {store.StatusRunning, store.StatusCancelled},
	store.StatusRunning: {
		store.StatusSucceeded, store.StatusFailed, store.StatusCancelled,
		store.StatusInterrupted, store.StatusQueued,
	},
}

// CanTransition reports whether the state machine allows from -> to:
//
//	queued  -> running (claim) | cancelled (operator)
//	running -> succeeded | failed | cancelled (worker completion)
//	running -> interrupted (recovery or shutdown, outcome uncertain)
//	running -> queued (recovery or shutdown of a read-only run; see CanRequeue)
//
// Terminal states have no outgoing transitions; an interrupted run is only
// re-executed through store.Resubmit, which creates a new run.
func CanTransition(from, to store.Status) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// CanRequeue reports whether a running run may go back to queued after
// its worker stopped: only read-only runs, and only while attempts remain.
// Mutating runs are never re-executed automatically because the earlier
// attempt may already have changed upstream state.
func CanRequeue(mutating bool, attempt, maxAttempts int) bool {
	return !mutating && attempt < maxAttempts
}

// Next returns the states reachable from s, in a stable order.
func Next(s store.Status) []store.Status {
	return append([]store.Status(nil), transitions[s]...)
}
