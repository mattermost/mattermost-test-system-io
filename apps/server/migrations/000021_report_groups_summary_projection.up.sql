-- Denormalized summary projection for /api/v1/reports/grouped.
--
-- Stores the test_stats + orchestration roll-ups that the handler previously
-- recomputed per-row on every request (an N+1 join over reports + suites +
-- test_cases + attempts JSONB expansion). The projection is updated
-- transactionally at write time (RefreshGroupSummary), so the read path
-- becomes a single indexed range-scan with zero joins.
--
-- The columns mirror the response shape /reports/grouped already serializes;
-- no contract change at the API level.

ALTER TABLE report_groups
    ADD COLUMN test_stats_json       jsonb,
    ADD COLUMN orchestration_json    jsonb,
    ADD COLUMN reports_count         int  NOT NULL DEFAULT 0,
    -- Total wall-clock from begin → last attempt; never a sum of phases
    -- (setup, first-pass, retest can overlap due to per-failure re-dispatch).
    ADD COLUMN total_duration_ms     bigint,
    ADD COLUMN last_summary_at       timestamptz;

-- Index build strategy: plain CREATE INDEX (non-CONCURRENT). golang-migrate's
-- pgx5 driver wraps every migration in a transaction with no per-file opt-out,
-- and CONCURRENTLY cannot run inside a transaction. At current production scale
-- (~77 report_groups, ~88k attempts rows) the SHARE-lock window is well under
-- two seconds; concurrent worker writes are absorbed by the dispatch-run 5xx
-- retry added alongside this projection. Revisit (move to a manual CONCURRENTLY
-- step in the deploy runbook) if attempts grows past ~1M rows.

-- /reports/grouped pages by (created_at DESC, id DESC); id DESC is the
-- stable tie-breaker for the rare case of identical created_at values,
-- so pagination boundaries don't drift across pages. uuidv7 ids preserve
-- creation order, so the index is monotonic for both keys.
CREATE INDEX IF NOT EXISTS report_groups_created_at_desc_idx
    ON report_groups (created_at DESC, id DESC);

-- Per-repo sub-listing on the dashboard groups by repository within a page.
CREATE INDEX IF NOT EXISTS report_groups_repository_created_at_idx
    ON report_groups (repository, created_at DESC);

-- Composite covers aggregateOrchestrationDurations' WHERE run_id = $1
-- ORDER BY dispatch_unit_id, created_at ASC pattern in a single index-only
-- scan (existing attempts_run_idx + attempts_unit_idx force either a sort or
-- a filter-then-rescan).
CREATE INDEX IF NOT EXISTS attempts_run_unit_created_idx
    ON attempts (run_id, dispatch_unit_id, created_at);
