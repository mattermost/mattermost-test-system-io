-- Additive performance indexes for high-load KPI rollups & queries (Wave 1).

-- Avoid sequential scans on DELETE FROM kpi_test_commit_outcomes WHERE repository=$1 AND commit_sha=$2.
CREATE INDEX IF NOT EXISTS kpi_test_commit_outcomes_repo_sha_idx
    ON kpi_test_commit_outcomes (repository, commit_sha);

-- Accelerate PR rerun / settlement lookups filtered by (repository, gh_pr_number, lane, platform).
CREATE INDEX IF NOT EXISTS kpi_group_rollups_pr_green_lookup_idx
    ON kpi_group_rollups (repository, gh_pr_number, lane, is_all_platforms, platform, group_created_at)
    WHERE is_completed = true AND is_red = false;

-- Accelerate cross-app / date-first flaky lookups where is_mixed is true.
CREATE INDEX IF NOT EXISTS kpi_test_commit_outcomes_mixed_date_idx
    ON kpi_test_commit_outcomes (metric_date DESC, test_id, project, platform)
    WHERE is_mixed;
