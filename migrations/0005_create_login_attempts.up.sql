-- Login attempts counted by the throttle. The throttle key (for example a
-- client address) is stored as a SHA-256 digest; the table only needs to
-- group attempts, not to recover the key.
CREATE TABLE login_attempts (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key_sha256   bytea       NOT NULL CHECK (octet_length(key_sha256) = 32),
    attempted_at timestamptz NOT NULL
);

CREATE INDEX login_attempts_key_idx ON login_attempts (key_sha256, attempted_at);
