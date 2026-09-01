-- W3 — blind waiver audit.
--
-- The blind audit is the human measurement behind the rollout ladder: a
-- reviewer sees the failure and its evidence WITHOUT the AI verdict, submits
-- their own call, and only then learns what the AI said. Agreement rate over a
-- trailing window is what W13 gates promotion on; disagreement rows are the
-- labeled examples false-green review feeds on.
--
-- One review per (verdict, reviewer): a second look by the same person is an
-- edit in place (upsert), not a second sample. Deleting a verdict cascades —
-- an audit of a verdict that no longer exists is not evidence.

CREATE TABLE triage_audit_reviews (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    verdict_id    uuid        NOT NULL REFERENCES triage_verdicts(id) ON DELETE CASCADE,
    repository    text        NOT NULL DEFAULT '',
    reviewer      text        NOT NULL,
    -- The human's blind call on the SAME question the AI answered: was this
    -- waiver right? true = agree with the waive, false = it should have stayed
    -- red. Storing agree (not a second verdict) is what makes the rate a
    -- measurement of the AI, not of the reviewer's vocabulary.
    human_agree   boolean     NOT NULL,
    note          text,
    -- Stratum the sampler drew the verdict from (flaky_test / main_regression
    -- / flaky_infra), so the pooled rate can be decomposed later.
    stratum       text        NOT NULL DEFAULT '',
    -- Set when the sampler force-included the row (W13 demotion window).
    force_included boolean    NOT NULL DEFAULT false,
    reviewed_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (verdict_id, reviewer)
);

CREATE INDEX triage_audit_reviews_time_idx ON triage_audit_reviews (reviewed_at DESC);
CREATE INDEX triage_audit_reviews_verdict_idx ON triage_audit_reviews (verdict_id);