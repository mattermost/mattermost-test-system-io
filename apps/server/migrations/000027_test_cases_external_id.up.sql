-- test_cases.external_test_id — the stable, human-assigned case ID (Mattermost
-- uses "MM-T1234" / "MM-T1234_5").
--
-- Per-test history has to be keyed on this rather than on title: titles get
-- reworded and a title-keyed series silently breaks at the rename, which is
-- exactly the point at which the history is most needed. Populated at
-- consolidation (internal/testreport.ExternalTestID) and backfilled below.
-- Left nullable — frameworks and repositories that do not use the convention
-- fall back to the title via stable_key (migration 28).

ALTER TABLE test_cases ADD COLUMN external_test_id text;

-- Backfill. Non-concurrent on purpose: migrations run in a transaction, and the
-- regex is cheap. If test_cases has grown past the point where a single UPDATE is
-- acceptable, run this in batches out-of-band and ship the migration with the
-- ALTER + index only.
-- \y is Postgres's word boundary, matching the \b that internal/testreport's Go
-- extractor anchors on. Without it this backfill would extract "MM-T123" from
-- "XMM-T123" while the Go path — which populates every row written after this
-- migration — would not, so backfilled and live rows would disagree about the
-- same title. The group stays non-capturing: substring() returns the captured
-- group when the pattern has one, which would truncate the "_N" sub-case.
--
-- Mirrors internal/testreport.ExternalTestID exactly: try full_title first,
-- and fall back to title only when full_title has no match — not only when
-- full_title is empty. A non-Playwright report can carry a full_title that
-- does not simply prefix the title (Cypress's fullTitle comes straight off
-- the mochawesome JSON), so an id present on the title alone must still be
-- found rather than backfilling to NULL while the Go path finds it.
UPDATE test_cases
SET external_test_id = coalesce(
    substring(full_title from '\yMM-T[0-9]+(?:_[0-9]+)?'),
    substring(title from '\yMM-T[0-9]+(?:_[0-9]+)?')
)
WHERE external_test_id IS NULL;

-- Partial index: the majority of rows in a non-Mattermost-convention repository
-- will be NULL and should not be indexed.
CREATE INDEX test_cases_external_id_idx
    ON test_cases (external_test_id)
    WHERE external_test_id IS NOT NULL;

-- Report-group time-ordering for the history window. The existing
-- report_groups_created_idx is global; history is always scoped to a repo+branch
-- first, so give that shape its own index.
CREATE INDEX report_groups_repo_branch_created_idx
    ON report_groups (repository, branch, created_at DESC);
