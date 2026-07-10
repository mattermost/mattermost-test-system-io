-- Revert the addition of 'maestro' to the framework enum.
ALTER TABLE report_groups
    DROP CONSTRAINT report_groups_framework_check;

ALTER TABLE report_groups
    ADD CONSTRAINT report_groups_framework_check
    CHECK (framework IN ('playwright','cypress','detox'));
