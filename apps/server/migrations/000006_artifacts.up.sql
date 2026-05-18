CREATE TABLE artifacts (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    test_case_id uuid        NOT NULL REFERENCES test_cases(id) ON DELETE CASCADE,
    kind         text        NOT NULL CHECK (kind IN ('screenshot','trace','video','log','other')),
    content_type text        NOT NULL,
    object_key   text        NOT NULL UNIQUE,
    size_bytes   bigint      NOT NULL CHECK (size_bytes >= 0),
    sha256       text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX artifacts_case_kind_idx ON artifacts (test_case_id, kind);
