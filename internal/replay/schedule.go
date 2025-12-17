package replay

import (
	"sync"

	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/suite"
)

// notStarted marks a case result that was never executed; it is replaced
// with cancelled or skipped once the run ends.
const notStarted report.CaseStatus = ""

// mustRunSequentially reports whether cases must run one at a time in
// suite order: when any case captures or references variables (later
// cases depend on earlier ones), or when any case is mutating (side
// effects must happen in the written order).
func mustRunSequentially(s *suite.Suite) bool {
	for i := range s.Cases {
		c := &s.Cases[i]
		if c.Mutating() || len(c.Capture) > 0 {
			return true
		}
		if refs, err := c.Refs(); err != nil || len(refs) > 0 {
			return true
		}
	}
	return false
}

// schedule runs every case through runCase, sequentially or with a pool
// of workers goroutines, and returns results in suite order. Once the run
// context is done no further case is started; their results keep the
// notStarted status. Every goroutine started here has finished when
// schedule returns.
func (rs *runState) schedule(sequential bool, workers int, runCase func(idx int) CaseResult) []CaseResult {
	n := len(rs.l.Suite.Cases)
	results := make([]CaseResult, n)
	for i := range results {
		c := &rs.l.Suite.Cases[i]
		results[i] = CaseResult{CaseID: c.ID, Index: i, Mutating: c.Mutating(), Status: notStarted}
	}
	var emitMu sync.Mutex
	finish := func(i int, r CaseResult) {
		results[i] = r
		if cb := rs.e.opts.OnCase; cb != nil {
			emitMu.Lock()
			defer emitMu.Unlock()
			cb(r)
		}
	}

	if sequential || workers <= 1 {
		for i := 0; i < n; i++ {
			if rs.run.Err() != nil {
				break
			}
			finish(i, runCase(i))
		}
		return results
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < min(workers, n); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if rs.run.Err() != nil {
					continue
				}
				finish(i, runCase(i))
			}
		}()
	}
feed:
	for i := 0; i < n; i++ {
		select {
		case jobs <- i:
		case <-rs.run.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	return results
}

// settleNotStarted gives never-executed cases their final status:
// cancelled when the caller cancelled the run, skipped when a deadline
// ended it. It reports whether any case was not started.
func (rs *runState) settleNotStarted(results []CaseResult) bool {
	status := report.CaseSkipped
	if rs.cancelledByCaller() {
		status = report.CaseCancelled
	}
	found := false
	for i := range results {
		if results[i].Status == notStarted {
			results[i].Status = status
			found = true
		}
	}
	return found
}
