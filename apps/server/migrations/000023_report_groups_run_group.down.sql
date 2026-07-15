DROP INDEX IF EXISTS report_groups_run_group_idx;
ALTER TABLE report_groups DROP COLUMN IF EXISTS run_group;
