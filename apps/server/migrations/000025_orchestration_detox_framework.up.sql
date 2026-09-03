-- Widen the orchestration_runs.framework CHECK constraint to admit Detox
-- alongside Playwright and Cypress. Mirrors 000020's rationale: the
-- orchestration tables are framework-agnostic in shape (spec_path is
-- opaque, the lease/attempt machinery doesn't care about the runner, and
-- test_cases JSONB accepts any reporter shape). Detox's report_groups.framework
-- CHECK already admits 'detox' (see 000015/000022) for the no-queue
-- report-upload path; this migration extends the same label to the
-- dispatch-queue path.
ALTER TABLE orchestration_runs
    DROP CONSTRAINT orchestration_runs_framework_check;

ALTER TABLE orchestration_runs
    ADD CONSTRAINT orchestration_runs_framework_check
    CHECK (framework IN ('playwright', 'cypress', 'detox'));
