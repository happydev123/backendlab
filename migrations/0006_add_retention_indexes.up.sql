-- Indexes for retention cleanup and for the run list filtered by status.

-- Retention deletes terminal runs by finish time.
CREATE INDEX runs_finished_idx ON runs (finished_at) WHERE finished_at IS NOT NULL;

-- The run list pages newest first, optionally filtered by status.
CREATE INDEX runs_created_idx ON runs (created_at DESC, id DESC);
CREATE INDEX runs_status_created_idx ON runs (status, created_at DESC, id DESC);

-- Deleting a run sets resubmitted_from to NULL on runs created from it.
CREATE INDEX runs_resubmitted_from_idx ON runs (resubmitted_from) WHERE resubmitted_from IS NOT NULL;

-- Throttle cleanup deletes old attempts by time.
CREATE INDEX login_attempts_time_idx ON login_attempts (attempted_at);
