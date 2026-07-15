CREATE TABLE api_keys (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    name          text        NOT NULL,
    key_prefix    text        NOT NULL UNIQUE,
    key_hash      text        NOT NULL,
    status        text        NOT NULL CHECK (status IN ('active', 'rotating', 'revoked')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz,
    revoked_at    timestamptz
);
CREATE INDEX api_keys_status_idx ON api_keys (status);
