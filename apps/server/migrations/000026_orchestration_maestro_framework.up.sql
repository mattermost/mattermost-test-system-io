-- Widen the orchestration_runs.framework CHECK constraint to admit Maestro
-- alongside Playwright, Cypress, and Detox. Mirrors 000025's rationale: the
-- orchestration tables are framework-agnostic in shape (spec_path is
-- opaque — for Maestro it's a flow .yml file — and the lease/attempt
-- machinery doesn't care about the runner). Maestro's report_groups.framework
-- CHECK already admits 'maestro' (see 000022) for the no-queue
-- report-upload path; this migration extends the same label to the
-- dispatch-queue path.
ALTER TABLE orchestration_runs
    DROP CONSTRAINT orchestration_runs_framework_check;

ALTER TABLE orchestration_runs
    ADD CONSTRAINT orchestration_runs_framework_check
    CHECK (framework IN ('playwright', 'cypress', 'detox', 'maestro'));
