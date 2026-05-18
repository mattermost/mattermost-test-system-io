-- Widen the orchestration_runs.framework CHECK constraint to admit Cypress
-- alongside Playwright. The orchestration tables (orchestration_runs,
-- dispatch_units, leases, attempts) are framework-agnostic in shape:
-- spec_path is opaque, the lease/attempt machinery doesn't care about the
-- runner, and the test_cases JSONB accepts any reporter shape. The CHECK
-- below was the only DB-level barrier preventing Cypress runs from
-- registering. Detox is intentionally NOT added here; it is a separate
-- future change that pairs a CHECK widening with detox-specific schema
-- considerations not yet researched.
ALTER TABLE orchestration_runs
    DROP CONSTRAINT orchestration_runs_framework_check;

ALTER TABLE orchestration_runs
    ADD CONSTRAINT orchestration_runs_framework_check
    CHECK (framework IN ('playwright', 'cypress'));
