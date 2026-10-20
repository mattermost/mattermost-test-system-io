-- A worker that leaves a run without ever holding a lease: it started after
-- the queue drained (409 RUN_NOT_IN_PROGRESS) or gave up polling an empty
-- queue. It ran no tests, so it uploads no report. Recording it lets the
-- run's report group reach total_reports_expected; see
-- reports.TryAutoCompleteGroup. A worker that crashes before it reaches the
-- orchestrator, or after it leased units, records nothing and still leaves
-- the group short.
CREATE TABLE orchestration_no_work_workers (
    run_id       uuid        NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
    -- Mirrors leases.gh_job_name / leases.gh_job_id.
    gh_job_name  text        NOT NULL CHECK (length(gh_job_name) BETWEEN 1 AND 256),
    gh_job_id    text        NOT NULL CHECK (length(gh_job_id) BETWEEN 1 AND 64),
    recorded_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, gh_job_id)
);
