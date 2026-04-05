-- Per-case results written by the worker while a run executes. Each row
-- records the fencing token of the attempt that wrote it; a claim removes
-- rows left by earlier attempts so the table reflects one attempt only.
CREATE TABLE case_results (
    run_id           bigint      NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    case_index       integer     NOT NULL CHECK (case_index >= 0),
    case_id          text        NOT NULL,
    status           text        NOT NULL
                     CHECK (status IN ('passed', 'failed', 'error', 'skipped', 'inconclusive', 'cancelled')),
    findings         jsonb       NOT NULL DEFAULT '[]',
    findings_dropped integer     NOT NULL DEFAULT 0 CHECK (findings_dropped >= 0),
    observations     jsonb       NOT NULL DEFAULT '[]',
    -- bodies maps target name to a bounded, redacted response body. It is
    -- NULL unless the server enables retention.retain_raw_bodies.
    bodies           jsonb,
    written_token    bigint      NOT NULL,
    recorded_at      timestamptz NOT NULL,
    PRIMARY KEY (run_id, case_index)
);
