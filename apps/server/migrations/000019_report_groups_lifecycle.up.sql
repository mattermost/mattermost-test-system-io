-- Report-group lifecycle additions split out of 000002 so existing databases
-- (where 000002 already ran with the original schema) pick up the new columns,
-- the `incomplete` status enum value, and the staleness index. The auto-finalize
-- predicate (count(reports.complete) >= total_reports_expected) flips
-- in_progress → completed; the staleness reaper flips in_progress → incomplete
-- after no upload activity within the configured idle window.

ALTER TABLE report_groups
    ADD COLUMN total_reports_expected integer
        CHECK (total_reports_expected IS NULL OR total_reports_expected > 0);

ALTER TABLE report_groups
    ADD COLUMN last_upload_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE report_groups DROP CONSTRAINT report_groups_status_check;
ALTER TABLE report_groups
    ADD CONSTRAINT report_groups_status_check
    CHECK (status IN ('in_progress','completed','incomplete'));

CREATE INDEX report_groups_staleness_idx
    ON report_groups (last_upload_at)
    WHERE status = 'in_progress';
