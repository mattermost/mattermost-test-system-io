CREATE TABLE verdicts (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    report_group_id uuid        REFERENCES report_groups(id) ON DELETE SET NULL,
    repository      text        NOT NULL,
    test_id         uuid        REFERENCES tests(id),
    signature_id    uuid        REFERENCES failure_signatures(id),
    label           text        NOT NULL,
    decided_by      text,
    rule            text,
    confidence      numeric,
    summary         text,
    model           text,
    triage_url      text,
    error_context   jsonb,
    reused_from_id  uuid        REFERENCES verdicts(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    superseded_at   timestamptz
);

CREATE UNIQUE INDEX verdicts_current_uq ON verdicts (
    report_group_id,
    COALESCE(test_id, '00000000-0000-0000-0000-000000000000'::uuid),
    COALESCE(signature_id, '00000000-0000-0000-0000-000000000000'::uuid)
) WHERE superseded_at IS NULL;

CREATE INDEX verdicts_signature_current_idx
    ON verdicts (signature_id, created_at DESC)
    WHERE superseded_at IS NULL;

CREATE INDEX verdicts_repo_created_idx
    ON verdicts (repository, created_at DESC)
    WHERE superseded_at IS NULL;

CREATE INDEX failure_signatures_merged_into_idx
    ON failure_signatures (merged_into_id)
    WHERE merged_into_id IS NOT NULL;
