DROP INDEX IF EXISTS kpi_group_signature_rollups_sig_date_idx;
ALTER TABLE kpi_group_signature_rollups
    DROP COLUMN IF EXISTS has_failed_test;

DROP INDEX IF EXISTS kpi_group_rollups_pr_red_idx;
DROP INDEX IF EXISTS kpi_group_rollups_rerun_idx;
ALTER TABLE kpi_group_rollups
    DROP COLUMN IF EXISTS gh_run_attempt_num,
    DROP COLUMN IF EXISTS gh_run_id;

DROP INDEX IF EXISTS kpi_e2e_lanes_repo_lower_idx;
DROP TABLE IF EXISTS kpi_e2e_lanes;
