-- Runs submitted to the JobDock queue. A run is one execution of an approved
-- suite snapshot against server-configured targets. The snapshot holds the
-- suite YAML exactly as approved; suites reference credentials by
-- environment variable name and never contain secret values.
CREATE TABLE runs (
    id                  bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id           text        NOT NULL UNIQUE,
    suite_name          text        NOT NULL,
    suite_file          text        NOT NULL,
    suite_sha256        text        NOT NULL CHECK (suite_sha256 ~ '^[0-9a-f]{64}$'),
    suite_snapshot      bytea       NOT NULL,
    mode                text        NOT NULL CHECK (mode IN ('run', 'compare')),
    targets             text[]      NOT NULL,
    writes_requested    boolean     NOT NULL DEFAULT false,
    mutating            boolean     NOT NULL DEFAULT false,
    status              text        NOT NULL DEFAULT 'queued'
                        CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted')),
    attempt             integer     NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    max_attempts        integer     NOT NULL CHECK (max_attempts BETWEEN 1 AND 5),
    lease_owner         text,
    -- lease_token is the fencing token. It only ever increases (once per
    -- claim) and every worker write is conditional on it.
    lease_token         bigint      NOT NULL DEFAULT 0,
    lease_expires_at    timestamptz,
    heartbeat_at        timestamptz,
    cancel_requested_at timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    started_at          timestamptz,
    finished_at         timestamptz,
    error               text,
    summary             jsonb,
    report              bytea,
    report_truncated    boolean     NOT NULL DEFAULT false,
    resubmitted_from    bigint      REFERENCES runs (id) ON DELETE SET NULL,
    CONSTRAINT runs_writes_explicit CHECK (NOT mutating OR writes_requested),
    CONSTRAINT runs_mutating_single_attempt CHECK (NOT mutating OR max_attempts = 1),
    CONSTRAINT runs_lease_present CHECK (
        status <> 'running' OR (lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CONSTRAINT runs_finished_at_terminal CHECK (
        (status IN ('succeeded', 'failed', 'cancelled', 'interrupted')) = (finished_at IS NOT NULL))
);

-- Claim scans queued runs oldest first.
CREATE INDEX runs_queued_idx ON runs (created_at, id) WHERE status = 'queued';

-- Recovery scans running runs by lease expiry.
CREATE INDEX runs_lease_idx ON runs (lease_expires_at) WHERE status = 'running';
