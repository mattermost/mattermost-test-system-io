CREATE TABLE users (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    github_id     bigint      NOT NULL UNIQUE,
    github_login  text        NOT NULL,
    email         text,
    display_name  text,
    avatar_url    text,
    role          text        NOT NULL DEFAULT 'viewer' CHECK (role IN ('viewer','editor','admin')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz
);
