-- A dispatch_unit is one row per spec file submitted by the caller at
-- `begin run`. Carries the per-unit queue state. dispatch_seq is the
-- caller's submission order (0-indexed) and serves as the FIFO checkout key.
CREATE TABLE dispatch_units (
    id               uuid        PRIMARY KEY DEFAULT uuidv7(),
    run_id           uuid        NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
    dispatch_seq     integer     NOT NULL,
    spec_path        text        NOT NULL CHECK (length(spec_path) > 0),
    state            text        NOT NULL DEFAULT 'pending'
                                 CHECK (state IN ('pending','leased',
                                                  'completed_pass','completed_fail',
                                                  'completed_skipped','abandoned')),
    -- current_lease_id is non-NULL only when state = 'leased'. The FK is
    -- non-deferrable: the leases table is created in the prior migration, and
    -- within a runtime transaction the lease INSERT runs before the UPDATE
    -- that sets this column, so the FK is satisfied at statement-end time.
    current_lease_id uuid        REFERENCES leases(id) ON DELETE SET NULL,
    -- Total leases ever issued for this unit. Includes both timeout-driven
    -- re-leases and retest-driven re-leases. Surfaced in the run-status and
    -- per-run UI views for diagnostics.
    lease_count      integer     NOT NULL DEFAULT 0,
    -- Number of leases that completed with reported `failed` status. Used with
    -- runs.retest_budget for retest eligibility. Timeouts do NOT increment this
    -- — only definitive `failed` reports.
    fail_count       integer     NOT NULL DEFAULT 0,
    outcome_set_at   timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT dispatch_units_lease_ck CHECK (
        (state = 'leased') = (current_lease_id IS NOT NULL)
    ),
    CONSTRAINT dispatch_units_outcome_ck CHECK (
        (state IN ('completed_pass','completed_fail','completed_skipped','abandoned'))
        = (outcome_set_at IS NOT NULL)
    )
);

-- FIFO sort key + identity within a run: drives the ORDER BY dispatch_seq in
-- checkout queries and prevents duplicate seq values per run.
CREATE UNIQUE INDEX dispatch_units_run_seq_idx ON dispatch_units (run_id, dispatch_seq);

-- Checkout query: scan only pending units within a run, in seq order.
CREATE INDEX dispatch_units_run_state_idx
    ON dispatch_units (run_id, state, dispatch_seq)
    WHERE state = 'pending';
