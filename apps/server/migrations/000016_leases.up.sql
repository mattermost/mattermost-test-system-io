-- A lease is one row per checkout response: the orchestrator's grant of one or
-- more dispatch_units to a single worker for a bounded deadline. Workers are
-- identified by their GitHub Actions (gh_job_name, gh_job_id) tuple — the lease
-- UUID itself is internal and never exposed to workers.
CREATE TABLE leases (
    id                 uuid        PRIMARY KEY DEFAULT uuidv7(),
    run_id             uuid        NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
    -- Worker identity from GitHub Actions: workflow-defined job name + runtime
    -- numeric job id. Mirrors reports.gh_job_name / reports.gh_job_id.
    gh_job_name        text        NOT NULL CHECK (length(gh_job_name) BETWEEN 1 AND 256),
    gh_job_id          text        NOT NULL CHECK (length(gh_job_id) BETWEEN 1 AND 64),
    -- Denormalized list of dispatch_units covered by this lease. Cheap retrieval
    -- without a join; the authoritative state still lives on dispatch_units.
    unit_ids           uuid[]      NOT NULL CHECK (cardinality(unit_ids) >= 1),
    issued_at          timestamptz NOT NULL DEFAULT now(),
    deadline           timestamptz NOT NULL,
    released_at        timestamptz,
    release_reason     text        CHECK (release_reason IS NULL
                                          OR release_reason IN ('completed','expired','run_timed_out')),
    auth_oidc_subject  text,
    auth_api_key_id    uuid        REFERENCES api_keys(id) ON DELETE SET NULL,
    CONSTRAINT leases_release_ck CHECK (
        (released_at IS NULL) = (release_reason IS NULL)
    )
);

-- UI per-run timeline (most-recent-first).
CREATE INDEX leases_run_idx ON leases (run_id, issued_at DESC);

-- Reaper scan: find active leases whose deadline has passed.
CREATE INDEX leases_active_idx ON leases (deadline) WHERE released_at IS NULL;

-- At-most-one active lease per worker per run. Insert-time conflict on this
-- partial UNIQUE index produces the 409 when a worker calls `checkout` while
-- it already has an active lease.
CREATE UNIQUE INDEX leases_active_worker_uq
    ON leases (run_id, gh_job_id)
    WHERE released_at IS NULL;

-- `complete`-handler lookup: find the worker's most recent lease in the run,
-- preferring active and falling back to most recently released.
CREATE INDEX leases_worker_lookup_idx ON leases (run_id, gh_job_id, issued_at DESC);
