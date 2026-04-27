-- An attempt is one row per (lease, spec file). Append-and-update: the row is
-- inserted when the lease is issued (status NULL) and updated by the worker's
-- `complete` call. lease_id is the join key; dispatch_unit_id and run_id are
-- denormalized for query convenience.
CREATE TABLE attempts (
    id                 uuid        PRIMARY KEY DEFAULT uuidv7(),
    lease_id           uuid        NOT NULL REFERENCES leases(id) ON DELETE CASCADE,
    dispatch_unit_id   uuid        NOT NULL REFERENCES dispatch_units(id) ON DELETE CASCADE,
    run_id             uuid        NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
    spec_path          text        NOT NULL,
    -- Aggregate per-spec status. NULL until reported. Enum mirrors
    -- test_cases.status (the broader per-test-case enum).
    status             text        CHECK (status IS NULL OR status IN
                                          ('passed','failed','skipped','flaky','timedOut','interrupted')),
    actual_duration_ms bigint      CHECK (actual_duration_ms IS NULL OR actual_duration_ms >= 0),
    -- File-level error (e.g. setup failure). Per-test-case errors live inside
    -- test_cases.
    error_message      text,
    error_stack        text,
    -- Optional framework-specific per-test-case detail. When non-null, an array
    -- of objects mirroring the columns on the existing test_cases table.
    -- Per-element shape is enforced at the application layer.
    test_cases         jsonb,
    reported_at        timestamptz,
    -- TRUE if reported_at > leases.deadline.
    late_report        boolean     NOT NULL DEFAULT FALSE,
    -- TRUE once the reaper marks the lease expired. Persists even if a later
    -- late report fills the status.
    expired            boolean     NOT NULL DEFAULT FALSE,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT attempts_status_reported_ck CHECK (
        (status IS NULL) OR (reported_at IS NOT NULL)
    ),
    CONSTRAINT attempts_test_cases_ck CHECK (
        test_cases IS NULL OR jsonb_typeof(test_cases) = 'array'
    )
);

-- Idempotency anchor: at most one row per (lease, spec_path).
CREATE UNIQUE INDEX attempts_lease_spec_idx ON attempts (lease_id, spec_path);

-- UI per-spec attempt history.
CREATE INDEX attempts_unit_idx ON attempts (dispatch_unit_id, created_at);

-- UI run-level summary.
CREATE INDEX attempts_run_idx ON attempts (run_id);
