DROP INDEX IF EXISTS report_groups_staleness_idx;

-- Roll any 'incomplete' rows back to 'in_progress' before tightening the
-- enum, otherwise the CHECK re-add would fail on existing data.
UPDATE report_groups SET status = 'in_progress' WHERE status = 'incomplete';

ALTER TABLE report_groups DROP CONSTRAINT report_groups_status_check;
ALTER TABLE report_groups
    ADD CONSTRAINT report_groups_status_check
    CHECK (status IN ('in_progress','completed'));

ALTER TABLE report_groups DROP COLUMN IF EXISTS last_upload_at;
ALTER TABLE report_groups DROP COLUMN IF EXISTS total_reports_expected;
