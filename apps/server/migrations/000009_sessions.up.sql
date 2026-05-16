CREATE TABLE sessions (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    token_hash   text        NOT NULL UNIQUE,
    user_id      uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issued_at    timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    ip           inet,
    user_agent   text,
    revoked_at   timestamptz
);
CREATE INDEX sessions_user_expires_idx ON sessions (user_id, expires_at);
