-- Assessments preserve the evidence and policy at decision time, even if source
-- reports are subsequently removed. There is intentionally no cascading FK.
CREATE TABLE triage_assessments (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    repository text NOT NULL,
    author text NOT NULL CHECK (length(author) > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    assessment jsonb NOT NULL CHECK (jsonb_typeof(assessment) = 'object'),
    CHECK (assessment ? 'can_unblock' AND assessment->'can_unblock' = 'false'::jsonb)
);
CREATE INDEX triage_assessments_repository_created_idx
    ON triage_assessments (repository, created_at DESC);

CREATE FUNCTION triage_assessments_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'triage assessments are append-only';
END;
$$;
CREATE TRIGGER triage_assessments_immutable_trg
    BEFORE UPDATE OR DELETE ON triage_assessments
    FOR EACH ROW EXECUTE FUNCTION triage_assessments_immutable();
CREATE TRIGGER triage_assessments_no_truncate_trg
    BEFORE TRUNCATE ON triage_assessments
    FOR EACH STATEMENT EXECUTE FUNCTION triage_assessments_immutable();
