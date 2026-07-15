CREATE TABLE refresh_tokens (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    session_id  uuid        NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    token_hash  text        NOT NULL UNIQUE,
    issued_at   timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    replaced_by uuid        REFERENCES refresh_tokens(id)
);
CREATE INDEX refresh_tokens_session_idx ON refresh_tokens (session_id);
