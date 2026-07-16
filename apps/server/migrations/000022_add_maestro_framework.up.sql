-- Add 'maestro' to the report_groups.framework CHECK constraint to support
-- Maestro as a test framework alongside Playwright, Cypress, and Detox.
ALTER TABLE report_groups
    DROP CONSTRAINT report_groups_framework_check;

ALTER TABLE report_groups
    ADD CONSTRAINT report_groups_framework_check
    CHECK (framework IN ('playwright','cypress','detox','maestro'));
