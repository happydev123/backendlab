-- Operator sessions. Only the SHA-256 digest of the session token is
-- stored, so a database read does not yield usable session cookies. The
-- CSRF token is stored as issued: it is useless without the session token.
CREATE TABLE sessions (
    token_sha256 bytea       PRIMARY KEY CHECK (octet_length(token_sha256) = 32),
    csrf_token   text        NOT NULL,
    created_at   timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    CONSTRAINT sessions_expiry_after_creation CHECK (expires_at > created_at)
);

CREATE INDEX sessions_expires_idx ON sessions (expires_at);
