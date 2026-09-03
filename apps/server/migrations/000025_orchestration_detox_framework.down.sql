-- Revert the orchestration_runs.framework CHECK back to playwright/cypress
-- only. If any detox orchestration_runs rows exist, this migration will
-- fail when the new CHECK is added — those rows MUST be removed (or
-- migrated to a different framework value the application supports)
-- before applying this DOWN.
ALTER TABLE orchestration_runs
    DROP CONSTRAINT orchestration_runs_framework_check;

ALTER TABLE orchestration_runs
    ADD CONSTRAINT orchestration_runs_framework_check
    CHECK (framework IN ('playwright', 'cypress'));
