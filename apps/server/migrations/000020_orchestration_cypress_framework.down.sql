-- Revert the orchestration_runs.framework CHECK back to playwright-only.
-- If any cypress orchestration_runs rows exist in the database, this
-- migration will fail when the new CHECK is added — those rows MUST be
-- removed (or migrated to a different framework value the application
-- supports) before applying this DOWN.
ALTER TABLE orchestration_runs
    DROP CONSTRAINT orchestration_runs_framework_check;

ALTER TABLE orchestration_runs
    ADD CONSTRAINT orchestration_runs_framework_check
    CHECK (framework = 'playwright');
