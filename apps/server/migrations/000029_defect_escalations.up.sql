-- Product bugs E2E triage surfaced, as an append-only event log.
--
-- When a test is correct and the product is wrong, the agent does not touch the
-- test — it files a defect in the issue tracker and records that here.
--
-- WHAT THIS IS NOT: a mirror of the tracker. There is no resolved_at and no
-- "is it still open" question answered here, because the tracker answers that
-- authoritatively and a copy would go stale the moment somebody closed a ticket
-- — after which the test could never be escalated again when it regressed.
-- Deduplication is the agent's, against the tracker, by label.
--
-- WHAT IT IS: the metric. "How many product defects did E2E surface this month,
-- on which tests, and how long after the break" is a question about test
-- history, which is this service's job. An event that already happened cannot
-- become wrong, which is why this table is append-only.
CREATE TABLE triage_defect_escalations (
    id               uuid        PRIMARY KEY DEFAULT uuidv7(),
    repository       text        NOT NULL,
    external_test_id text        NOT NULL,
    -- Failure signature, so the same test failing two different ways counts as
    -- two defects rather than one.
    cluster_signature text,
    -- The tracker's own key (e.g. MM-12345) and URL. Recorded, never resolved.
    issue_key        text        NOT NULL,
    issue_url        text        NOT NULL,
    summary          text        NOT NULL DEFAULT '',
    -- The last_pass..failing_since range that justified it, when one existed.
    -- Null is the honest value for a break that predates the history window.
    suspect_range    text,
    escalated_by     text        NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now()
);

-- Per-test lookup ("has this test ever produced a defect, and which") and the
-- windowed repo-wide rollup that feeds the metric, from one index.
CREATE INDEX triage_defect_escalations_repo_test_idx
    ON triage_defect_escalations (repository, external_test_id, created_at DESC);
CREATE INDEX triage_defect_escalations_repo_time_idx
    ON triage_defect_escalations (repository, created_at DESC);
