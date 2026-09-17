CREATE TABLE triage_policies (
    repository text NOT NULL,
    context text NOT NULL,
    mode text NOT NULL DEFAULT 'shadow' CHECK (mode IN ('off','shadow','enforce')),
    thresholds jsonb NOT NULL,
    updated_by text,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repository, context)
);
