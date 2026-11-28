DROP INDEX IF EXISTS kpi_daily_test_executions_test_date_idx;
DROP TABLE IF EXISTS kpi_daily_test_executions;

DROP INDEX IF EXISTS kpi_test_commit_outcomes_test_idx;
DROP INDEX IF EXISTS kpi_test_commit_outcomes_flaky_idx;
DROP INDEX IF EXISTS kpi_test_commit_outcomes_app_date_idx;
DROP TABLE IF EXISTS kpi_test_commit_outcomes;

DROP INDEX IF EXISTS kpi_daily_signature_metrics_app_date_idx;
DROP INDEX IF EXISTS kpi_daily_signature_metrics_sig_date_idx;
DROP TABLE IF EXISTS kpi_daily_signature_metrics;

DROP INDEX IF EXISTS kpi_daily_lane_metrics_date_idx;
DROP INDEX IF EXISTS kpi_daily_lane_metrics_app_date_idx;
DROP TABLE IF EXISTS kpi_daily_lane_metrics;

DROP INDEX IF EXISTS kpi_group_signature_rollups_bucket_idx;
DROP TABLE IF EXISTS kpi_group_signature_rollups;

DROP INDEX IF EXISTS kpi_group_rollups_stage_created_idx;
DROP INDEX IF EXISTS kpi_group_rollups_bucket_idx;
DROP TABLE IF EXISTS kpi_group_rollups;

DROP VIEW IF EXISTS kpi_test_results;

DROP INDEX IF EXISTS verdicts_group_idx;
DROP INDEX IF EXISTS report_groups_kpi_unrefreshed_idx;

ALTER TABLE report_groups
    DROP COLUMN IF EXISTS kpi_rollups_version,
    DROP COLUMN IF EXISTS kpi_summary_version,
    DROP COLUMN IF EXISTS kpi_rollups_refreshed_at;
