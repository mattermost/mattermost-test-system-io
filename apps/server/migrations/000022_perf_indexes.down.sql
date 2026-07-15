-- Reverse 000022: restore the redundant indexes and drop the new ones.
CREATE INDEX IF NOT EXISTS report_groups_created_idx ON report_groups (created_at DESC);
CREATE INDEX IF NOT EXISTS report_groups_repository_idx ON report_groups (repository);

DROP INDEX IF EXISTS dispatch_units_current_lease_idx;
DROP INDEX IF EXISTS reports_created_at_idx;
DROP INDEX IF EXISTS report_json_files_report_id_created_idx;
