DROP INDEX IF EXISTS attempts_run_unit_created_idx;
DROP INDEX IF EXISTS report_groups_repository_created_at_idx;
DROP INDEX IF EXISTS report_groups_created_at_desc_idx;

ALTER TABLE report_groups
    DROP COLUMN IF EXISTS last_summary_at,
    DROP COLUMN IF EXISTS total_duration_ms,
    DROP COLUMN IF EXISTS reports_count,
    DROP COLUMN IF EXISTS orchestration_json,
    DROP COLUMN IF EXISTS test_stats_json;
