-- Per-test history across commits, and the AI-triage verdict ledger.
--
-- Two additions:
--
-- 1. test_cases.external_test_id — the stable, human-assigned case ID (Mattermost
--    uses "MM-T1234" / "MM-T1234_5"). History has to be keyed on this rather than
--    on title: titles get reworded and a title-keyed series silently breaks at the
--    rename, which is exactly the point at which the history is most needed.
--    Populated at consolidation (internal/testreport.ExternalTestID) and backfilled
--    below. Left nullable — frameworks and repos that do not use the convention
--    simply have no history series.
--
-- 2. triage_verdicts — one row per (run, cluster-or-test) triage decision. This is
--    the ledger behind flake amnesty (how many times has this test been auto-waived
--    recently?) and behind the false-green metric (a verdict that a human later
--    corrected). It deliberately lives here rather than as a file in the tested
--    repo: a repo file would need a commit from CI, would conflict across parallel
--    PRs, and would race.

ALTER TABLE test_cases ADD COLUMN external_test_id text;

-- Backfill. Non-concurrent on purpose: migrations run in a transaction, and the
-- regex is cheap. If test_cases has grown past the point where a single UPDATE is
-- acceptable, run this in batches out-of-band and ship the migration with the
-- ALTER + index only.
UPDATE test_cases
SET external_test_id = substring(coalesce(nullif(full_title, ''), title) from 'MM-T[0-9]+(?:_[0-9]+)?')
WHERE external_test_id IS NULL;

-- History lookups are always (external_test_id → recent rows), joined back up to
-- report_groups for repo/branch/commit. Partial index: the majority of rows in a
-- non-Mattermost-convention repo will be NULL and should not be indexed.
CREATE INDEX test_cases_external_id_idx
    ON test_cases (external_test_id)
    WHERE external_test_id IS NOT NULL;

-- Report-group time-ordering for the history window. The existing
-- report_groups_created_idx is global; history is always scoped to a repo+branch
-- first, so give that shape its own index.
CREATE INDEX report_groups_repo_branch_created_idx
    ON report_groups (repository, branch, created_at DESC);

CREATE TABLE triage_verdicts (
    id                uuid        PRIMARY KEY DEFAULT uuidv7(),
    repository        text        NOT NULL,
    branch            text        NOT NULL DEFAULT '',
    commit_sha        text        NOT NULL,
    gh_run_id         text        NOT NULL DEFAULT '',
    gh_pr_number      integer,
    -- NULL for suite-level verdicts (a whole shard died); set for test-level ones.
    external_test_id  text,
    -- Failure-signature hash. Members of one cluster share it, so a suite-wide
    -- verdict can be attributed back to every test it covered.
    cluster_signature text,
    -- Number of failing tests this verdict covers. 1 for a single test.
    member_count      integer     NOT NULL DEFAULT 1 CHECK (member_count >= 0),
    verdict           text        NOT NULL CHECK (verdict IN (
                                      'PR_REGRESSION','MAIN_REGRESSION','FLAKY_TEST',
                                      'FLAKY_INFRA','FLAKY_SERVER','BUILD_OR_ENV_ERROR',
                                      'TEST_DEBT','INCONCLUSIVE')),
    confidence        numeric(4,3) NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    -- Volume tier the triage ran at (0-4); records how much evidence was affordable.
    tier              smallint    CHECK (tier IS NULL OR (tier >= 0 AND tier <= 4)),
    root_cause        text,
    evidence          jsonb       NOT NULL DEFAULT '[]'
                                  CHECK (jsonb_typeof(evidence) = 'array'),
    suspect_commit    text,
    -- The commit status this verdict produced.
    check_state       text        NOT NULL DEFAULT 'failure'
                                  CHECK (check_state IN ('success','failure','pending','error')),
    -- TRUE when the verdict turned a red result green (an AI waiver). This is the
    -- column the false-green metric counts.
    waived            boolean     NOT NULL DEFAULT FALSE,
    model             text,
    -- Human correction, written by the override command. A non-null value means
    -- the original verdict was wrong and is a labelled training example.
    corrected_verdict text        CHECK (corrected_verdict IS NULL OR corrected_verdict IN (
                                      'PR_REGRESSION','MAIN_REGRESSION','FLAKY_TEST',
                                      'FLAKY_INFRA','FLAKY_SERVER','BUILD_OR_ENV_ERROR',
                                      'TEST_DEBT','INCONCLUSIVE')),
    corrected_by      text,
    corrected_reason  text,
    corrected_at      timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT triage_verdicts_correction_ck CHECK (
        (corrected_verdict IS NULL AND corrected_at IS NULL) OR
        (corrected_verdict IS NOT NULL AND corrected_at IS NOT NULL)
    )
);

-- Idempotency: re-running triage for the same run + cluster + test must update,
-- not accumulate. NULLs are distinct in a plain unique index, so use the
-- NULLS NOT DISTINCT form (PG15+) to make repeated suite-level verdicts collide.
CREATE UNIQUE INDEX triage_verdicts_run_key_idx
    ON triage_verdicts (repository, commit_sha, gh_run_id, cluster_signature, external_test_id)
    NULLS NOT DISTINCT;

-- Amnesty lookup: waived flake verdicts for one test inside a time window.
CREATE INDEX triage_verdicts_amnesty_idx
    ON triage_verdicts (repository, external_test_id, created_at DESC)
    WHERE waived AND external_test_id IS NOT NULL;

-- Accuracy metrics: verdicts a human later corrected.
CREATE INDEX triage_verdicts_corrected_idx
    ON triage_verdicts (repository, created_at DESC)
    WHERE corrected_verdict IS NOT NULL;
