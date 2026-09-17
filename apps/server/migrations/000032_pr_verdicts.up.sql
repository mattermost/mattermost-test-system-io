CREATE TABLE pr_verdicts (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    report_group_id uuid NOT NULL REFERENCES report_groups(id) ON DELETE CASCADE,
    repository text NOT NULL,
    context text NOT NULL,
    gh_pr_number integer,
    head_sha text NOT NULL,
    base_ref text NOT NULL,
    base_sha text,
    lane text NOT NULL,
    mode text NOT NULL,
    verdict text NOT NULL CHECK (verdict IN ('SUCCESS','FAILURE','ACTION_REQUIRED','NEUTRAL','INCOMPLETE')),
    confidence double precision NOT NULL,
    counts jsonb NOT NULL,
    findings jsonb NOT NULL,
    thresholds_used jsonb NOT NULL,
    engine_version text NOT NULL,
    inputs_hash bytea NOT NULL,
    human_override jsonb,
    computed_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (report_group_id, engine_version, inputs_hash)
);
CREATE INDEX pr_verdicts_pr_idx ON pr_verdicts (repository, gh_pr_number, computed_at DESC);
